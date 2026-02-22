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
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/route53"
	r53types "github.com/aws/aws-sdk-go-v2/service/route53/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// route53API defines the subset of the AWS Route53 SDK client we use.
type route53API interface {
	ChangeResourceRecordSets(ctx context.Context, params *route53.ChangeResourceRecordSetsInput, optFns ...func(*route53.Options)) (*route53.ChangeResourceRecordSetsOutput, error)
	GetChange(ctx context.Context, params *route53.GetChangeInput, optFns ...func(*route53.Options)) (*route53.GetChangeOutput, error)
}

// RealRoute53Client is the production implementation of DNSClient that
// delegates to the AWS Route53 SDK.
type RealRoute53Client struct {
	api route53API
}

// NewRealRoute53Client creates a new RealRoute53Client. If assumeRoleArn is
// non-empty, the client uses STS AssumeRole to obtain temporary credentials
// for cross-account Route53 access. The credential provider handles automatic
// refresh before expiration.
func NewRealRoute53Client(cfg aws.Config, assumeRoleArn string) (*RealRoute53Client, error) {
	if assumeRoleArn != "" {
		stsClient := sts.NewFromConfig(cfg)
		provider := stscreds.NewAssumeRoleProvider(stsClient, assumeRoleArn)
		cfg = cfg.Copy()
		cfg.Credentials = aws.NewCredentialsCache(provider)
	}
	return &RealRoute53Client{
		api: route53.NewFromConfig(cfg),
	}, nil
}

func (c *RealRoute53Client) UpsertCNAMERecords(ctx context.Context, input *UpsertDNSRecordsInput) (*DNSChangeOutput, error) {
	start := time.Now()
	defer observeAWSLatency("Route53ChangeResourceRecordSets", start)

	changes := make([]r53types.Change, len(input.Records))
	for i, rec := range input.Records {
		changes[i] = r53types.Change{
			Action: r53types.ChangeActionUpsert,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name: aws.String(rec.Name),
				Type: r53types.RRTypeCname,
				TTL:  aws.Int64(rec.TTL),
				ResourceRecords: []r53types.ResourceRecord{
					{Value: aws.String(rec.Target)},
				},
			},
		}
	}

	out, err := c.api.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(input.HostedZoneId),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: changes,
			Comment: aws.String("Managed by cloudfront-tenant-operator"),
		},
	})
	if err != nil {
		return nil, classifyRoute53Error(err)
	}

	changeId := ""
	if out.ChangeInfo != nil && out.ChangeInfo.Id != nil {
		changeId = aws.ToString(out.ChangeInfo.Id)
	}

	return &DNSChangeOutput{ChangeId: changeId}, nil
}

func (c *RealRoute53Client) GetChangeStatus(ctx context.Context, changeId string) (string, error) {
	start := time.Now()
	defer observeAWSLatency("Route53GetChange", start)

	out, err := c.api.GetChange(ctx, &route53.GetChangeInput{
		Id: aws.String(changeId),
	})
	if err != nil {
		return "", classifyRoute53Error(err)
	}

	if out.ChangeInfo == nil {
		return "", fmt.Errorf("Route53 GetChange returned nil ChangeInfo for %s", changeId)
	}

	return string(out.ChangeInfo.Status), nil
}

func (c *RealRoute53Client) DeleteCNAMERecords(ctx context.Context, input *DeleteDNSRecordsInput) error {
	start := time.Now()
	defer observeAWSLatency("Route53ChangeResourceRecordSets", start)

	changes := make([]r53types.Change, len(input.Records))
	for i, rec := range input.Records {
		changes[i] = r53types.Change{
			Action: r53types.ChangeActionDelete,
			ResourceRecordSet: &r53types.ResourceRecordSet{
				Name: aws.String(rec.Name),
				Type: r53types.RRTypeCname,
				TTL:  aws.Int64(rec.TTL),
				ResourceRecords: []r53types.ResourceRecord{
					{Value: aws.String(rec.Target)},
				},
			},
		}
	}

	_, err := c.api.ChangeResourceRecordSets(ctx, &route53.ChangeResourceRecordSetsInput{
		HostedZoneId: aws.String(input.HostedZoneId),
		ChangeBatch: &r53types.ChangeBatch{
			Changes: changes,
			Comment: aws.String("Cleanup by cloudfront-tenant-operator"),
		},
	})
	if err != nil {
		classified := classifyRoute53Error(err)
		// Treat "record not found" as success for idempotent cleanup.
		if IsNotFound(classified) {
			return nil
		}
		return classified
	}

	return nil
}
