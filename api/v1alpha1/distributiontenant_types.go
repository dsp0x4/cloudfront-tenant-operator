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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DistributionTenantSpec defines the desired state of a CloudFront Distribution Tenant.
// It mirrors the AWS CreateDistributionTenant / UpdateDistributionTenant API.
type DistributionTenantSpec struct {
	// distributionId is the ID of the multi-tenant distribution to use.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	DistributionId string `json:"distributionId"`

	// domains is the list of domains associated with the distribution tenant.
	// At least one domain is required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Domains []DomainSpec `json:"domains"`

	// enabled indicates whether the distribution tenant should serve traffic.
	// Defaults to true.
	// +optional
	// +kubebuilder:default=true
	Enabled *bool `json:"enabled,omitempty"`

	// connectionGroupId is the ID of the connection group to associate with
	// the distribution tenant. If not specified, CloudFront uses the default
	// connection group.
	// +optional
	ConnectionGroupId *string `json:"connectionGroupId,omitempty"`

	// parameters is a list of key-value parameter values to add to the resource.
	// A valid parameter value must exist for any parameter that is marked as
	// required in the multi-tenant distribution.
	// +optional
	Parameters []Parameter `json:"parameters,omitempty"`

	// customizations allows overriding specific settings from the parent
	// multi-tenant distribution, including geo restrictions, WAF web ACL, and
	// ACM certificate.
	// +optional
	Customizations *Customizations `json:"customizations,omitempty"`

	// managedCertificateRequest configures a CloudFront-managed ACM certificate.
	// +optional
	ManagedCertificateRequest *ManagedCertificateRequest `json:"managedCertificateRequest,omitempty"`

	// tags is a list of key-value tags to associate with the distribution tenant.
	// +optional
	Tags []Tag `json:"tags,omitempty"`

	// dns configures automatic DNS record management for the tenant's domains.
	// When configured, the operator will create/update/delete CNAME records
	// pointing to the CloudFront distribution or connection group endpoint.
	// DNS records are created before the tenant and deleted after it.
	// +optional
	DNS *DNSConfig `json:"dns,omitempty"`
}

// DNSConfig configures automatic DNS record management.
// +kubebuilder:validation:XValidation:rule="self.provider != 'route53' || has(self.hostedZoneId)",message="hostedZoneId is required when provider is 'route53'"
type DNSConfig struct {
	// provider is the DNS provider to use for record management.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=route53
	Provider string `json:"provider"`

	// hostedZoneId is the Route53 hosted zone ID where records will be managed.
	// Required when provider is "route53".
	// +optional
	HostedZoneId *string `json:"hostedZoneId,omitempty"`

	// ttl is the Time-To-Live for DNS CNAME records in seconds.
	// Must be between 60 and 172800.
	// Defaults to 300 seconds.
	// +optional
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=172800
	// +kubebuilder:default=300
	TTL *int64 `json:"ttl,omitempty"`

	// assumeRoleArn is the ARN of an IAM role to assume when making Route53
	// API calls. Use this when the hosted zone is in a different AWS account
	// than the operator. Only affects Route53 operations (not CloudFront).
	// +optional
	AssumeRoleArn *string `json:"assumeRoleArn,omitempty"`
}

// DomainSpec represents a domain to associate with the distribution tenant.
type DomainSpec struct {
	// domain is the fully qualified domain name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Domain string `json:"domain"`
}

// Parameter is a key-value pair passed to the multi-tenant distribution template.
type Parameter struct {
	// name is the parameter name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9\-_]+$`
	Name string `json:"name"`

	// value is the parameter value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Value string `json:"value"`
}

// Customizations allows overriding settings inherited from the parent multi-tenant distribution.
type Customizations struct {
	// webAcl configures the WAF web ACL for this tenant.
	// +optional
	WebAcl *WebAclCustomization `json:"webAcl,omitempty"`

	// certificate configures an ACM certificate override for this tenant.
	// +optional
	Certificate *CertificateCustomization `json:"certificate,omitempty"`

	// geoRestrictions configures geographic restrictions for this tenant.
	// +optional
	GeoRestrictions *GeoRestrictionCustomization `json:"geoRestrictions,omitempty"`
}

// WebAclCustomization defines the WAF web ACL customization.
// +kubebuilder:validation:XValidation:rule="self.action != 'override' || has(self.arn)",message="arn is required when action is 'override'"
type WebAclCustomization struct {
	// action specifies what to do with the WAF web ACL.
	// "override" uses a separate WAF web ACL for this tenant.
	// "disable" removes WAF protection entirely.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=disable;override
	Action string `json:"action"`

	// arn is the ARN of the WAF web ACL. Required when action is "override".
	// +optional
	Arn *string `json:"arn,omitempty"`
}

// CertificateCustomization defines an ACM certificate override.
// +kubebuilder:validation:XValidation:rule="self.arn.startsWith('arn:aws:acm:us-east-1:')",message="ACM certificates used with CloudFront must be in the us-east-1 region"
type CertificateCustomization struct {
	// arn is the ARN of the ACM certificate to use for this tenant.
	// +kubebuilder:validation:Required
	Arn string `json:"arn"`
}

// GeoRestrictionCustomization defines geographic restrictions for the tenant.
type GeoRestrictionCustomization struct {
	// restrictionType is the method for restricting content distribution by country.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=none;whitelist;blacklist
	RestrictionType string `json:"restrictionType"`

	// locations is the list of ISO 3166-1 alpha-2 country codes.
	// +optional
	Locations []string `json:"locations,omitempty"`
}

// ManagedCertificateRequest configures CloudFront-managed ACM certificate issuance.
type ManagedCertificateRequest struct {
	// validationTokenHost specifies how the HTTP validation token is served.
	// "cloudfront" means CloudFront serves the token automatically (requires
	// DNS CNAME to be pointing to CloudFront before the request).
	// "self-hosted" means you serve the validation token from your own infrastructure.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=cloudfront;self-hosted
	ValidationTokenHost string `json:"validationTokenHost"`

	// primaryDomainName is the primary domain for the managed certificate.
	// Must be one of the domains listed in spec.domains.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	PrimaryDomainName string `json:"primaryDomainName"`

	// certificateTransparencyLoggingPreference controls CT logging.
	// +optional
	// +kubebuilder:validation:Enum=enabled;disabled
	CertificateTransparencyLoggingPreference *string `json:"certificateTransparencyLoggingPreference,omitempty"`
}

// Tag is a key-value pair for resource tagging.
type Tag struct {
	// key is the tag key.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Key string `json:"key"`

	// value is the tag value.
	// +optional
	// +kubebuilder:validation:MaxLength=256
	Value *string `json:"value,omitempty"`
}

// DistributionTenantStatus defines the observed state of DistributionTenant.
type DistributionTenantStatus struct {
	// observedGeneration is the most recent generation of the spec that the
	// controller has successfully reconciled. Used for three-way diff: if
	// metadata.generation != observedGeneration, the spec was modified since
	// the last successful reconciliation. Combined with a spec-vs-AWS
	// comparison, this distinguishes user-initiated changes from external drift.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// id is the AWS-assigned ID of the distribution tenant.
	// +optional
	ID string `json:"id,omitempty"`

	// arn is the Amazon Resource Name of the distribution tenant.
	// +optional
	Arn string `json:"arn,omitempty"`

	// eTag is the current version identifier used for optimistic concurrency
	// control on updates and deletes.
	// +optional
	ETag string `json:"eTag,omitempty"`

	// distributionTenantStatus is the AWS-reported deployment status
	// (e.g., "InProgress", "Deployed").
	// +optional
	DistributionTenantStatus string `json:"distributionTenantStatus,omitempty"`

	// createdTime is the timestamp when the distribution tenant was created in AWS.
	// +optional
	CreatedTime *metav1.Time `json:"createdTime,omitempty"`

	// lastModifiedTime is the timestamp when the distribution tenant was last
	// modified in AWS.
	// +optional
	LastModifiedTime *metav1.Time `json:"lastModifiedTime,omitempty"`

	// domainResults reflects the status of each domain as reported by AWS.
	// +optional
	DomainResults []DomainResult `json:"domainResults,omitempty"`

	// certificateArn is the ARN of the ACM certificate associated with this
	// tenant, either from a custom certificate customization or a managed
	// certificate issued by CloudFront.
	// +optional
	CertificateArn string `json:"certificateArn,omitempty"`

	// managedCertificateStatus is the lifecycle status of the CloudFront-managed
	// ACM certificate: "pending-validation", "issued", "inactive", "expired",
	// "validation-timed-out", "revoked", "failed".
	// Only set when a managedCertificateRequest is configured in the spec.
	// +optional
	ManagedCertificateStatus string `json:"managedCertificateStatus,omitempty"`

	// lastDriftCheckTime is the timestamp of the last drift detection check.
	// +optional
	LastDriftCheckTime *metav1.Time `json:"lastDriftCheckTime,omitempty"`

	// driftDetected indicates whether the AWS state differs from the spec
	// due to an external change.
	// +optional
	DriftDetected bool `json:"driftDetected,omitempty"`

	// dnsChangeId is the Route53 change ID for a pending DNS record change.
	// Set after upserting records and cleared once the change reaches INSYNC.
	// Used to track propagation across reconcile cycles.
	// +optional
	DNSChangeId string `json:"dnsChangeId,omitempty"`

	// dnsTarget is the CNAME target (CloudFront endpoint) used for DNS records.
	// Stored in status so deletion can clean up records even if the spec changes.
	// +optional
	DNSTarget string `json:"dnsTarget,omitempty"`

	// conditions represent the latest available observations of the
	// DistributionTenant's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// DomainResult represents the status of a domain in AWS.
type DomainResult struct {
	// domain is the fully qualified domain name.
	Domain string `json:"domain"`

	// status is the AWS-reported domain status ("active" or "inactive").
	// +optional
	Status string `json:"status,omitempty"`
}

// Condition type constants for DistributionTenant.
const (
	// ConditionTypeReady indicates the tenant is fully deployed and serving traffic.
	ConditionTypeReady = "Ready"

	// ConditionTypeSynced indicates the K8s spec matches the AWS state.
	ConditionTypeSynced = "Synced"

	// ConditionTypeCertificateReady indicates whether the managed certificate
	// has been validated and is ready for use.
	ConditionTypeCertificateReady = "CertificateReady"

	// ConditionTypeDNSReady indicates whether DNS records have been created
	// and propagated in Route53.
	ConditionTypeDNSReady = "DNSReady"
)

// Condition reason constants.
const (
	ReasonDeployed           = "Deployed"
	ReasonDeploying          = "Deploying"
	ReasonCreating           = "Creating"
	ReasonDeleting           = "Deleting"
	ReasonDisabling          = "Disabling"
	ReasonInSync             = "InSync"
	ReasonDriftDetected      = "DriftDetected"
	ReasonUpdatePending      = "UpdatePending"
	ReasonDomainConflict     = "DomainConflict"
	ReasonAccessDenied       = "AccessDenied"
	ReasonInvalidSpec        = "InvalidSpec"
	ReasonAWSError           = "AWSError"
	ReasonCertValidated      = "Validated"
	ReasonCertAttaching      = "Attaching"
	ReasonCertPending        = "PendingValidation"
	ReasonCertNotConfigured  = "NotConfigured"
	ReasonCertFailed         = "CertificateFailed"
	ReasonValidationFailed   = "ValidationFailed"
	ReasonMissingParameters  = "MissingParameters"
	ReasonMissingCertificate = "MissingCertificate"

	ReasonDNSRecordCreating       = "DNSRecordCreating"
	ReasonDNSPropagating          = "DNSPropagating"
	ReasonDNSReady                = "DNSReady"
	ReasonDNSError                = "DNSError"
	ReasonDNSNotConfigured        = "DNSNotConfigured"
	ReasonCertSANMismatch         = "CertificateSANMismatch"
	ReasonDomainValidationPending = "DomainValidationPending"
)

// FinalizerName is the finalizer used by this operator.
const FinalizerName = "cloudfront-tenant-operator.io/finalizer"

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Status",type=string,JSONPath=`.status.distributionTenantStatus`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.status.id`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// DistributionTenant is the Schema for the distributiontenants API.
// It represents a CloudFront Distribution Tenant managed by the operator.
type DistributionTenant struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the DistributionTenant.
	// +required
	Spec DistributionTenantSpec `json:"spec"`

	// status defines the observed state of the DistributionTenant.
	// +optional
	Status DistributionTenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// DistributionTenantList contains a list of DistributionTenant.
type DistributionTenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DistributionTenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DistributionTenant{}, &DistributionTenantList{})
}
