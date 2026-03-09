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

// TenantSourceSpec defines the desired state of a TenantSource.
// A TenantSource points to an external database that contains tenant
// definitions. The operator periodically polls the source and creates or
// updates DistributionTenant CRs accordingly.
type TenantSourceSpec struct {
	// provider is the type of external data source.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=postgres;dynamodb
	Provider string `json:"provider"`

	// pollInterval is how often the operator polls the external source
	// for changes. Defaults to 5m.
	// +optional
	// +kubebuilder:default="5m"
	PollInterval *metav1.Duration `json:"pollInterval,omitempty"`

	// postgres contains the connection details for a PostgreSQL source.
	// Required when provider is "postgres".
	// +optional
	Postgres *PostgresSourceConfig `json:"postgres,omitempty"`

	// dynamodb contains the connection details for a DynamoDB source.
	// Required when provider is "dynamodb".
	// +optional
	DynamoDB *DynamoDBSourceConfig `json:"dynamodb,omitempty"`

	// distributionId is the default multi-tenant distribution ID to use
	// for tenants discovered from this source (can be overridden per tenant
	// in the query results).
	// +kubebuilder:validation:Required
	DistributionId string `json:"distributionId"`

	// targetNamespace is the Kubernetes namespace where DistributionTenant
	// CRs will be created. Defaults to the TenantSource's namespace.
	// +optional
	TargetNamespace *string `json:"targetNamespace,omitempty"`

	// template defines default values applied to every DistributionTenant
	// created by this source. DynamoDB items can override individual fields
	// when the corresponding attribute mapping is configured.
	// +optional
	Template *TenantTemplate `json:"template,omitempty"`

	// dryRun when true prevents the operator from creating or modifying
	// DistributionTenant CRs. Instead, it logs what would be changed and
	// updates status with a plan of pending changes. This is useful for
	// GitOps review workflows.
	// +optional
	// +kubebuilder:default=false
	DryRun *bool `json:"dryRun,omitempty"`
}

// TenantTemplate defines default values applied to every DistributionTenant
// created by a TenantSource. All fields are optional; DynamoDB items can
// override individual fields via attribute mappings.
type TenantTemplate struct {
	// enabled indicates whether tenants should serve traffic. Defaults to true.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// connectionGroupId is the default connection group for all tenants.
	// +optional
	ConnectionGroupId *string `json:"connectionGroupId,omitempty"`

	// parameters is a list of default key-value parameter values.
	// +optional
	Parameters []Parameter `json:"parameters,omitempty"`

	// customizations allows overriding distribution-level settings.
	// +optional
	Customizations *Customizations `json:"customizations,omitempty"`

	// managedCertificateRequest configures a CloudFront-managed ACM certificate
	// for tenants. If a DynamoDB item provides a certificateArn, it takes
	// precedence and the managed certificate is not used for that tenant.
	// The primaryDomainName defaults to the tenant's first domain if not set.
	// +optional
	ManagedCertificateRequest *ManagedCertificateRequest `json:"managedCertificateRequest,omitempty"`

	// tags is a list of default key-value tags for all tenants.
	// +optional
	Tags []Tag `json:"tags,omitempty"`

	// dns configures automatic DNS record management for all tenants.
	// +optional
	DNS *DNSConfig `json:"dns,omitempty"`
}

// PostgresSourceConfig defines connection details for a PostgreSQL source.
type PostgresSourceConfig struct {
	// connectionSecretRef is a reference to a Kubernetes secret containing the
	// connection string (key: "connectionString").
	// +kubebuilder:validation:Required
	ConnectionSecretRef SecretReference `json:"connectionSecretRef"`

	// query is the SQL query to execute. It must return rows with columns:
	// name (string), domain (string), and optionally: enabled (bool),
	// connection_group_id (string).
	// +kubebuilder:validation:Required
	Query string `json:"query"`
}

// DynamoDBSourceConfig defines connection details for a DynamoDB source.
// Each attribute field specifies the DynamoDB attribute name that maps to the
// corresponding DistributionTenant spec field. Only nameAttribute and
// domainsAttribute are required; all others are optional and override template
// values on a per-item basis.
type DynamoDBSourceConfig struct {
	// tableName is the DynamoDB table to scan.
	// +kubebuilder:validation:Required
	TableName string `json:"tableName"`

	// region is the AWS region for the DynamoDB table.
	// If not set, uses the operator's default region.
	// +optional
	Region *string `json:"region,omitempty"`

	// nameAttribute is the DynamoDB attribute that maps to the tenant name
	// (used as the DistributionTenant resource name). Defaults to "name".
	// +optional
	// +kubebuilder:default="name"
	NameAttribute *string `json:"nameAttribute,omitempty"`

	// domainsAttribute is the DynamoDB attribute that maps to the tenant's
	// domains. Accepts a String (S) for a single domain or a StringSet (SS)
	// for multiple domains. Defaults to "domains".
	// +optional
	// +kubebuilder:default="domains"
	DomainsAttribute *string `json:"domainsAttribute,omitempty"`

	// enabledAttribute is the DynamoDB attribute (boolean) that maps to
	// spec.enabled. If not set, uses the template value or defaults to true.
	// +optional
	EnabledAttribute *string `json:"enabledAttribute,omitempty"`

	// connectionGroupIdAttribute is the DynamoDB attribute that maps to
	// spec.connectionGroupId.
	// +optional
	ConnectionGroupIdAttribute *string `json:"connectionGroupIdAttribute,omitempty"`

	// certificateArnAttribute is the DynamoDB attribute that maps to
	// spec.customizations.certificate.arn. When present, takes precedence
	// over template.managedCertificateRequest. The ARN must refer to an ACM
	// certificate in us-east-1.
	// +optional
	CertificateArnAttribute *string `json:"certificateArnAttribute,omitempty"`

	// validationTokenHostAttribute is the DynamoDB attribute that overrides
	// template.managedCertificateRequest.validationTokenHost per tenant.
	// +optional
	ValidationTokenHostAttribute *string `json:"validationTokenHostAttribute,omitempty"`

	// primaryDomainNameAttribute is the DynamoDB attribute that overrides
	// the primary domain for the managed certificate. If not set, defaults
	// to the tenant's first domain.
	// +optional
	PrimaryDomainNameAttribute *string `json:"primaryDomainNameAttribute,omitempty"`

	// certificateTransparencyLoggingPreferenceAttribute is the DynamoDB
	// attribute that overrides the CT logging preference per tenant.
	// +optional
	CertificateTransparencyLoggingPreferenceAttribute *string `json:"certificateTransparencyLoggingPreferenceAttribute,omitempty"`

	// dnsProviderAttribute is the DynamoDB attribute that overrides
	// template.dns.provider per tenant.
	// +optional
	DNSProviderAttribute *string `json:"dnsProviderAttribute,omitempty"`

	// hostedZoneIdAttribute is the DynamoDB attribute that overrides
	// template.dns.hostedZoneId per tenant.
	// +optional
	HostedZoneIdAttribute *string `json:"hostedZoneIdAttribute,omitempty"`

	// dnsTTLAttribute is the DynamoDB attribute (number) that overrides
	// template.dns.ttl per tenant.
	// +optional
	DNSTTLAttribute *string `json:"dnsTTLAttribute,omitempty"`

	// dnsAssumeRoleArnAttribute is the DynamoDB attribute that overrides
	// template.dns.assumeRoleArn per tenant.
	// +optional
	DNSAssumeRoleArnAttribute *string `json:"dnsAssumeRoleArnAttribute,omitempty"`

	// webAclActionAttribute is the DynamoDB attribute that overrides
	// template.customizations.webAcl.action per tenant.
	// +optional
	WebAclActionAttribute *string `json:"webAclActionAttribute,omitempty"`

	// webAclArnAttribute is the DynamoDB attribute that overrides
	// template.customizations.webAcl.arn per tenant.
	// +optional
	WebAclArnAttribute *string `json:"webAclArnAttribute,omitempty"`

	// geoRestrictionTypeAttribute is the DynamoDB attribute that overrides
	// template.customizations.geoRestrictions.restrictionType per tenant.
	// +optional
	GeoRestrictionTypeAttribute *string `json:"geoRestrictionTypeAttribute,omitempty"`

	// geoLocationsAttribute is the DynamoDB attribute (StringSet) that
	// overrides template.customizations.geoRestrictions.locations per tenant.
	// +optional
	GeoLocationsAttribute *string `json:"geoLocationsAttribute,omitempty"`

	// parametersAttribute is the DynamoDB attribute (Map) that overrides
	// or merges with template.parameters per tenant. The map keys are
	// parameter names and values are parameter values.
	// +optional
	ParametersAttribute *string `json:"parametersAttribute,omitempty"`

	// tagsAttribute is the DynamoDB attribute (Map) that overrides or merges
	// with template.tags per tenant. The map keys are tag keys and values
	// are tag values.
	// +optional
	TagsAttribute *string `json:"tagsAttribute,omitempty"`
}

// SecretReference is a reference to a Kubernetes secret in the same namespace.
type SecretReference struct {
	// name is the name of the Kubernetes secret.
	// +kubebuilder:validation:Required
	Name string `json:"name"`
}

// TenantSourceStatus defines the observed state of TenantSource.
type TenantSourceStatus struct {
	// lastPollTime is the timestamp of the last successful poll.
	// +optional
	LastPollTime *metav1.Time `json:"lastPollTime,omitempty"`

	// tenantsDiscovered is the number of tenants found in the last poll.
	// +optional
	TenantsDiscovered int `json:"tenantsDiscovered"`

	// tenantsCreated is the number of DistributionTenant CRs created
	// in the last poll cycle.
	// +optional
	TenantsCreated int `json:"tenantsCreated"`

	// tenantsUpdated is the number of DistributionTenant CRs updated
	// in the last poll cycle.
	// +optional
	TenantsUpdated int `json:"tenantsUpdated"`

	// tenantsDeleted is the number of DistributionTenant CRs deleted
	// in the last poll cycle.
	// +optional
	TenantsDeleted int `json:"tenantsDeleted"`

	// pendingChanges describes what would change if dryRun were false.
	// Only populated when dryRun is true.
	// +optional
	PendingChanges []PendingChange `json:"pendingChanges,omitempty"`

	// conditions represent the latest available observations of the
	// TenantSource's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PendingChange represents a change that would be made if dryRun were false.
type PendingChange struct {
	// action is "create", "update", or "delete".
	Action string `json:"action"`

	// tenantName is the name of the DistributionTenant that would be affected.
	TenantName string `json:"tenantName"`

	// description is a human-readable description of the change.
	Description string `json:"description"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Provider",type=string,JSONPath=`.spec.provider`
// +kubebuilder:printcolumn:name="Discovered",type=integer,JSONPath=`.status.tenantsDiscovered`
// +kubebuilder:printcolumn:name="DryRun",type=boolean,JSONPath=`.spec.dryRun`
// +kubebuilder:printcolumn:name="LastPoll",type=date,JSONPath=`.status.lastPollTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// TenantSource is the Schema for the tenantsources API.
// It defines an external data source from which the operator discovers and
// manages DistributionTenant resources automatically.
type TenantSource struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of the TenantSource.
	// +required
	Spec TenantSourceSpec `json:"spec"`

	// status defines the observed state of the TenantSource.
	// +optional
	Status TenantSourceStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantSourceList contains a list of TenantSource.
type TenantSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []TenantSource `json:"items"`
}

// TenantSource condition type constants.
const (
	// TSConditionTypeReady indicates the TenantSource is polling successfully.
	TSConditionTypeReady = "Ready"
)

// TenantSource condition reason constants.
const (
	TSReasonPolling        = "Polling"
	TSReasonPollSucceeded  = "PollSucceeded"
	TSReasonPollFailed     = "PollFailed"
	TSReasonSourceError    = "SourceError"
	TSReasonConflict       = "Conflict"
	TSReasonDeleting       = "Deleting"
	TSReasonInvalidConfig  = "InvalidConfig"
	TSReasonDryRunComplete = "DryRunComplete"
)

// TenantSourceFinalizerName is the finalizer used by the TenantSource controller.
const TenantSourceFinalizerName = "cloudfront-tenant-operator.io/tenantsource-finalizer"

// TenantSourceLabelKey is the label applied to DistributionTenant CRs managed
// by a TenantSource, with the value set to the TenantSource's name.
const TenantSourceLabelKey = "cloudfront-tenant-operator.io/source"

func init() {
	SchemeBuilder.Register(&TenantSource{}, &TenantSourceList{})
}
