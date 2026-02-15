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
	"strings"
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
		name          string
		input         error
		expectedIs    error  // for errors.Is checks (sentinel matching)
		containsMsg   string // the original AWS message should be preserved
		exactPassthru bool   // if true, the error should pass through unchanged
	}{
		{
			name:       "nil error returns nil",
			input:      nil,
			expectedIs: nil,
		},
		{
			name:        "CNAMEAlreadyExists maps to DomainConflict and preserves message",
			input:       &mockAPIError{code: "CNAMEAlreadyExists", message: "The CNAME example.com is already in use by distribution E1234"},
			expectedIs:  ErrDomainConflict,
			containsMsg: "The CNAME example.com is already in use by distribution E1234",
		},
		{
			name:        "AccessDenied maps to AccessDenied and preserves message",
			input:       &mockAPIError{code: "AccessDenied", message: "User arn:aws:iam::123:user/foo is not authorized"},
			expectedIs:  ErrAccessDenied,
			containsMsg: "User arn:aws:iam::123:user/foo is not authorized",
		},
		{
			name:        "InvalidArgument maps to InvalidArgument and preserves message",
			input:       &mockAPIError{code: "InvalidArgument", message: "The parameter Domains is missing required value"},
			expectedIs:  ErrInvalidArgument,
			containsMsg: "The parameter Domains is missing required value",
		},
		{
			name:        "EntityNotFound maps to NotFound and preserves message",
			input:       &mockAPIError{code: "EntityNotFound", message: "Distribution tenant dt-123 not found"},
			expectedIs:  ErrNotFound,
			containsMsg: "Distribution tenant dt-123 not found",
		},
		{
			name:        "ResourceNotDisabled maps correctly and preserves message",
			input:       &mockAPIError{code: "ResourceNotDisabled", message: "The resource must be disabled first"},
			expectedIs:  ErrResourceNotDisabled,
			containsMsg: "The resource must be disabled first",
		},
		{
			name:        "PreconditionFailed maps correctly and preserves message",
			input:       &mockAPIError{code: "PreconditionFailed", message: "The If-Match version is stale"},
			expectedIs:  ErrPreconditionFailed,
			containsMsg: "The If-Match version is stale",
		},
		{
			name:        "InvalidIfMatchVersion maps to PreconditionFailed",
			input:       &mockAPIError{code: "InvalidIfMatchVersion", message: "ETag mismatch"},
			expectedIs:  ErrPreconditionFailed,
			containsMsg: "ETag mismatch",
		},
		{
			name:        "Throttling maps correctly and preserves message",
			input:       &mockAPIError{code: "Throttling", message: "Rate exceeded for API"},
			expectedIs:  ErrThrottling,
			containsMsg: "Rate exceeded for API",
		},
		{
			name:          "unknown API error passes through",
			input:         &mockAPIError{code: "UnknownError", message: "something else"},
			exactPassthru: true,
		},
		{
			name:          "non-API error passes through",
			input:         errors.New("network error"),
			exactPassthru: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := classifyAWSError(tt.input)
			if tt.expectedIs == nil && !tt.exactPassthru {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}
			if result == nil {
				t.Errorf("expected non-nil error, got nil")
				return
			}
			if tt.exactPassthru {
				if result != tt.input {
					t.Errorf("expected passthrough error %v, got %v", tt.input, result)
				}
				return
			}
			// Check sentinel classification with errors.Is
			if !errors.Is(result, tt.expectedIs) {
				t.Errorf("errors.Is(%v, %v) = false, want true", result, tt.expectedIs)
			}
			// Check that original AWS message is preserved
			if tt.containsMsg != "" {
				if !strings.Contains(result.Error(), tt.containsMsg) {
					t.Errorf("error %q should contain original message %q", result.Error(), tt.containsMsg)
				}
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
		{"wrapped DomainConflict is terminal", classifyAWSError(&mockAPIError{code: "CNAMEAlreadyExists", message: "conflict"}), true},
		{"wrapped AccessDenied is terminal", classifyAWSError(&mockAPIError{code: "AccessDenied", message: "denied"}), true},
		{"wrapped InvalidArgument is terminal", classifyAWSError(&mockAPIError{code: "InvalidArgument", message: "bad"}), true},
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
