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

package dynamodb

import (
	"context"
	"errors"
	"fmt"
	"testing"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
	"github.com/dsp0x4/cloudfront-tenant-operator/internal/tenantsource"
)

func TestTranslateErr(t *testing.T) {
	tests := []struct {
		name    string
		in      error
		wantErr error
	}{
		{"nil passes through", nil, nil},
		{"table not found maps to source not found",
			fmt.Errorf("%w: no such table", cfaws.ErrDynamoDBTableNotFound),
			tenantsource.ErrSourceNotFound},
		{"access denied maps to source access denied",
			fmt.Errorf("%w: denied", cfaws.ErrAccessDenied),
			tenantsource.ErrSourceAccessDenied},
		{"throttling maps to source throttled",
			fmt.Errorf("%w: slow down", cfaws.ErrThrottling),
			tenantsource.ErrSourceThrottled},
		{"unknown error passes through unchanged",
			errors.New("boom"),
			nil}, // sentinel check below: errors.Is returns false for all known sentinels
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := translateErr(tc.in)

			if tc.in == nil {
				if got != nil {
					t.Fatalf("expected nil, got %v", got)
				}
				return
			}
			if tc.wantErr != nil && !errors.Is(got, tc.wantErr) {
				t.Fatalf("expected errors.Is(got, %v) to be true, got %v", tc.wantErr, got)
			}
			// Ensure the original message is preserved so users see the
			// underlying AWS detail in status conditions.
			if got == nil || got.Error() == "" {
				t.Fatalf("expected non-empty wrapped error, got %v", got)
			}
		})
	}
}

func TestQueryTenants_MissingDynamoDBSpec(t *testing.T) {
	b := NewWithFactory(func(string) cfaws.DynamoDBClient {
		t.Fatal("client factory must not be called when spec.dynamodb is nil")
		return nil
	})

	source := &cloudfrontv1alpha1.TenantSource{}
	_, err := b.QueryTenants(context.Background(), source)
	if !errors.Is(err, tenantsource.ErrSourceInvalidConfig) {
		t.Fatalf("expected ErrSourceInvalidConfig, got %v", err)
	}
}

func TestQueryTenants_PassesRegionToFactory(t *testing.T) {
	region := "eu-west-1"
	var gotRegion string
	b := NewWithFactory(func(r string) cfaws.DynamoDBClient {
		gotRegion = r
		return &cfaws.MockDynamoDBClient{}
	})

	source := &cloudfrontv1alpha1.TenantSource{
		Spec: cloudfrontv1alpha1.TenantSourceSpec{
			Provider: "dynamodb",
			DynamoDB: &cloudfrontv1alpha1.DynamoDBSourceConfig{
				TableName: "t",
				Region:    &region,
			},
		},
	}
	if _, err := b.QueryTenants(context.Background(), source); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotRegion != region {
		t.Fatalf("factory got region %q, want %q", gotRegion, region)
	}
}

func TestBuildScanInput_DefaultsAndOverrides(t *testing.T) {
	// Defaults apply when only TableName is set.
	cfg := &cloudfrontv1alpha1.DynamoDBSourceConfig{TableName: "tenants"}
	in := buildScanInput(cfg)
	if in.TableName != "tenants" {
		t.Errorf("TableName: got %q, want %q", in.TableName, "tenants")
	}
	if in.NameAttribute != "name" {
		t.Errorf("NameAttribute default: got %q, want %q", in.NameAttribute, "name")
	}
	if in.DomainsAttribute != "domains" {
		t.Errorf("DomainsAttribute default: got %q, want %q", in.DomainsAttribute, "domains")
	}
	// Optional attributes default to empty so the mapper skips them.
	if in.EnabledAttribute != "" {
		t.Errorf("EnabledAttribute default: got %q, want empty", in.EnabledAttribute)
	}

	// Overrides flow through.
	nameAttr := "tenantName"
	domainsAttr := "hosts"
	enabledAttr := "active"
	certAttr := "certArn"
	paramsAttr := "params"
	cfg = &cloudfrontv1alpha1.DynamoDBSourceConfig{
		TableName:               "t",
		NameAttribute:           &nameAttr,
		DomainsAttribute:        &domainsAttr,
		EnabledAttribute:        &enabledAttr,
		CertificateArnAttribute: &certAttr,
		ParametersAttribute:     &paramsAttr,
	}
	in = buildScanInput(cfg)
	if in.NameAttribute != nameAttr {
		t.Errorf("NameAttribute override: got %q, want %q", in.NameAttribute, nameAttr)
	}
	if in.DomainsAttribute != domainsAttr {
		t.Errorf("DomainsAttribute override: got %q, want %q", in.DomainsAttribute, domainsAttr)
	}
	if in.EnabledAttribute != enabledAttr {
		t.Errorf("EnabledAttribute override: got %q, want %q", in.EnabledAttribute, enabledAttr)
	}
	if in.CertificateArnAttribute != certAttr {
		t.Errorf("CertificateArnAttribute override: got %q, want %q", in.CertificateArnAttribute, certAttr)
	}
	if in.ParametersAttribute != paramsAttr {
		t.Errorf("ParametersAttribute override: got %q, want %q", in.ParametersAttribute, paramsAttr)
	}
}
