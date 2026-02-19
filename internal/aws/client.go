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

	// GetDistributionInfo retrieves configuration details about the parent
	// multi-tenant distribution, including its default certificate and
	// parameter definitions. Used for spec validation before creating or
	// updating a tenant.
	GetDistributionInfo(ctx context.Context, distributionId string) (*DistributionInfo, error)

	// GetManagedCertificateDetails retrieves the managed certificate details
	// for a distribution tenant. Returns nil details (without error) if no
	// managed certificate is configured for the tenant.
	GetManagedCertificateDetails(ctx context.Context, tenantIdentifier string) (*ManagedCertificateDetailsOutput, error)

	// GetConnectionGroupRoutingEndpoint retrieves the routing endpoint for a
	// CloudFront connection group. Used to determine the CNAME target when
	// the tenant uses a custom connection group.
	GetConnectionGroupRoutingEndpoint(ctx context.Context, connectionGroupId string) (string, error)
}

// DNSClient defines the interface for managing DNS records. Currently
// implemented by Route53 only (provider="route53").
type DNSClient interface {
	// UpsertCNAMERecords creates or updates CNAME records in the hosted zone.
	// Returns the change ID that can be polled via GetChangeStatus.
	UpsertCNAMERecords(ctx context.Context, input *UpsertDNSRecordsInput) (*DNSChangeOutput, error)

	// GetChangeStatus returns the propagation status of a Route53 change.
	// Returns "PENDING" or "INSYNC".
	GetChangeStatus(ctx context.Context, changeId string) (string, error)

	// DeleteCNAMERecords removes CNAME records from the hosted zone.
	// Silently succeeds if the records do not exist.
	DeleteCNAMERecords(ctx context.Context, input *DeleteDNSRecordsInput) error
}

// ACMClient defines the interface for retrieving ACM certificate details.
// Used to validate that a certificate's SANs cover the tenant's domains
// before creating DNS records.
type ACMClient interface {
	// GetCertificateSANs retrieves the Subject Alternative Names for a certificate.
	GetCertificateSANs(ctx context.Context, certificateArn string) ([]string, error)
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

// DistributionInfo contains configuration details about a multi-tenant
// distribution, used for validating tenant specs before creation or update.
type DistributionInfo struct {
	// ID is the distribution identifier.
	ID string

	// DomainName is the CloudFront domain name (e.g. d111111abcdef8.cloudfront.net).
	DomainName string

	// ViewerCertificateArn is the ARN of the ACM certificate attached to the
	// distribution. Empty if the distribution uses the CloudFront default certificate.
	ViewerCertificateArn string

	// UsesCloudFrontDefaultCert is true when the distribution uses the
	// CloudFront default certificate (*.cloudfront.net) rather than a custom
	// ACM certificate.
	UsesCloudFrontDefaultCert bool

	// ParameterDefinitions lists the template parameters defined on the
	// multi-tenant distribution, including whether they are required and any
	// default values.
	ParameterDefinitions []ParameterDefinitionOutput
}

// ParameterDefinitionOutput represents a parameter definition from the
// multi-tenant distribution template.
type ParameterDefinitionOutput struct {
	// Name is the parameter name.
	Name string

	// Required indicates whether the parameter must be supplied by each tenant.
	Required bool

	// DefaultValue is the default value for the parameter. Empty when Required
	// is true and no default is configured.
	DefaultValue string
}

// ManagedCertificateDetailsOutput contains the status of a CloudFront-managed
// ACM certificate for a distribution tenant.
type ManagedCertificateDetailsOutput struct {
	// CertificateArn is the ARN of the managed ACM certificate. May be empty
	// while the certificate is still being provisioned.
	CertificateArn string

	// CertificateStatus is the certificate lifecycle status:
	// "pending-validation", "issued", "inactive", "expired",
	// "validation-timed-out", "revoked", "failed".
	CertificateStatus string

	// ValidationTokenHost is how the validation token is served ("cloudfront"
	// or "self-hosted").
	ValidationTokenHost string

	// ValidationTokenDetails contains per-domain validation token information
	// (redirect URLs for HTTP validation).
	ValidationTokenDetails []ValidationTokenDetailOutput
}

// ValidationTokenDetailOutput contains domain-level validation token information.
type ValidationTokenDetailOutput struct {
	Domain       string
	RedirectFrom string
	RedirectTo   string
}

// UpsertDNSRecordsInput represents the input for creating or updating DNS records.
type UpsertDNSRecordsInput struct {
	HostedZoneId string
	Records      []DNSRecord
}

// DNSRecord represents a single CNAME DNS record.
type DNSRecord struct {
	Name   string
	Target string
	TTL    int64
}

// DNSChangeOutput represents the result of a DNS record change.
type DNSChangeOutput struct {
	ChangeId string
}

// DeleteDNSRecordsInput represents the input for deleting DNS records.
type DeleteDNSRecordsInput struct {
	HostedZoneId string
	Records      []DNSRecord
}
