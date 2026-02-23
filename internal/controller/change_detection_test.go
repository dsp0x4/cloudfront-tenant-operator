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
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
)

func boolPtr(b bool) *bool    { return &b }
func strPtr(s string) *string { return &s }

func TestValidateDriftPolicy(t *testing.T) {
	valid := []string{"enforce", "report", "suspend"}
	for _, p := range valid {
		if err := ValidateDriftPolicy(p); err != nil {
			t.Errorf("ValidateDriftPolicy(%q) returned error: %v", p, err)
		}
	}

	invalid := []string{"", "auto", "ENFORCE", "Report"}
	for _, p := range invalid {
		if err := ValidateDriftPolicy(p); err == nil {
			t.Errorf("ValidateDriftPolicy(%q) expected error, got nil", p)
		}
	}
}

func TestDetectChanges(t *testing.T) {
	baseTenant := func(gen, obsGen int64) *cloudfrontv1alpha1.DistributionTenant {
		enabled := true
		return &cloudfrontv1alpha1.DistributionTenant{
			ObjectMeta: metav1.ObjectMeta{Generation: gen},
			Spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "example.com"}},
				Enabled:        &enabled,
			},
			Status: cloudfrontv1alpha1.DistributionTenantStatus{
				ObservedGeneration: obsGen,
			},
		}
	}

	matchingAWS := &cfaws.DistributionTenantOutput{
		DistributionId: "E123",
		Domains:        []cfaws.DomainResultOutput{{Domain: "example.com", Status: "active"}},
		Enabled:        true,
	}

	tests := []struct {
		name     string
		tenant   *cloudfrontv1alpha1.DistributionTenant
		aws      *cfaws.DistributionTenantOutput
		expected changeType
	}{
		{
			name:     "steady state: generation matches, spec matches AWS",
			tenant:   baseTenant(2, 2),
			aws:      matchingAWS,
			expected: changeNone,
		},
		{
			name: "spec changed: generation bumped, spec differs from AWS",
			tenant: func() *cloudfrontv1alpha1.DistributionTenant {
				t := baseTenant(3, 2) // generation > observedGeneration
				t.Spec.Domains = append(t.Spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: "new.example.com"})
				return t
			}(),
			aws:      matchingAWS,
			expected: changeSpec,
		},
		{
			name:     "spec changed but matches AWS (no-op): generation bumped, spec still matches AWS",
			tenant:   baseTenant(3, 2), // generation > observedGeneration, but spec matches AWS
			aws:      matchingAWS,
			expected: changeNone, // no actual change needed
		},
		{
			name:   "drift: generation matches, but AWS differs from spec",
			tenant: baseTenant(2, 2),
			aws: &cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains: []cfaws.DomainResultOutput{
					{Domain: "example.com", Status: "active"},
					{Domain: "rogue.example.com", Status: "active"}, // external addition
				},
				Enabled: true,
			},
			expected: changeDrift,
		},
		{
			name:   "drift: AWS enabled state changed externally",
			tenant: baseTenant(2, 2),
			aws: &cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "example.com", Status: "active"}},
				Enabled:        false, // was disabled externally
			},
			expected: changeDrift,
		},
		{
			name:   "first reconcile (observedGeneration=0): treated as spec change",
			tenant: baseTenant(1, 0), // never reconciled
			aws: &cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "other.com", Status: "active"}},
				Enabled:        true,
			},
			expected: changeSpec,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectChanges(tt.tenant, tt.aws)
			if got != tt.expected {
				t.Errorf("detectChanges() = %v (%s), want %v (%s)",
					got, got.String(), tt.expected, tt.expected.String())
			}
		})
	}
}

func TestSpecMatchesAWS(t *testing.T) {
	tests := []struct {
		name     string
		spec     cloudfrontv1alpha1.DistributionTenantSpec
		aws      cfaws.DistributionTenantOutput
		expected bool
	}{
		{
			name: "identical minimal spec",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
			},
			expected: true,
		},
		{
			name: "domains in different order still match",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains: []cloudfrontv1alpha1.DomainSpec{
					{Domain: "b.com"}, {Domain: "a.com"},
				},
				Enabled: boolPtr(true),
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains: []cfaws.DomainResultOutput{
					{Domain: "a.com"}, {Domain: "b.com"},
				},
				Enabled: true,
			},
			expected: true,
		},
		{
			name: "different domain count",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains: []cfaws.DomainResultOutput{
					{Domain: "a.com"}, {Domain: "b.com"},
				},
				Enabled: true,
			},
			expected: false,
		},
		{
			name: "enabled state differs",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        false,
			},
			expected: false,
		},
		{
			name: "nil enabled defaults to true",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				// Enabled is nil → defaults to true
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
			},
			expected: true,
		},
		{
			name: "parameters match",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				Parameters: []cloudfrontv1alpha1.Parameter{
					{Name: "origin", Value: "api.example.com"},
					{Name: "ttl", Value: "300"},
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				Parameters: []cfaws.ParameterInput{
					{Name: "ttl", Value: "300"},
					{Name: "origin", Value: "api.example.com"},
				},
			},
			expected: true,
		},
		{
			name: "parameter value differs",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				Parameters:     []cloudfrontv1alpha1.Parameter{{Name: "ttl", Value: "300"}},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				Parameters:     []cfaws.ParameterInput{{Name: "ttl", Value: "600"}},
			},
			expected: false,
		},
		{
			name: "connection group ID differs",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId:    "E123",
				Domains:           []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:           boolPtr(true),
				ConnectionGroupId: strPtr("cg-123"),
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId:    "E123",
				Domains:           []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:           true,
				ConnectionGroupId: "cg-456",
			},
			expected: false,
		},
		{
			name: "customizations: certificate matches",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				Customizations: &cloudfrontv1alpha1.Customizations{
					Certificate: &cloudfrontv1alpha1.CertificateCustomization{
						Arn: "arn:aws:acm:us-east-1:123:certificate/abc",
					},
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				Customizations: &cfaws.CustomizationsInput{
					Certificate: &cfaws.CertificateCustomizationInput{
						Arn: "arn:aws:acm:us-east-1:123:certificate/abc",
					},
				},
			},
			expected: true,
		},
		{
			name: "customizations: spec has them, AWS doesn't",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				Customizations: &cloudfrontv1alpha1.Customizations{
					WebAcl: &cloudfrontv1alpha1.WebAclCustomization{Action: "disable"},
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				// No customizations
			},
			expected: false,
		},
		{
			name: "customizations: geo restriction locations in different order match",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				Customizations: &cloudfrontv1alpha1.Customizations{
					GeoRestrictions: &cloudfrontv1alpha1.GeoRestrictionCustomization{
						RestrictionType: "whitelist",
						Locations:       []string{"US", "CA", "GB"},
					},
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				Customizations: &cfaws.CustomizationsInput{
					GeoRestrictions: &cfaws.GeoRestrictionCustomizationInput{
						RestrictionType: "whitelist",
						Locations:       []string{"GB", "US", "CA"},
					},
				},
			},
			expected: true,
		},
		{
			name: "managed cert: spec has cert ARN persisted, AWS matches",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
					ValidationTokenHost: "cloudfront",
					PrimaryDomainName:   "a.com",
				},
				Customizations: &cloudfrontv1alpha1.Customizations{
					Certificate: &cloudfrontv1alpha1.CertificateCustomization{
						Arn: "arn:aws:acm:us-east-1:123:certificate/managed",
					},
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
				Customizations: &cfaws.CustomizationsInput{
					Certificate: &cfaws.CertificateCustomizationInput{
						Arn: "arn:aws:acm:us-east-1:123:certificate/managed",
					},
				},
			},
			expected: true,
		},
		{
			name: "managed cert: spec has no cert yet, AWS has no cert either (pre-attach)",
			spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: "E123",
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "a.com"}},
				Enabled:        boolPtr(true),
				ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
					ValidationTokenHost: "cloudfront",
					PrimaryDomainName:   "a.com",
				},
			},
			aws: cfaws.DistributionTenantOutput{
				DistributionId: "E123",
				Domains:        []cfaws.DomainResultOutput{{Domain: "a.com"}},
				Enabled:        true,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := specMatchesAWS(&tt.spec, &tt.aws)
			if got != tt.expected {
				t.Errorf("specMatchesAWS() = %v, want %v", got, tt.expected)
			}
		})
	}
}
