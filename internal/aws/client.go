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

// Package aws provides the CloudFront API client interface and implementations
// for the cloudfront-tenant-operator.
package aws

import (
	"context"
	"time"
)

// CloudFrontClient defines the interface for interacting with CloudFront
// Distribution Tenant APIs. This abstraction enables unit testing with mock
// implementations and isolates the operator from AWS SDK changes.
type CloudFrontClient interface {
	// CreateDistributionTenant creates a new distribution tenant in AWS.
	CreateDistributionTenant(ctx context.Context, input *CreateDistributionTenantInput) (*DistributionTenantOutput, error)

	// GetDistributionTenant retrieves the current state of a distribution tenant.
	GetDistributionTenant(ctx context.Context, id string) (*DistributionTenantOutput, error)

	// UpdateDistributionTenant updates an existing distribution tenant.
	// The ifMatch parameter must contain the current ETag for optimistic concurrency.
	UpdateDistributionTenant(ctx context.Context, input *UpdateDistributionTenantInput) (*DistributionTenantOutput, error)

	// DeleteDistributionTenant deletes a distribution tenant.
	// The tenant must be disabled (Enabled=false) and in Deployed status before deletion.
	// The ifMatch parameter must contain the current ETag.
	DeleteDistributionTenant(ctx context.Context, id string, ifMatch string) error
}

// CreateDistributionTenantInput represents the input for creating a distribution tenant.
type CreateDistributionTenantInput struct {
	DistributionId            string
	Name                      string
	Domains                   []DomainInput
	Enabled                   *bool
	ConnectionGroupId         *string
	Parameters                []ParameterInput
	Customizations            *CustomizationsInput
	ManagedCertificateRequest *ManagedCertificateRequestInput
	Tags                      []TagInput
}

// UpdateDistributionTenantInput represents the input for updating a distribution tenant.
type UpdateDistributionTenantInput struct {
	ID                        string
	DistributionId            string
	IfMatch                   string
	Domains                   []DomainInput
	Enabled                   *bool
	ConnectionGroupId         *string
	Parameters                []ParameterInput
	Customizations            *CustomizationsInput
	ManagedCertificateRequest *ManagedCertificateRequestInput
}

// DomainInput represents a domain in the input.
type DomainInput struct {
	Domain string
}

// ParameterInput represents a key-value parameter in the input.
type ParameterInput struct {
	Name  string
	Value string
}

// CustomizationsInput represents tenant customizations in the input.
type CustomizationsInput struct {
	WebAcl          *WebAclCustomizationInput
	Certificate     *CertificateCustomizationInput
	GeoRestrictions *GeoRestrictionCustomizationInput
}

// WebAclCustomizationInput represents WAF web ACL customization in the input.
type WebAclCustomizationInput struct {
	Action string
	Arn    *string
}

// CertificateCustomizationInput represents certificate customization in the input.
type CertificateCustomizationInput struct {
	Arn string
}

// GeoRestrictionCustomizationInput represents geo restriction customization in the input.
type GeoRestrictionCustomizationInput struct {
	RestrictionType string
	Locations       []string
}

// ManagedCertificateRequestInput represents a managed certificate request in the input.
type ManagedCertificateRequestInput struct {
	ValidationTokenHost                      string
	PrimaryDomainName                        *string
	CertificateTransparencyLoggingPreference *string
}

// TagInput represents a tag in the input.
type TagInput struct {
	Key   string
	Value *string
}

// DistributionTenantOutput represents the response from AWS for a distribution tenant.
type DistributionTenantOutput struct {
	ID                string
	Arn               string
	DistributionId    string
	Name              string
	ETag              string
	Status            string
	Enabled           bool
	ConnectionGroupId string
	CreatedTime       *time.Time
	LastModifiedTime  *time.Time
	Domains           []DomainResultOutput
	Parameters        []ParameterInput
	Customizations    *CustomizationsInput
}

// DomainResultOutput represents the status of a domain as returned by AWS.
type DomainResultOutput struct {
	Domain string
	Status string
}
