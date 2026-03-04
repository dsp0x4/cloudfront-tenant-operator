/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/aws/smithy-go"

	cfmetrics "github.com/dsp0x4/cloudfront-tenant-operator/internal/metrics"
)

// DynamoDBClient defines the interface for scanning tenant data from DynamoDB.
type DynamoDBClient interface {
	ScanTenants(ctx context.Context, input *ScanTenantsInput) ([]TenantItem, error)
}

// ScanTenantsInput contains the parameters for scanning a DynamoDB table.
type ScanTenantsInput struct {
	TableName string

	// Attribute mappings (DynamoDB attribute name -> TenantItem field).
	NameAttribute              string
	DomainAttribute            string
	EnabledAttribute           string
	ConnectionGroupIdAttribute string
	CertificateArnAttribute    string
}

// TenantItem represents a single tenant discovered from DynamoDB.
type TenantItem struct {
	Name              string
	Domain            string
	Enabled           *bool
	ConnectionGroupId *string
	CertificateArn    *string
}

// dynamoDBAPI defines the subset of the AWS DynamoDB SDK client we use.
type dynamoDBAPI interface {
	Scan(ctx context.Context, params *dynamodb.ScanInput, optFns ...func(*dynamodb.Options)) (*dynamodb.ScanOutput, error)
}

// RealDynamoDBClient is the production implementation of DynamoDBClient.
type RealDynamoDBClient struct {
	api dynamoDBAPI
}

// NewRealDynamoDBClient creates a new RealDynamoDBClient.
func NewRealDynamoDBClient(cfg aws.Config) *RealDynamoDBClient {
	return &RealDynamoDBClient{
		api: dynamodb.NewFromConfig(cfg),
	}
}

// NewRealDynamoDBClientForRegion creates a new RealDynamoDBClient configured
// for a specific AWS region, overriding the default config region.
func NewRealDynamoDBClientForRegion(cfg aws.Config, region string) *RealDynamoDBClient {
	return &RealDynamoDBClient{
		api: dynamodb.NewFromConfig(cfg, func(o *dynamodb.Options) {
			o.Region = region
		}),
	}
}

// ScanTenants performs a full Scan of the DynamoDB table and maps items to
// TenantItems using the attribute mappings in the input.
func (c *RealDynamoDBClient) ScanTenants(ctx context.Context, input *ScanTenantsInput) ([]TenantItem, error) {
	start := time.Now()
	defer observeAWSLatency("DynamoDBScan", start)

	var items []TenantItem
	var lastKey map[string]dbtypes.AttributeValue

	for {
		scanInput := &dynamodb.ScanInput{
			TableName:         aws.String(input.TableName),
			ExclusiveStartKey: lastKey,
		}
		out, err := c.api.Scan(ctx, scanInput)
		if err != nil {
			cfmetrics.ReconcileErrors.WithLabelValues("dynamodb_scan").Inc()
			return nil, classifyDynamoDBError(err)
		}

		for _, item := range out.Items {
			tenant, err := mapDynamoItem(item, input)
			if err != nil {
				continue
			}
			items = append(items, tenant)
		}

		if out.LastEvaluatedKey == nil {
			break
		}
		lastKey = out.LastEvaluatedKey
	}

	return items, nil
}

// mapDynamoItem converts a raw DynamoDB item to a TenantItem using the
// attribute mappings. Returns an error if required fields are missing.
func mapDynamoItem(item map[string]dbtypes.AttributeValue, input *ScanTenantsInput) (TenantItem, error) {
	tenant := TenantItem{}

	name, ok := getStringAttr(item, input.NameAttribute)
	if !ok || name == "" {
		return tenant, fmt.Errorf("missing required attribute %q", input.NameAttribute)
	}
	tenant.Name = name

	domain, ok := getStringAttr(item, input.DomainAttribute)
	if !ok || domain == "" {
		return tenant, fmt.Errorf("missing required attribute %q for item %q", input.DomainAttribute, name)
	}
	tenant.Domain = domain

	if input.EnabledAttribute != "" {
		if v, ok := getBoolAttr(item, input.EnabledAttribute); ok {
			tenant.Enabled = &v
		}
	}

	if input.ConnectionGroupIdAttribute != "" {
		if v, ok := getStringAttr(item, input.ConnectionGroupIdAttribute); ok && v != "" {
			tenant.ConnectionGroupId = &v
		}
	}

	if input.CertificateArnAttribute != "" {
		if v, ok := getStringAttr(item, input.CertificateArnAttribute); ok && v != "" {
			tenant.CertificateArn = &v
		}
	}

	return tenant, nil
}

func getStringAttr(item map[string]dbtypes.AttributeValue, key string) (string, bool) {
	av, ok := item[key]
	if !ok {
		return "", false
	}
	if s, ok := av.(*dbtypes.AttributeValueMemberS); ok {
		return s.Value, true
	}
	return "", false
}

func getBoolAttr(item map[string]dbtypes.AttributeValue, key string) (bool, bool) {
	av, ok := item[key]
	if !ok {
		return false, false
	}
	if b, ok := av.(*dbtypes.AttributeValueMemberBOOL); ok {
		return b.Value, true
	}
	// Also handle string "true"/"false" for flexibility
	if s, ok := av.(*dbtypes.AttributeValueMemberS); ok {
		return strings.EqualFold(s.Value, "true"), true
	}
	return false, false
}

// classifyDynamoDBError maps DynamoDB errors to domain error types.
func classifyDynamoDBError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		msg := apiErr.ErrorMessage()
		switch code {
		case awsErrCodeResourceNotFound:
			return fmt.Errorf("%w: %s", ErrDynamoDBTableNotFound, msg)
		case awsErrCodeAccessDenied:
			return fmt.Errorf("%w: %s", ErrAccessDenied, msg)
		case awsErrCodeThrottling, "ThrottlingException", "ProvisionedThroughputExceededException":
			return fmt.Errorf("%w: %s", ErrThrottling, msg)
		}
	}

	return err
}
