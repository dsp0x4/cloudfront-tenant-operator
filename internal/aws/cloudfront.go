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
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"

	cfmetrics "github.com/dsp0x4/cloudfront-tenant-operator/internal/metrics"
)

// cloudFrontAPI defines the subset of the AWS CloudFront SDK client we use.
// This enables easier testing with a fake SDK client.
type cloudFrontAPI interface {
	CreateDistributionTenant(ctx context.Context, params *cloudfront.CreateDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateDistributionTenantOutput, error)
	GetDistributionTenant(ctx context.Context, params *cloudfront.GetDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetDistributionTenantOutput, error)
	UpdateDistributionTenant(ctx context.Context, params *cloudfront.UpdateDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.UpdateDistributionTenantOutput, error)
	DeleteDistributionTenant(ctx context.Context, params *cloudfront.DeleteDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.DeleteDistributionTenantOutput, error)
	GetDistribution(ctx context.Context, params *cloudfront.GetDistributionInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetDistributionOutput, error)
	GetManagedCertificateDetails(ctx context.Context, params *cloudfront.GetManagedCertificateDetailsInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetManagedCertificateDetailsOutput, error)
}

// RealCloudFrontClient is the production implementation of CloudFrontClient
// that delegates to the AWS SDK.
type RealCloudFrontClient struct {
	api cloudFrontAPI
}

// NewRealCloudFrontClient creates a new RealCloudFrontClient using the provided
// AWS SDK configuration.
func NewRealCloudFrontClient(cfg aws.Config) *RealCloudFrontClient {
	return &RealCloudFrontClient{
		api: cloudfront.NewFromConfig(cfg),
	}
}

func observeAWSLatency(operation string, start time.Time) {
	cfmetrics.AWSAPICallDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
}

// CreateDistributionTenant creates a new distribution tenant via the AWS API.
func (c *RealCloudFrontClient) CreateDistributionTenant(ctx context.Context, input *CreateDistributionTenantInput) (*DistributionTenantOutput, error) {
	start := time.Now()
	defer observeAWSLatency("CreateDistributionTenant", start)

	domains := make([]cftypes.DomainItem, len(input.Domains))
	for i, d := range input.Domains {
		domains[i] = cftypes.DomainItem{
			Domain: aws.String(d.Domain),
		}
	}

	awsInput := &cloudfront.CreateDistributionTenantInput{
		DistributionId: aws.String(input.DistributionId),
		Name:           aws.String(input.Name),
		Domains:        domains,
		Enabled:        input.Enabled,
	}

	if input.ConnectionGroupId != nil {
		awsInput.ConnectionGroupId = input.ConnectionGroupId
	}

	if len(input.Parameters) > 0 {
		params := make([]cftypes.Parameter, len(input.Parameters))
		for i, p := range input.Parameters {
			params[i] = cftypes.Parameter{
				Name:  aws.String(p.Name),
				Value: aws.String(p.Value),
			}
		}
		awsInput.Parameters = params
	}

	if input.Customizations != nil {
		awsInput.Customizations = toAWSCustomizations(input.Customizations)
	}

	if input.ManagedCertificateRequest != nil {
		awsInput.ManagedCertificateRequest = toAWSManagedCertificateRequest(input.ManagedCertificateRequest)
	}

	if len(input.Tags) > 0 {
		tags := make([]cftypes.Tag, len(input.Tags))
		for i, t := range input.Tags {
			tags[i] = cftypes.Tag{
				Key:   aws.String(t.Key),
				Value: t.Value,
			}
		}
		awsInput.Tags = &cftypes.Tags{Items: tags}
	}

	out, err := c.api.CreateDistributionTenant(ctx, awsInput)
	if err != nil {
		return nil, classifyAWSError(err)
	}

	return toDistributionTenantOutput(out.DistributionTenant, aws.ToString(out.ETag)), nil
}

// GetDistributionTenant retrieves a distribution tenant by ID.
func (c *RealCloudFrontClient) GetDistributionTenant(ctx context.Context, id string) (*DistributionTenantOutput, error) {
	start := time.Now()
	defer observeAWSLatency("GetDistributionTenant", start)

	out, err := c.api.GetDistributionTenant(ctx, &cloudfront.GetDistributionTenantInput{
		Identifier: aws.String(id),
	})
	if err != nil {
		return nil, classifyAWSError(err)
	}

	return toDistributionTenantOutput(out.DistributionTenant, aws.ToString(out.ETag)), nil
}

// UpdateDistributionTenant updates an existing distribution tenant.
func (c *RealCloudFrontClient) UpdateDistributionTenant(ctx context.Context, input *UpdateDistributionTenantInput) (*DistributionTenantOutput, error) {
	start := time.Now()
	defer observeAWSLatency("UpdateDistributionTenant", start)

	domains := make([]cftypes.DomainItem, len(input.Domains))
	for i, d := range input.Domains {
		domains[i] = cftypes.DomainItem{
			Domain: aws.String(d.Domain),
		}
	}

	awsInput := &cloudfront.UpdateDistributionTenantInput{
		Id:             aws.String(input.ID),
		DistributionId: aws.String(input.DistributionId),
		IfMatch:        aws.String(input.IfMatch),
		Domains:        domains,
		Enabled:        input.Enabled,
	}

	if input.ConnectionGroupId != nil {
		awsInput.ConnectionGroupId = input.ConnectionGroupId
	}

	if len(input.Parameters) > 0 {
		params := make([]cftypes.Parameter, len(input.Parameters))
		for i, p := range input.Parameters {
			params[i] = cftypes.Parameter{
				Name:  aws.String(p.Name),
				Value: aws.String(p.Value),
			}
		}
		awsInput.Parameters = params
	}

	if input.Customizations != nil {
		awsInput.Customizations = toAWSCustomizations(input.Customizations)
	}

	if input.ManagedCertificateRequest != nil {
		awsInput.ManagedCertificateRequest = toAWSManagedCertificateRequest(input.ManagedCertificateRequest)
	}

	out, err := c.api.UpdateDistributionTenant(ctx, awsInput)
	if err != nil {
		return nil, classifyAWSError(err)
	}

	return toDistributionTenantOutput(out.DistributionTenant, aws.ToString(out.ETag)), nil
}

// DeleteDistributionTenant deletes a distribution tenant by ID.
func (c *RealCloudFrontClient) DeleteDistributionTenant(ctx context.Context, id string, ifMatch string) error {
	start := time.Now()
	defer observeAWSLatency("DeleteDistributionTenant", start)

	_, err := c.api.DeleteDistributionTenant(ctx, &cloudfront.DeleteDistributionTenantInput{
		Id:      aws.String(id),
		IfMatch: aws.String(ifMatch),
	})
	return classifyAWSError(err)
}

// GetDistributionInfo retrieves configuration details about the parent distribution.
func (c *RealCloudFrontClient) GetDistributionInfo(ctx context.Context, distributionId string) (*DistributionInfo, error) {
	start := time.Now()
	defer observeAWSLatency("GetDistribution", start)

	out, err := c.api.GetDistribution(ctx, &cloudfront.GetDistributionInput{
		Id: aws.String(distributionId),
	})
	if err != nil {
		return nil, classifyAWSError(err)
	}

	if out.Distribution == nil || out.Distribution.DistributionConfig == nil {
		return nil, fmt.Errorf("distribution %s has no configuration", distributionId)
	}

	dist := out.Distribution
	cfg := dist.DistributionConfig
	info := &DistributionInfo{
		ID:         aws.ToString(dist.Id),
		DomainName: aws.ToString(dist.DomainName),
	}

	if cfg.ViewerCertificate != nil {
		vc := cfg.ViewerCertificate
		info.ViewerCertificateArn = aws.ToString(vc.ACMCertificateArn)
		info.UsesCloudFrontDefaultCert = aws.ToBool(vc.CloudFrontDefaultCertificate)
	}

	if cfg.TenantConfig != nil {
		for _, pd := range cfg.TenantConfig.ParameterDefinitions {
			pdo := ParameterDefinitionOutput{
				Name: aws.ToString(pd.Name),
			}
			if pd.Definition != nil && pd.Definition.StringSchema != nil {
				pdo.Required = aws.ToBool(pd.Definition.StringSchema.Required)
				pdo.DefaultValue = aws.ToString(pd.Definition.StringSchema.DefaultValue)
			}
			info.ParameterDefinitions = append(info.ParameterDefinitions, pdo)
		}
	}

	return info, nil
}

// GetManagedCertificateDetails retrieves managed certificate details for a tenant.
func (c *RealCloudFrontClient) GetManagedCertificateDetails(ctx context.Context, tenantIdentifier string) (*ManagedCertificateDetailsOutput, error) {
	start := time.Now()
	defer observeAWSLatency("GetManagedCertificateDetails", start)

	out, err := c.api.GetManagedCertificateDetails(ctx, &cloudfront.GetManagedCertificateDetailsInput{
		Identifier: aws.String(tenantIdentifier),
	})
	if err != nil {
		// If there's no managed cert configured, the API may return NotFound.
		if IsNotFound(classifyAWSError(err)) {
			return nil, nil
		}
		return nil, classifyAWSError(err)
	}

	if out.ManagedCertificateDetails == nil {
		return nil, nil
	}

	details := out.ManagedCertificateDetails
	result := &ManagedCertificateDetailsOutput{
		CertificateArn:      aws.ToString(details.CertificateArn),
		CertificateStatus:   string(details.CertificateStatus),
		ValidationTokenHost: string(details.ValidationTokenHost),
	}

	for _, vtd := range details.ValidationTokenDetails {
		result.ValidationTokenDetails = append(result.ValidationTokenDetails, ValidationTokenDetailOutput{
			Domain:       aws.ToString(vtd.Domain),
			RedirectFrom: aws.ToString(vtd.RedirectFrom),
			RedirectTo:   aws.ToString(vtd.RedirectTo),
		})
	}

	return result, nil
}

// toAWSCustomizations converts our domain type to the AWS SDK type.
func toAWSCustomizations(c *CustomizationsInput) *cftypes.Customizations {
	if c == nil {
		return nil
	}
	out := &cftypes.Customizations{}

	if c.WebAcl != nil {
		wac := &cftypes.WebAclCustomization{
			Action: cftypes.CustomizationActionType(c.WebAcl.Action),
		}
		if c.WebAcl.Arn != nil {
			wac.Arn = c.WebAcl.Arn
		}
		out.WebAcl = wac
	}

	if c.Certificate != nil {
		out.Certificate = &cftypes.Certificate{
			Arn: aws.String(c.Certificate.Arn),
		}
	}

	if c.GeoRestrictions != nil {
		out.GeoRestrictions = &cftypes.GeoRestrictionCustomization{
			RestrictionType: cftypes.GeoRestrictionType(c.GeoRestrictions.RestrictionType),
			Locations:       c.GeoRestrictions.Locations,
		}
	}

	return out
}

// toAWSManagedCertificateRequest converts our domain type to the AWS SDK type.
func toAWSManagedCertificateRequest(m *ManagedCertificateRequestInput) *cftypes.ManagedCertificateRequest {
	if m == nil {
		return nil
	}
	out := &cftypes.ManagedCertificateRequest{
		ValidationTokenHost: cftypes.ValidationTokenHost(m.ValidationTokenHost),
	}
	if m.PrimaryDomainName != nil {
		out.PrimaryDomainName = m.PrimaryDomainName
	}
	if m.CertificateTransparencyLoggingPreference != nil {
		out.CertificateTransparencyLoggingPreference = cftypes.CertificateTransparencyLoggingPreference(*m.CertificateTransparencyLoggingPreference)
	}
	return out
}

// toDistributionTenantOutput converts the AWS SDK response to our domain type.
func toDistributionTenantOutput(dt *cftypes.DistributionTenant, eTag string) *DistributionTenantOutput {
	if dt == nil {
		return nil
	}

	out := &DistributionTenantOutput{
		ID:                aws.ToString(dt.Id),
		Arn:               aws.ToString(dt.Arn),
		DistributionId:    aws.ToString(dt.DistributionId),
		Name:              aws.ToString(dt.Name),
		ETag:              eTag,
		Status:            aws.ToString(dt.Status),
		Enabled:           aws.ToBool(dt.Enabled),
		ConnectionGroupId: aws.ToString(dt.ConnectionGroupId),
		CreatedTime:       dt.CreatedTime,
		LastModifiedTime:  dt.LastModifiedTime,
	}

	if dt.Domains != nil {
		out.Domains = make([]DomainResultOutput, len(dt.Domains))
		for i, d := range dt.Domains {
			out.Domains[i] = DomainResultOutput{
				Domain: aws.ToString(d.Domain),
				Status: string(d.Status),
			}
		}
	}

	if dt.Parameters != nil {
		out.Parameters = make([]ParameterInput, len(dt.Parameters))
		for i, p := range dt.Parameters {
			out.Parameters[i] = ParameterInput{
				Name:  aws.ToString(p.Name),
				Value: aws.ToString(p.Value),
			}
		}
	}

	if dt.Customizations != nil {
		out.Customizations = fromAWSCustomizations(dt.Customizations)
	}

	return out
}

// fromAWSCustomizations converts the AWS SDK customizations to our domain type.
func fromAWSCustomizations(c *cftypes.Customizations) *CustomizationsInput {
	if c == nil {
		return nil
	}
	out := &CustomizationsInput{}

	if c.WebAcl != nil {
		out.WebAcl = &WebAclCustomizationInput{
			Action: string(c.WebAcl.Action),
			Arn:    c.WebAcl.Arn,
		}
	}

	if c.Certificate != nil {
		out.Certificate = &CertificateCustomizationInput{
			Arn: aws.ToString(c.Certificate.Arn),
		}
	}

	if c.GeoRestrictions != nil {
		out.GeoRestrictions = &GeoRestrictionCustomizationInput{
			RestrictionType: string(c.GeoRestrictions.RestrictionType),
			Locations:       c.GeoRestrictions.Locations,
		}
	}

	return out
}
