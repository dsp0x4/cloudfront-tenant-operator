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

package controller

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cloudfrontv1alpha1 "github.com/paolo-desantis/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/paolo-desantis/cloudfront-tenant-operator/internal/aws"
)

var _ = Describe("DistributionTenant Controller", func() {
	const (
		tenantName      = "test-tenant"
		tenantNamespace = "default"
		distributionId  = "E1XNX8R2GOAABC"
		timeout         = time.Second * 10
		interval        = time.Millisecond * 250
	)

	var (
		ctx        context.Context
		mockClient *cfaws.MockCloudFrontClient
		reconciler *DistributionTenantReconciler
		namespacedName types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockClient = cfaws.NewMockCloudFrontClient()
		reconciler = &DistributionTenantReconciler{
			Client:   k8sClient,
			Scheme:   k8sClient.Scheme(),
			CFClient: mockClient,
		}
		namespacedName = types.NamespacedName{
			Name:      tenantName,
			Namespace: tenantNamespace,
		}
	})

	AfterEach(func() {
		// Clean up the CR if it exists
		tenant := &cloudfrontv1alpha1.DistributionTenant{}
		err := k8sClient.Get(ctx, namespacedName, tenant)
		if err == nil {
			// Remove finalizer so we can delete
			tenant.Finalizers = nil
			_ = k8sClient.Update(ctx, tenant)
			_ = k8sClient.Delete(ctx, tenant)
		}
	})

	Context("When creating a new DistributionTenant", func() {
		It("should add a finalizer and create the tenant in AWS", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// First reconcile: adds finalizer
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())

			// Verify finalizer was added
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(cloudfrontv1alpha1.FinalizerName))

			// Second reconcile: creates in AWS
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))

			// Verify status was updated
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ID).NotTo(BeEmpty())
			Expect(tenant.Status.Arn).NotTo(BeEmpty())
			Expect(tenant.Status.ETag).NotTo(BeEmpty())
			Expect(tenant.Status.DistributionTenantStatus).To(Equal("InProgress"))

			// Verify AWS was called
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should handle terminal errors (CNAMEAlreadyExists) without retrying", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// First reconcile: adds finalizer
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Set up domain conflict error
			mockClient.CreateError = cfaws.ErrDomainConflict

			// Second reconcile: should handle terminal error
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred()) // Terminal errors don't return error
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// Verify condition was set
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDomainConflict))
		})
	})

	Context("When reconciling an existing DistributionTenant", func() {
		It("should detect when AWS status transitions to Deployed", func() {
			// Create tenant in K8s and AWS
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// Create in AWS
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Get the tenant to find the AWS ID
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID

			// Simulate AWS deploying the tenant
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Reconcile again: should detect Deployed status
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// Verify Ready condition is True
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDeployed))
		})

		It("should update AWS when spec changes", func() {
			// Create tenant
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Reconcile to get steady state
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Change the spec: add a new domain
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			tenant.Spec.Domains = append(tenant.Spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: "new.example.com"})
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			// Reconcile: should detect change and update
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.UpdateCallCount).To(BeNumerically(">", 0))
		})
	})

	Context("When deleting a DistributionTenant", func() {
		It("should follow the disable-before-delete flow", func() {
			// Create tenant
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			// Reconcile 1: should disable the tenant
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))

			// Verify tenant is now disabled in mock
			awsTenant := mockClient.GetTenant(awsID)
			Expect(awsTenant).NotTo(BeNil())
			Expect(awsTenant.Enabled).To(BeFalse())

			// Simulate deployment completing
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Reconcile 2: should delete the tenant
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Verify tenant was deleted from mock
			Expect(mockClient.GetTenant(awsID)).To(BeNil())
			Expect(mockClient.DeleteCallCount).To(Equal(1))
		})

		It("should remove finalizer immediately if no AWS resource exists", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer only (skip creation)
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Mark for deletion
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			// Reconcile: should just remove finalizer and K8s will GC the object
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// After finalizer removal with a deletion timestamp set, K8s garbage
			// collects the resource. Verify the object is gone (NotFound is expected).
			Eventually(func() bool {
				err := k8sClient.Get(ctx, namespacedName, tenant)
				return err != nil // Should be NotFound
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Error handling", func() {
		It("should handle ETag mismatch by requeuing immediately", func() {
			// Create tenant
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Reconcile to reach steady state
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Change spec to trigger update
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			tenant.Spec.Domains = append(tenant.Spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: "new.example.com"})
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			// Set up precondition failed
			mockClient.UpdateError = cfaws.ErrPreconditionFailed

			// Reconcile: should requeue immediately
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue())
		})

		It("should handle throttling with longer backoff", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Set up throttling error
			mockClient.CreateError = cfaws.ErrThrottling

			// Reconcile: should back off
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort * 2))
		})
	})
})

// newTestTenant creates a DistributionTenant CR for testing.
func newTestTenant(name, namespace, distributionId string) *cloudfrontv1alpha1.DistributionTenant {
	enabled := true
	return &cloudfrontv1alpha1.DistributionTenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cloudfrontv1alpha1.DistributionTenantSpec{
			DistributionId: distributionId,
			Name:           name,
			Domains: []cloudfrontv1alpha1.DomainSpec{
				{Domain: "example.com"},
			},
			Enabled: &enabled,
		},
	}
}
