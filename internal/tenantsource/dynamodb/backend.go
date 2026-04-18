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

// Package dynamodb implements the tenantsource.Backend interface on top of
// the shared internal/aws DynamoDB client.
package dynamodb

import (
	"context"
	"errors"
	"fmt"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
	"github.com/dsp0x4/cloudfront-tenant-operator/internal/tenantsource"
)

// Backend reads tenants from a DynamoDB table using the attribute mappings in
// the TenantSource spec. It is a thin adapter over cfaws.DynamoDBClient that
// translates DynamoDB-specific sentinel errors into the provider-neutral ones
// in package tenantsource.
type Backend struct {
	newClient func(region string) cfaws.DynamoDBClient
}

// New constructs a Backend using the default AWS SDK config. At poll time the
// backend creates a region-specific client if spec.dynamodb.region is set,
// otherwise the SDK's default region applies.
func New(cfg awssdk.Config) *Backend {
	return &Backend{
		newClient: func(region string) cfaws.DynamoDBClient {
			if region != "" {
				return cfaws.NewRealDynamoDBClientForRegion(cfg, region)
			}
			return cfaws.NewRealDynamoDBClient(cfg)
		},
	}
}

// NewWithFactory lets tests inject an arbitrary client factory (e.g. a
// MockDynamoDBClient) without going through the AWS SDK.
func NewWithFactory(newClient func(region string) cfaws.DynamoDBClient) *Backend {
	return &Backend{newClient: newClient}
}

// QueryTenants implements tenantsource.Backend.
func (b *Backend) QueryTenants(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) ([]tenantsource.TenantItem, error) {
	if source.Spec.DynamoDB == nil {
		return nil, fmt.Errorf("%w: spec.dynamodb is required when provider is 'dynamodb'", tenantsource.ErrSourceInvalidConfig)
	}
	region := ""
	if source.Spec.DynamoDB.Region != nil {
		region = *source.Spec.DynamoDB.Region
	}
	items, err := b.newClient(region).ScanTenants(ctx, buildScanInput(source.Spec.DynamoDB))
	if err != nil {
		return nil, translateErr(err)
	}
	return items, nil
}

// translateErr maps cfaws sentinel errors to provider-neutral tenantsource
// sentinels while preserving the original message chain so users still see the
// specific AWS detail in status conditions.
func translateErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, cfaws.ErrDynamoDBTableNotFound):
		return fmt.Errorf("%w: %s", tenantsource.ErrSourceNotFound, err.Error())
	case errors.Is(err, cfaws.ErrAccessDenied):
		return fmt.Errorf("%w: %s", tenantsource.ErrSourceAccessDenied, err.Error())
	case errors.Is(err, cfaws.ErrThrottling):
		return fmt.Errorf("%w: %s", tenantsource.ErrSourceThrottled, err.Error())
	}
	return err
}

// buildScanInput converts a DynamoDBSourceConfig into the ScanTenantsInput
// consumed by cfaws.DynamoDBClient. Empty optional mappings are left empty so
// the scan skips them entirely.
func buildScanInput(cfg *cloudfrontv1alpha1.DynamoDBSourceConfig) *cfaws.ScanTenantsInput {
	input := &cfaws.ScanTenantsInput{
		TableName:        cfg.TableName,
		NameAttribute:    "name",
		DomainsAttribute: "domains",
	}

	if cfg.NameAttribute != nil {
		input.NameAttribute = *cfg.NameAttribute
	}
	if cfg.DomainsAttribute != nil {
		input.DomainsAttribute = *cfg.DomainsAttribute
	}
	if cfg.EnabledAttribute != nil {
		input.EnabledAttribute = *cfg.EnabledAttribute
	}
	if cfg.ConnectionGroupIdAttribute != nil {
		input.ConnectionGroupIdAttribute = *cfg.ConnectionGroupIdAttribute
	}
	if cfg.CertificateArnAttribute != nil {
		input.CertificateArnAttribute = *cfg.CertificateArnAttribute
	}
	if cfg.ValidationTokenHostAttribute != nil {
		input.ValidationTokenHostAttribute = *cfg.ValidationTokenHostAttribute
	}
	if cfg.PrimaryDomainNameAttribute != nil {
		input.PrimaryDomainNameAttribute = *cfg.PrimaryDomainNameAttribute
	}
	if cfg.CertificateTransparencyLoggingPreferenceAttribute != nil {
		input.CertificateTransparencyLoggingPreferenceAttribute = *cfg.CertificateTransparencyLoggingPreferenceAttribute
	}
	if cfg.DNSProviderAttribute != nil {
		input.DNSProviderAttribute = *cfg.DNSProviderAttribute
	}
	if cfg.HostedZoneIdAttribute != nil {
		input.HostedZoneIdAttribute = *cfg.HostedZoneIdAttribute
	}
	if cfg.DNSTTLAttribute != nil {
		input.DNSTTLAttribute = *cfg.DNSTTLAttribute
	}
	if cfg.DNSAssumeRoleArnAttribute != nil {
		input.DNSAssumeRoleArnAttribute = *cfg.DNSAssumeRoleArnAttribute
	}
	if cfg.WebAclActionAttribute != nil {
		input.WebAclActionAttribute = *cfg.WebAclActionAttribute
	}
	if cfg.WebAclArnAttribute != nil {
		input.WebAclArnAttribute = *cfg.WebAclArnAttribute
	}
	if cfg.GeoRestrictionTypeAttribute != nil {
		input.GeoRestrictionTypeAttribute = *cfg.GeoRestrictionTypeAttribute
	}
	if cfg.GeoLocationsAttribute != nil {
		input.GeoLocationsAttribute = *cfg.GeoLocationsAttribute
	}
	if cfg.ParametersAttribute != nil {
		input.ParametersAttribute = *cfg.ParametersAttribute
	}
	if cfg.TagsAttribute != nil {
		input.TagsAttribute = *cfg.TagsAttribute
	}

	return input
}
