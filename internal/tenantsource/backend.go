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

// Package tenantsource defines the provider-neutral abstractions used by the
// TenantSource controller to read external tenant catalogs. Concrete backends
// (DynamoDB today, PostgreSQL/Mongo/Redis in the future) live in sub-packages
// and implement the Backend interface here.
package tenantsource

import (
	"context"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
)

// Backend reads the external source described by a TenantSource and returns
// the current tenant set. Implementations should wrap underlying errors with
// the sentinels defined in errors.go so the controller can classify them
// without provider-specific knowledge.
type Backend interface {
	QueryTenants(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) ([]TenantItem, error)
}

// TenantItem is the backend-neutral representation of a single tenant row
// discovered in an external source. All optional fields default to nil when
// the backend did not provide them; the controller then falls back to the
// TenantSource template.
type TenantItem struct {
	Name    string
	Domains []string

	Enabled           *bool
	ConnectionGroupId *string
	CertificateArn    *string

	ValidationTokenHost                      *string
	PrimaryDomainName                        *string
	CertificateTransparencyLoggingPreference *string

	DNSProvider      *string
	HostedZoneId     *string
	DNSTTL           *int64
	DNSAssumeRoleArn *string

	WebAclAction       *string
	WebAclArn          *string
	GeoRestrictionType *string
	GeoLocations       []string

	Parameters map[string]string
	Tags       map[string]string
}
