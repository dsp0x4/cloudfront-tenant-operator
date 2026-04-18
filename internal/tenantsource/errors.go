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

import "errors"

// Provider-neutral sentinel errors that Backend implementations should wrap
// their concrete SDK errors in. The controller classifies these without
// knowing which backend produced them.
var (
	// ErrSourceNotFound indicates the configured resource (table, database,
	// collection, ...) does not exist. Terminal; requeue with long backoff.
	ErrSourceNotFound = errors.New("tenant source not found")

	// ErrSourceAccessDenied indicates the operator lacks permission to read
	// from the source. Terminal; requeue with long backoff.
	ErrSourceAccessDenied = errors.New("tenant source access denied")

	// ErrSourceThrottled indicates the source is rate-limiting requests.
	// Transient; requeue with short backoff.
	ErrSourceThrottled = errors.New("tenant source throttled")

	// ErrSourceInvalidConfig indicates the TenantSource spec is missing or
	// malformed for the selected provider. Terminal; requeue with long backoff.
	ErrSourceInvalidConfig = errors.New("tenant source config invalid")
)
