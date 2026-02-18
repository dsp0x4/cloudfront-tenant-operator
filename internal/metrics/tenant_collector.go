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

package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
)

var tenantsTotalDesc = prometheus.NewDesc(
	"cloudfront_tenant_operator_tenants_total",
	"Current number of DistributionTenant resources by readiness status, computed from the informer cache at Prometheus scrape time.",
	[]string{"namespace", "status"},
	nil,
)

// TenantCollector implements prometheus.Collector following the kube-state-metrics
// pattern: instead of eagerly setting a gauge during reconciliation, Collect()
// enumerates resources from the informer cache on demand when Prometheus scrapes.
// This avoids per-reconcile overhead and guarantees the metric is never stale.
type TenantCollector struct {
	reader client.Reader
}

// NewTenantCollector creates a TenantCollector that reads from the given
// client.Reader (typically the manager's cached client).
func NewTenantCollector(reader client.Reader) *TenantCollector {
	return &TenantCollector{reader: reader}
}

func (c *TenantCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- tenantsTotalDesc
}

func (c *TenantCollector) Collect(ch chan<- prometheus.Metric) {
	var tenantList cloudfrontv1alpha1.DistributionTenantList
	if err := c.reader.List(context.Background(), &tenantList); err != nil {
		return
	}

	type nsStatus struct{ namespace, status string }
	counts := make(map[nsStatus]float64)

	for i := range tenantList.Items {
		t := &tenantList.Items[i]
		status := "NotReady"
		cond := meta.FindStatusCondition(t.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
		if cond != nil && cond.Status == metav1.ConditionTrue {
			status = "Ready"
		}
		counts[nsStatus{t.Namespace, status}]++
	}

	for k, v := range counts {
		ch <- prometheus.MustNewConstMetric(tenantsTotalDesc, prometheus.GaugeValue, v, k.namespace, k.status)
	}
}
