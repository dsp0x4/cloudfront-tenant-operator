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

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudfront"
	cftypes "github.com/aws/aws-sdk-go-v2/service/cloudfront/types"
)

// cloudFrontAPI defines the subset of the AWS CloudFront SDK client we use.
// This enables easier testing with a fake SDK client.
type cloudFrontAPI interface {
	CreateDistributionTenant(ctx context.Context, params *cloudfront.CreateDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.CreateDistributionTenantOutput, error)
	GetDistributionTenant(ctx context.Context, params *cloudfront.GetDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.GetDistributionTenantOutput, error)
	UpdateDistributionTenant(ctx context.Context, params *cloudfront.UpdateDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.UpdateDistributionTenantOutput, error)
	DeleteDistributionTenant(ctx context.Context, params *cloudfront.DeleteDistributionTenantInput, optFns ...func(*cloudfront.Options)) (*cloudfront.DeleteDistributionTenantOutput, error)
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

// CreateDistributionTenant creates a new distribution tenant via the AWS API.
func (c *RealCloudFrontClient) CreateDistributionTenant(ctx context.Context, input *CreateDistributionTenantInput) (*DistributionTenantOutput, error) {
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
	_, err := c.api.DeleteDistributionTenant(ctx, &cloudfront.DeleteDistributionTenantInput{
		Id:      aws.String(id),
		IfMatch: aws.String(ifMatch),
	})
	return classifyAWSError(err)
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
