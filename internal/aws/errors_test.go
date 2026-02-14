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
	"errors"
	"testing"

	smithy "github.com/aws/smithy-go"
)

// mockAPIError implements smithy.APIError for testing.
type mockAPIError struct {
	code    string
	message string
}

func (e *mockAPIError) Error() string                 { return e.message }
func (e *mockAPIError) ErrorCode() string             { return e.code }
func (e *mockAPIError) ErrorMessage() string          { return e.message }
func (e *mockAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestClassifyAWSError(t *testing.T) {
	tests := []struct {
		name     string
		input    error
		expected error
	}{
		{
			name:     "nil error returns nil",
			input:    nil,
			expected: nil,
		},
		{
			name:     "CNAMEAlreadyExists maps to DomainConflict",
			input:    &mockAPIError{code: "CNAMEAlreadyExists", message: "domain conflict"},
			expected: ErrDomainConflict,
		},
		{
			name:     "AccessDenied maps to AccessDenied",
			input:    &mockAPIError{code: "AccessDenied", message: "forbidden"},
			expected: ErrAccessDenied,
		},
		{
			name:     "InvalidArgument maps to InvalidArgument",
			input:    &mockAPIError{code: "InvalidArgument", message: "bad input"},
			expected: ErrInvalidArgument,
		},
		{
			name:     "EntityNotFound maps to NotFound",
			input:    &mockAPIError{code: "EntityNotFound", message: "not found"},
			expected: ErrNotFound,
		},
		{
			name:     "ResourceNotDisabled maps correctly",
			input:    &mockAPIError{code: "ResourceNotDisabled", message: "still enabled"},
			expected: ErrResourceNotDisabled,
		},
		{
			name:     "PreconditionFailed maps correctly",
			input:    &mockAPIError{code: "PreconditionFailed", message: "etag mismatch"},
			expected: ErrPreconditionFailed,
		},
		{
			name:     "InvalidIfMatchVersion maps to PreconditionFailed",
			input:    &mockAPIError{code: "InvalidIfMatchVersion", message: "etag mismatch"},
			expected: ErrPreconditionFailed,
		},
		{
			name:     "Throttling maps correctly",
			input:    &mockAPIError{code: "Throttling", message: "rate limited"},
			expected: ErrThrottling,
		},
		{
			name:     "unknown API error passes through",
			input:    &mockAPIError{code: "UnknownError", message: "something else"},
			expected: &mockAPIError{code: "UnknownError", message: "something else"},
		},
		{
			name:     "non-API error passes through",
			input:    errors.New("network error"),
			expected: errors.New("network error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyAWSError(tt.input)
			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if result == nil {
				t.Errorf("expected %v, got nil", tt.expected)
				return
			}
			if result.Error() != tt.expected.Error() {
				t.Errorf("expected error %q, got %q", tt.expected.Error(), result.Error())
			}
		})
	}
}

func TestIsTerminalError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"DomainConflict is terminal", ErrDomainConflict, true},
		{"AccessDenied is terminal", ErrAccessDenied, true},
		{"InvalidArgument is terminal", ErrInvalidArgument, true},
		{"NotFound is not terminal", ErrNotFound, false},
		{"PreconditionFailed is not terminal", ErrPreconditionFailed, false},
		{"Throttling is not terminal", ErrThrottling, false},
		{"generic error is not terminal", errors.New("random"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTerminalError(tt.err); got != tt.expected {
				t.Errorf("IsTerminalError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

func TestIsHelpers(t *testing.T) {
	if !IsNotFound(ErrNotFound) {
		t.Error("IsNotFound should return true for ErrNotFound")
	}
	if !IsResourceNotDisabled(ErrResourceNotDisabled) {
		t.Error("IsResourceNotDisabled should return true for ErrResourceNotDisabled")
	}
	if !IsPreconditionFailed(ErrPreconditionFailed) {
		t.Error("IsPreconditionFailed should return true for ErrPreconditionFailed")
	}
	if !IsThrottling(ErrThrottling) {
		t.Error("IsThrottling should return true for ErrThrottling")
	}
}
