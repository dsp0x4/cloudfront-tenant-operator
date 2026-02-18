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

// Package metrics provides Prometheus metric definitions for the
// cloudfront-tenant-operator.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// ReconcileErrors tracks the total number of reconciliation errors by
	// AWS error classification. Per-resource errors are available via status
	// conditions and Kubernetes events; this counter provides an aggregate
	// view for alerting and dashboards.
	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "reconcile_errors_total",
			Help:      "Total number of reconciliation errors by AWS error classification.",
		},
		[]string{"error_type"},
	)

	// DriftDetectedCount tracks the total number of drift detections across
	// all tenants. Per-resource drift is available via the Synced condition
	// and status.driftDetected field; this counter provides an aggregate
	// signal for alerting.
	DriftDetectedCount = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "drift_detected_total",
			Help:      "Total number of external drift detections across all tenants.",
		},
	)

	// AWSAPICallDuration tracks the duration of AWS API calls.
	AWSAPICallDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "aws_api_call_duration_seconds",
			Help:      "Duration of AWS API calls in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"operation"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		ReconcileErrors,
		DriftDetectedCount,
		AWSAPICallDuration,
	)
}
