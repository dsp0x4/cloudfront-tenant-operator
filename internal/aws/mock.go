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
	"fmt"
	"sync"
	"time"
)

// MockCloudFrontClient is a test double for the CloudFrontClient interface.
// It stores tenants in-memory and supports configurable error injection.
type MockCloudFrontClient struct {
	mu      sync.Mutex
	tenants map[string]*DistributionTenantOutput
	nextID  int

	// CreateError, if set, is returned by CreateDistributionTenant.
	CreateError error
	// GetError, if set, is returned by GetDistributionTenant.
	GetError error
	// UpdateError, if set, is returned by UpdateDistributionTenant.
	UpdateError error
	// DeleteError, if set, is returned by DeleteDistributionTenant.
	DeleteError error
	// GetDistributionInfoError, if set, is returned by GetDistributionInfo.
	GetDistributionInfoError error
	// GetManagedCertError, if set, is returned by GetManagedCertificateDetails.
	GetManagedCertError error

	// CreateCallCount tracks the number of calls to CreateDistributionTenant.
	CreateCallCount int
	// GetCallCount tracks the number of calls to GetDistributionTenant.
	GetCallCount int
	// UpdateCallCount tracks the number of calls to UpdateDistributionTenant.
	UpdateCallCount int
	// DeleteCallCount tracks the number of calls to DeleteDistributionTenant.
	DeleteCallCount int
	// GetDistributionInfoCallCount tracks calls to GetDistributionInfo.
	GetDistributionInfoCallCount int
	// GetManagedCertCallCount tracks calls to GetManagedCertificateDetails.
	GetManagedCertCallCount int

	// DistributionInfos stores mock distribution info keyed by distribution ID.
	DistributionInfos map[string]*DistributionInfo

	// ManagedCertDetails stores mock managed cert details keyed by tenant ID.
	ManagedCertDetails map[string]*ManagedCertificateDetailsOutput
}

// NewMockCloudFrontClient creates a new MockCloudFrontClient.
func NewMockCloudFrontClient() *MockCloudFrontClient {
	return &MockCloudFrontClient{
		tenants:            make(map[string]*DistributionTenantOutput),
		DistributionInfos:  make(map[string]*DistributionInfo),
		ManagedCertDetails: make(map[string]*ManagedCertificateDetailsOutput),
	}
}

// CreateDistributionTenant creates a mock distribution tenant.
func (m *MockCloudFrontClient) CreateDistributionTenant(_ context.Context, input *CreateDistributionTenantInput) (*DistributionTenantOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.CreateCallCount++

	if m.CreateError != nil {
		return nil, m.CreateError
	}

	m.nextID++
	id := fmt.Sprintf("dt_mock_%d", m.nextID)
	eTag := fmt.Sprintf("ETAG_%d", m.nextID)
	now := time.Now()

	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	domains := make([]DomainResultOutput, len(input.Domains))
	for i, d := range input.Domains {
		domains[i] = DomainResultOutput{
			Domain: d.Domain,
			Status: "active",
		}
	}

	out := &DistributionTenantOutput{
		ID:                id,
		Arn:               fmt.Sprintf("arn:aws:cloudfront::123456789012:distribution-tenant/%s", id),
		DistributionId:    input.DistributionId,
		Name:              input.Name,
		ETag:              eTag,
		Status:            "InProgress",
		Enabled:           enabled,
		ConnectionGroupId: derefString(input.ConnectionGroupId),
		CreatedTime:       &now,
		LastModifiedTime:  &now,
		Domains:           domains,
		Parameters:        input.Parameters,
		Customizations:    input.Customizations,
	}

	m.tenants[id] = out
	return out, nil
}

// GetDistributionTenant retrieves a mock distribution tenant.
func (m *MockCloudFrontClient) GetDistributionTenant(_ context.Context, id string) (*DistributionTenantOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetCallCount++

	if m.GetError != nil {
		return nil, m.GetError
	}

	tenant, ok := m.tenants[id]
	if !ok {
		return nil, ErrNotFound
	}

	return tenant, nil
}

// UpdateDistributionTenant updates a mock distribution tenant.
func (m *MockCloudFrontClient) UpdateDistributionTenant(_ context.Context, input *UpdateDistributionTenantInput) (*DistributionTenantOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.UpdateCallCount++

	if m.UpdateError != nil {
		return nil, m.UpdateError
	}

	tenant, ok := m.tenants[input.ID]
	if !ok {
		return nil, ErrNotFound
	}

	if tenant.ETag != input.IfMatch {
		return nil, ErrPreconditionFailed
	}

	now := time.Now()
	newETag := fmt.Sprintf("ETAG_updated_%d", now.UnixNano())

	domains := make([]DomainResultOutput, len(input.Domains))
	for i, d := range input.Domains {
		domains[i] = DomainResultOutput{
			Domain: d.Domain,
			Status: "active",
		}
	}

	enabled := tenant.Enabled
	if input.Enabled != nil {
		enabled = *input.Enabled
	}

	tenant.ETag = newETag
	tenant.Status = "InProgress"
	tenant.Enabled = enabled
	tenant.DistributionId = input.DistributionId
	tenant.Domains = domains
	tenant.Parameters = input.Parameters
	tenant.Customizations = input.Customizations
	tenant.ConnectionGroupId = derefString(input.ConnectionGroupId)
	tenant.LastModifiedTime = &now

	return tenant, nil
}

// DeleteDistributionTenant deletes a mock distribution tenant.
func (m *MockCloudFrontClient) DeleteDistributionTenant(_ context.Context, id string, ifMatch string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.DeleteCallCount++

	if m.DeleteError != nil {
		return m.DeleteError
	}

	tenant, ok := m.tenants[id]
	if !ok {
		return ErrNotFound
	}

	if tenant.ETag != ifMatch {
		return ErrPreconditionFailed
	}

	if tenant.Enabled {
		return ErrResourceNotDisabled
	}

	delete(m.tenants, id)
	return nil
}

// GetDistributionInfo returns mock distribution info.
func (m *MockCloudFrontClient) GetDistributionInfo(_ context.Context, distributionId string) (*DistributionInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetDistributionInfoCallCount++

	if m.GetDistributionInfoError != nil {
		return nil, m.GetDistributionInfoError
	}

	info, ok := m.DistributionInfos[distributionId]
	if !ok {
		return nil, ErrNotFound
	}

	return info, nil
}

// GetManagedCertificateDetails returns mock managed certificate details.
func (m *MockCloudFrontClient) GetManagedCertificateDetails(_ context.Context, tenantIdentifier string) (*ManagedCertificateDetailsOutput, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.GetManagedCertCallCount++

	if m.GetManagedCertError != nil {
		return nil, m.GetManagedCertError
	}

	details := m.ManagedCertDetails[tenantIdentifier]
	return details, nil
}

// SetTenantStatus allows tests to set the status of a mock tenant
// (e.g., transitioning from InProgress to Deployed).
func (m *MockCloudFrontClient) SetTenantStatus(id string, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if tenant, ok := m.tenants[id]; ok {
		tenant.Status = status
	}
}

// GetTenant returns the mock tenant for assertions, or nil if not found.
func (m *MockCloudFrontClient) GetTenant(id string) *DistributionTenantOutput {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tenants[id]
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
