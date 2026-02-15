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

package controller

import (
	"fmt"

	cloudfrontv1alpha1 "github.com/paolo-desantis/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/paolo-desantis/cloudfront-tenant-operator/internal/aws"
)

// DriftPolicy controls how the operator responds when external drift is detected
// (AWS state was modified outside the operator while the K8s spec remained unchanged).
type DriftPolicy string

const (
	// DriftPolicyEnforce overwrites the AWS state with the K8s spec.
	// The spec is treated as the single source of truth.
	DriftPolicyEnforce DriftPolicy = "enforce"

	// DriftPolicyReport logs the drift and sets status conditions, but does
	// not modify the AWS state.
	DriftPolicyReport DriftPolicy = "report"

	// DriftPolicySuspend skips drift detection entirely. Useful during
	// planned maintenance windows when AWS resources are modified manually.
	DriftPolicySuspend DriftPolicy = "suspend"
)

// ValidateDriftPolicy returns an error if the given policy string is not valid.
func ValidateDriftPolicy(p string) error {
	switch DriftPolicy(p) {
	case DriftPolicyEnforce, DriftPolicyReport, DriftPolicySuspend:
		return nil
	default:
		return fmt.Errorf("invalid drift policy %q: must be one of enforce, report, suspend", p)
	}
}

// changeType represents the result of a three-way diff between the K8s spec,
// the last-reconciled state (tracked via observedGeneration), and the current
// AWS state.
type changeType int

const (
	// changeNone means the spec matches AWS and no action is needed.
	changeNone changeType = iota

	// changeSpec means the user modified the spec since the last successful
	// reconciliation. The operator should push the spec to AWS.
	changeSpec

	// changeDrift means the spec has NOT changed since the last successful
	// reconciliation, but the AWS state differs. This indicates an external
	// modification (console, CLI, another tool).
	changeDrift
)

func (c changeType) String() string {
	switch c {
	case changeNone:
		return "none"
	case changeSpec:
		return "spec_changed"
	case changeDrift:
		return "drift"
	default:
		return "unknown"
	}
}

// detectChanges performs a three-way diff using the observedGeneration pattern.
//
// The logic:
//
//	metadata.generation: incremented by K8s on every spec change.
//	status.observedGeneration: set by the operator after a successful create/update.
//
//	generation != observedGeneration → spec was modified since last reconcile.
//	generation == observedGeneration → spec is unchanged.
//
// Combined with a spec-vs-AWS comparison:
//
//	spec changed  + spec != AWS → changeSpec  (user intent, push to AWS)
//	spec changed  + spec == AWS → changeNone  (no-op, just update observedGeneration)
//	spec same     + spec != AWS → changeDrift (external modification)
//	spec same     + spec == AWS → changeNone  (steady state)
func detectChanges(tenant *cloudfrontv1alpha1.DistributionTenant, awsTenant *cfaws.DistributionTenantOutput) changeType {
	specChanged := tenant.Generation != tenant.Status.ObservedGeneration
	awsMatchesSpec := specMatchesAWS(&tenant.Spec, awsTenant)

	switch {
	case specChanged && !awsMatchesSpec:
		return changeSpec
	case !specChanged && !awsMatchesSpec:
		return changeDrift
	default:
		return changeNone
	}
}

// specMatchesAWS compares the CRD spec against the current AWS state.
// It covers all fields that the operator sends to the AWS API and that
// AWS returns in GetDistributionTenant. Fields that are spec-only
// (e.g., ManagedCertificateRequest, Tags, DNS) are excluded because
// AWS does not return them in the tenant response.
func specMatchesAWS(spec *cloudfrontv1alpha1.DistributionTenantSpec, aws *cfaws.DistributionTenantOutput) bool {
	// Enabled
	specEnabled := true
	if spec.Enabled != nil {
		specEnabled = *spec.Enabled
	}
	if specEnabled != aws.Enabled {
		return false
	}

	// Distribution ID
	if spec.DistributionId != aws.DistributionId {
		return false
	}

	// Connection group ID
	specCGID := ""
	if spec.ConnectionGroupId != nil {
		specCGID = *spec.ConnectionGroupId
	}
	if specCGID != aws.ConnectionGroupId {
		return false
	}

	// Domains (order-independent)
	if len(spec.Domains) != len(aws.Domains) {
		return false
	}
	specDomains := make(map[string]bool, len(spec.Domains))
	for _, d := range spec.Domains {
		specDomains[d.Domain] = true
	}
	for _, d := range aws.Domains {
		if !specDomains[d.Domain] {
			return false
		}
	}

	// Parameters (order-independent)
	if len(spec.Parameters) != len(aws.Parameters) {
		return false
	}
	if len(spec.Parameters) > 0 {
		specParams := make(map[string]string, len(spec.Parameters))
		for _, p := range spec.Parameters {
			specParams[p.Name] = p.Value
		}
		for _, p := range aws.Parameters {
			if specParams[p.Name] != p.Value {
				return false
			}
		}
	}

	// Customizations
	if !customizationsMatch(spec.Customizations, aws.Customizations) {
		return false
	}

	return true
}

// customizationsMatch compares spec-level customizations against the AWS state.
func customizationsMatch(spec *cloudfrontv1alpha1.Customizations, aws *cfaws.CustomizationsInput) bool {
	if spec == nil && aws == nil {
		return true
	}
	if spec == nil || aws == nil {
		return false
	}

	// WebACL
	if (spec.WebAcl == nil) != (aws.WebAcl == nil) {
		return false
	}
	if spec.WebAcl != nil {
		if spec.WebAcl.Action != aws.WebAcl.Action {
			return false
		}
		specArn := ""
		if spec.WebAcl.Arn != nil {
			specArn = *spec.WebAcl.Arn
		}
		awsArn := ""
		if aws.WebAcl.Arn != nil {
			awsArn = *aws.WebAcl.Arn
		}
		if specArn != awsArn {
			return false
		}
	}

	// Certificate
	if (spec.Certificate == nil) != (aws.Certificate == nil) {
		return false
	}
	if spec.Certificate != nil {
		if spec.Certificate.Arn != aws.Certificate.Arn {
			return false
		}
	}

	// Geo restrictions
	if (spec.GeoRestrictions == nil) != (aws.GeoRestrictions == nil) {
		return false
	}
	if spec.GeoRestrictions != nil {
		if spec.GeoRestrictions.RestrictionType != aws.GeoRestrictions.RestrictionType {
			return false
		}
		if len(spec.GeoRestrictions.Locations) != len(aws.GeoRestrictions.Locations) {
			return false
		}
		specLocs := make(map[string]bool, len(spec.GeoRestrictions.Locations))
		for _, l := range spec.GeoRestrictions.Locations {
			specLocs[l] = true
		}
		for _, l := range aws.GeoRestrictions.Locations {
			if !specLocs[l] {
				return false
			}
		}
	}

	return true
}
