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

	// dryRun when true prevents the operator from creating or modifying
	// DistributionTenant CRs. Instead, it logs what would be changed and
	// updates status with a plan of pending changes. This is useful for
	// GitOps review workflows.
	// +optional
	// +kubebuilder:default=false
	DryRun *bool `json:"dryRun,omitempty"`
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
type DynamoDBSourceConfig struct {
	// tableName is the DynamoDB table to scan or query.
	// +kubebuilder:validation:Required
	TableName string `json:"tableName"`

	// region is the AWS region for the DynamoDB table.
	// +optional
	Region *string `json:"region,omitempty"`

	// nameAttribute is the attribute name in DynamoDB items that maps to
	// the tenant name. Defaults to "name".
	// +optional
	// +kubebuilder:default="name"
	NameAttribute *string `json:"nameAttribute,omitempty"`

	// domainAttribute is the attribute name in DynamoDB items that maps to
	// the tenant domain. Defaults to "domain".
	// +optional
	// +kubebuilder:default="domain"
	DomainAttribute *string `json:"domainAttribute,omitempty"`
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
	TenantsDiscovered int `json:"tenantsDiscovered,omitempty"`

	// tenantsCreated is the number of DistributionTenant CRs created
	// in the last poll cycle.
	// +optional
	TenantsCreated int `json:"tenantsCreated,omitempty"`

	// tenantsUpdated is the number of DistributionTenant CRs updated
	// in the last poll cycle.
	// +optional
	TenantsUpdated int `json:"tenantsUpdated,omitempty"`

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

func init() {
	SchemeBuilder.Register(&TenantSource{}, &TenantSourceList{})
}
