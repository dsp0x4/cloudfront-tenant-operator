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
	"sync"
)

// MockDynamoDBClient implements DynamoDBClient for testing.
type MockDynamoDBClient struct {
	mu sync.Mutex

	Items []TenantItem
	Err   error

	ScanCallCount int
}

// ScanTenants returns the pre-configured items or error.
func (m *MockDynamoDBClient) ScanTenants(_ context.Context, _ *ScanTenantsInput) ([]TenantItem, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ScanCallCount++

	if m.Err != nil {
		return nil, m.Err
	}
	result := make([]TenantItem, len(m.Items))
	copy(result, m.Items)
	return result, nil
}
