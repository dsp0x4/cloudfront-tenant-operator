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

package tenantsource

import (
	"context"
	"sync"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
)

// MockBackend is an in-memory Backend implementation for tests. Items and Err
// are read on every call under a mutex, so they may be mutated between
// reconciliations from the test body.
type MockBackend struct {
	mu sync.Mutex

	Items []TenantItem
	Err   error

	QueryCallCount int
}

// QueryTenants returns the pre-configured items or error.
func (m *MockBackend) QueryTenants(_ context.Context, _ *cloudfrontv1alpha1.TenantSource) ([]TenantItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.QueryCallCount++

	if m.Err != nil {
		return nil, m.Err
	}
	result := make([]TenantItem, len(m.Items))
	copy(result, m.Items)
	return result, nil
}
