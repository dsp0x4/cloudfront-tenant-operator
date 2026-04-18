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
	"github.com/dsp0x4/cloudfront-tenant-operator/internal/tenantsource"
)

// DynamoDBClient defines the interface for scanning tenant data from DynamoDB.
type DynamoDBClient interface {
	ScanTenants(ctx context.Context, input *ScanTenantsInput) ([]tenantsource.TenantItem, error)
}

// ScanTenantsInput contains the parameters for scanning a DynamoDB table.
type ScanTenantsInput struct {
	TableName string

	// Required attribute mappings.
	NameAttribute    string
	DomainsAttribute string

	// Optional attribute mappings — only active when non-empty.
	EnabledAttribute           string
	ConnectionGroupIdAttribute string
	CertificateArnAttribute    string

	// Managed certificate overrides.
	ValidationTokenHostAttribute                      string
	PrimaryDomainNameAttribute                        string
	CertificateTransparencyLoggingPreferenceAttribute string

	// DNS overrides.
	DNSProviderAttribute      string
	HostedZoneIdAttribute     string
	DNSTTLAttribute           string
	DNSAssumeRoleArnAttribute string

	// Customization overrides.
	WebAclActionAttribute       string
	WebAclArnAttribute          string
	GeoRestrictionTypeAttribute string
	GeoLocationsAttribute       string

	// Parameters and tags (DynamoDB Map type).
	ParametersAttribute string
	TagsAttribute       string
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
func (c *RealDynamoDBClient) ScanTenants(ctx context.Context, input *ScanTenantsInput) ([]tenantsource.TenantItem, error) {
	start := time.Now()
	defer observeAWSLatency("DynamoDBScan", start)

	var items []tenantsource.TenantItem
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
func mapDynamoItem(item map[string]dbtypes.AttributeValue, input *ScanTenantsInput) (tenantsource.TenantItem, error) {
	tenant := tenantsource.TenantItem{}

	name, ok := getStringAttr(item, input.NameAttribute)
	if !ok || name == "" {
		return tenant, fmt.Errorf("missing required attribute %q", input.NameAttribute)
	}
	tenant.Name = name

	domains, ok := getDomainsAttr(item, input.DomainsAttribute)
	if !ok || len(domains) == 0 {
		return tenant, fmt.Errorf("missing required attribute %q for item %q", input.DomainsAttribute, name)
	}
	tenant.Domains = domains

	if input.EnabledAttribute != "" {
		if v, ok := getBoolAttr(item, input.EnabledAttribute); ok {
			tenant.Enabled = &v
		}
	}

	setOptionalString(item, input.ConnectionGroupIdAttribute, &tenant.ConnectionGroupId)
	setOptionalString(item, input.CertificateArnAttribute, &tenant.CertificateArn)

	setOptionalString(item, input.ValidationTokenHostAttribute, &tenant.ValidationTokenHost)
	setOptionalString(item, input.PrimaryDomainNameAttribute, &tenant.PrimaryDomainName)
	setOptionalString(item, input.CertificateTransparencyLoggingPreferenceAttribute, &tenant.CertificateTransparencyLoggingPreference)

	setOptionalString(item, input.DNSProviderAttribute, &tenant.DNSProvider)
	setOptionalString(item, input.HostedZoneIdAttribute, &tenant.HostedZoneId)
	setOptionalString(item, input.DNSAssumeRoleArnAttribute, &tenant.DNSAssumeRoleArn)

	if input.DNSTTLAttribute != "" {
		if v, ok := getNumberAttr(item, input.DNSTTLAttribute); ok {
			tenant.DNSTTL = &v
		}
	}

	setOptionalString(item, input.WebAclActionAttribute, &tenant.WebAclAction)
	setOptionalString(item, input.WebAclArnAttribute, &tenant.WebAclArn)
	setOptionalString(item, input.GeoRestrictionTypeAttribute, &tenant.GeoRestrictionType)

	if input.GeoLocationsAttribute != "" {
		if v, ok := getStringSetAttr(item, input.GeoLocationsAttribute); ok {
			tenant.GeoLocations = v
		}
	}

	if input.ParametersAttribute != "" {
		if v, ok := getMapStringAttr(item, input.ParametersAttribute); ok {
			tenant.Parameters = v
		}
	}

	if input.TagsAttribute != "" {
		if v, ok := getMapStringAttr(item, input.TagsAttribute); ok {
			tenant.Tags = v
		}
	}

	return tenant, nil
}

// setOptionalString reads a string attribute and sets the target pointer if
// the attribute mapping is configured and the value is non-empty.
func setOptionalString(item map[string]dbtypes.AttributeValue, attr string, target **string) {
	if attr == "" {
		return
	}
	if v, ok := getStringAttr(item, attr); ok && v != "" {
		*target = &v
	}
}

// getDomainsAttr reads a domains attribute that can be either a String (S) for
// a single domain or a StringSet (SS) for multiple domains.
func getDomainsAttr(item map[string]dbtypes.AttributeValue, key string) ([]string, bool) {
	av, ok := item[key]
	if !ok {
		return nil, false
	}
	if s, ok := av.(*dbtypes.AttributeValueMemberS); ok {
		if s.Value == "" {
			return nil, false
		}
		return []string{s.Value}, true
	}
	if ss, ok := av.(*dbtypes.AttributeValueMemberSS); ok {
		if len(ss.Value) == 0 {
			return nil, false
		}
		return ss.Value, true
	}
	return nil, false
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
	if s, ok := av.(*dbtypes.AttributeValueMemberS); ok {
		return strings.EqualFold(s.Value, "true"), true
	}
	return false, false
}

func getNumberAttr(item map[string]dbtypes.AttributeValue, key string) (int64, bool) {
	av, ok := item[key]
	if !ok {
		return 0, false
	}
	if n, ok := av.(*dbtypes.AttributeValueMemberN); ok {
		var v int64
		if _, err := fmt.Sscanf(n.Value, "%d", &v); err == nil {
			return v, true
		}
	}
	return 0, false
}

func getStringSetAttr(item map[string]dbtypes.AttributeValue, key string) ([]string, bool) {
	av, ok := item[key]
	if !ok {
		return nil, false
	}
	if ss, ok := av.(*dbtypes.AttributeValueMemberSS); ok && len(ss.Value) > 0 {
		return ss.Value, true
	}
	return nil, false
}

func getMapStringAttr(item map[string]dbtypes.AttributeValue, key string) (map[string]string, bool) {
	av, ok := item[key]
	if !ok {
		return nil, false
	}
	m, ok := av.(*dbtypes.AttributeValueMemberM)
	if !ok || len(m.Value) == 0 {
		return nil, false
	}
	result := make(map[string]string, len(m.Value))
	for k, v := range m.Value {
		if s, ok := v.(*dbtypes.AttributeValueMemberS); ok {
			result[k] = s.Value
		}
	}
	return result, len(result) > 0
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
