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
	// ReconcileDuration tracks the time taken for each reconciliation loop.
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of reconciliation loops in seconds.",
			Buckets:   prometheus.DefBuckets,
		},
		[]string{"namespace", "name", "result"},
	)

	// ReconcileErrors tracks the total number of reconciliation errors by type.
	ReconcileErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "reconcile_errors_total",
			Help:      "Total number of reconciliation errors by error type.",
		},
		[]string{"namespace", "name", "error_type"},
	)

	// TenantCount tracks the total number of managed tenants by status.
	TenantCount = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "tenants_total",
			Help:      "Total number of managed distribution tenants by status.",
		},
		[]string{"namespace", "status"},
	)

	// DriftDetectedCount tracks the total number of drift detections.
	DriftDetectedCount = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudfront_tenant_operator",
			Name:      "drift_detected_total",
			Help:      "Total number of drift detections.",
		},
		[]string{"namespace", "name"},
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
		ReconcileDuration,
		ReconcileErrors,
		TenantCount,
		DriftDetectedCount,
		AWSAPICallDuration,
	)
}
