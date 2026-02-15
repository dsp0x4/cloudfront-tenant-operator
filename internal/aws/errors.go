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
	"fmt"
	"strings"

	"github.com/aws/smithy-go"
)

// Terminal error sentinel values. These errors indicate conditions that cannot
// be resolved by retrying and require user intervention.
var (
	// ErrDomainConflict indicates a domain (CNAME) is already associated
	// with another CloudFront distribution or tenant.
	ErrDomainConflict = errors.New("domain already associated with another distribution or tenant (CNAMEAlreadyExists)")

	// ErrAccessDenied indicates the caller does not have permission to
	// perform the requested operation.
	ErrAccessDenied = errors.New("access denied: check IAM permissions")

	// ErrInvalidArgument indicates the request contains invalid parameters.
	ErrInvalidArgument = errors.New("invalid argument in request")

	// ErrNotFound indicates the distribution tenant does not exist in AWS.
	ErrNotFound = errors.New("distribution tenant not found")

	// ErrResourceNotDisabled indicates a delete was attempted on an enabled tenant.
	ErrResourceNotDisabled = errors.New("resource must be disabled before deletion")

	// ErrPreconditionFailed indicates the ETag does not match (stale state).
	ErrPreconditionFailed = errors.New("precondition failed: ETag mismatch")

	// ErrThrottling indicates the API rate limit was exceeded.
	ErrThrottling = errors.New("API rate limit exceeded")
)

// IsTerminalError returns true if the error is a terminal error that should
// not be retried. The operator should set an error condition and wait for
// the user to fix the issue.
func IsTerminalError(err error) bool {
	return errors.Is(err, ErrDomainConflict) ||
		errors.Is(err, ErrAccessDenied) ||
		errors.Is(err, ErrInvalidArgument)
}

// IsNotFound returns true if the error indicates the resource was not found.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

// IsResourceNotDisabled returns true if the error indicates the resource
// must be disabled before the operation can proceed.
func IsResourceNotDisabled(err error) bool {
	return errors.Is(err, ErrResourceNotDisabled)
}

// IsPreconditionFailed returns true if the error is due to an ETag mismatch.
func IsPreconditionFailed(err error) bool {
	return errors.Is(err, ErrPreconditionFailed)
}

// IsThrottling returns true if the error is due to API rate limiting.
func IsThrottling(err error) bool {
	return errors.Is(err, ErrThrottling)
}

// classifyAWSError maps AWS SDK API errors to our domain error types.
// The original AWS error message is preserved by wrapping the sentinel error,
// so callers can use errors.Is() for classification while users see the
// specific details from the AWS API response.
func classifyAWSError(err error) error {
	if err == nil {
		return nil
	}

	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		msg := apiErr.ErrorMessage()
		switch {
		case code == "CNAMEAlreadyExists" || strings.Contains(code, "CNAMEAlreadyExists"):
			return fmt.Errorf("%w: %s", ErrDomainConflict, msg)
		case code == "AccessDenied":
			return fmt.Errorf("%w: %s", ErrAccessDenied, msg)
		case code == "InvalidArgument":
			return fmt.Errorf("%w: %s", ErrInvalidArgument, msg)
		case code == "EntityNotFound" || code == "NoSuchDistributionTenant":
			return fmt.Errorf("%w: %s", ErrNotFound, msg)
		case code == "ResourceNotDisabled":
			return fmt.Errorf("%w: %s", ErrResourceNotDisabled, msg)
		case code == "PreconditionFailed" || code == "InvalidIfMatchVersion":
			return fmt.Errorf("%w: %s", ErrPreconditionFailed, msg)
		case code == "Throttling" || code == "TooManyRequests":
			return fmt.Errorf("%w: %s", ErrThrottling, msg)
		}
	}

	return err
}
