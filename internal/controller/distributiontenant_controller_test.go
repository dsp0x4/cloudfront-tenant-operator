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
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
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
		ctx            context.Context
		mockClient     *cfaws.MockCloudFrontClient
		mockDNSClient  *cfaws.MockDNSClient
		mockACMClient  *cfaws.MockACMClient
		reconciler     *DistributionTenantReconciler
		namespacedName types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockClient = cfaws.NewMockCloudFrontClient()
		mockDNSClient = cfaws.NewMockDNSClient()
		mockACMClient = cfaws.NewMockACMClient()
		reconciler = &DistributionTenantReconciler{
			Client:    k8sClient,
			Scheme:    k8sClient.Scheme(),
			CFClient:  mockClient,
			ACMClient: mockACMClient,
			NewDNSClient: func(_ string) (cfaws.DNSClient, error) {
				return mockDNSClient, nil
			},
			DriftPolicy: DriftPolicyEnforce,
		}
		namespacedName = types.NamespacedName{
			Name:      tenantName,
			Namespace: tenantNamespace,
		}

		// Set up default distribution info that passes validation.
		// The distribution has a custom ACM cert, so tenants don't need
		// their own cert by default.
		mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
			ID:                        distributionId,
			DomainName:                "d111111abcdef8.cloudfront.net",
			ViewerCertificateArn:      "arn:aws:acm:us-east-1:123456789012:certificate/wildcard-cert",
			UsesCloudFrontDefaultCert: false,
		}

		// Default connection group endpoint for DNS CNAME target resolution.
		mockClient.DefaultConnectionGroupEndpoint = "d111111abcdef8.cloudfront.net"

		// Default ACM certificate SANs cover the test domain.
		mockACMClient.CertificateSANs["arn:aws:acm:us-east-1:123456789012:certificate/wildcard-cert"] = []string{
			"*.example.com", "example.com",
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
			Expect(result.RequeueAfter).To(Equal(1 * time.Second))

			// Verify finalizer was added
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Finalizers).To(ContainElement(cloudfrontv1alpha1.FinalizerName))

			// Second reconcile: validates spec + creates in AWS
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
			// Verify distribution info was fetched for validation
			Expect(mockClient.GetDistributionInfoCallCount).To(Equal(1))
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

		It("should update AWS when spec changes (three-way diff: spec changed)", func() {
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

			// Verify observedGeneration was set
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))

			// Change the spec: add a new domain (this bumps metadata.generation)
			tenant.Spec.Domains = append(tenant.Spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: "new.example.com"})
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			// Verify generation was bumped
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Generation).To(BeNumerically(">", tenant.Status.ObservedGeneration))

			// Reconcile: three-way diff detects spec change, pushes update
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.UpdateCallCount).To(BeNumerically(">", 0))

			// Verify observedGeneration updated after successful update
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))
		})

		It("should allow spec updates while previous deployment is still InProgress", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())

			// Leave AWS status as InProgress (don't call SetTenantStatus("Deployed"))
			// but set observedGeneration so the three-way diff works
			tenant.Status.ObservedGeneration = tenant.Generation
			Expect(k8sClient.Status().Update(ctx, tenant)).To(Succeed())

			// Change the spec while InProgress
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			tenant.Spec.Domains = append(tenant.Spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: "new.example.com"})
			Expect(k8sClient.Update(ctx, tenant)).To(Succeed())

			updateCountBefore := mockClient.UpdateCallCount

			// Reconcile: should NOT block on InProgress, should push the update
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore + 1))
		})

		It("should poll with Ready=False when InProgress and no spec changes", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())

			// AWS is still InProgress, no spec changes
			// Reconcile: should set Ready=False and poll
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDeploying))

			// Synced should still be True (spec matches what we sent to AWS)
			syncedCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeSynced)
			Expect(syncedCond).NotTo(BeNil())
			Expect(syncedCond.Status).To(Equal(metav1.ConditionTrue))
		})

		It("should enforce spec when drift is detected (drift-policy=enforce)", func() {
			// Default policy is enforce (set in BeforeEach)
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

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))
			updateCountBefore := mockClient.UpdateCallCount

			// Simulate external drift
			awsTenant := mockClient.GetTenant(awsID)
			awsTenant.Domains = append(awsTenant.Domains, cfaws.DomainResultOutput{
				Domain: "rogue.example.com", Status: "active",
			})

			// Reconcile: should detect drift and push spec to AWS
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort)) // update requeues short

			// Verify an update was pushed (drift corrected)
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore + 1))
		})

		It("should only report drift without modifying AWS (drift-policy=report)", func() {
			reconciler.DriftPolicy = DriftPolicyReport

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

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ObservedGeneration).To(Equal(tenant.Generation))
			updateCountBefore := mockClient.UpdateCallCount

			// Simulate external drift
			awsTenant := mockClient.GetTenant(awsID)
			awsTenant.Domains = append(awsTenant.Domains, cfaws.DomainResultOutput{
				Domain: "rogue.example.com", Status: "active",
			})

			// Reconcile: should report drift but NOT push update
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// No update was pushed
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore))

			// Drift condition was set
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.DriftDetected).To(BeTrue())
			syncedCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeSynced)
			Expect(syncedCond).NotTo(BeNil())
			Expect(syncedCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(syncedCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDriftDetected))
			Expect(syncedCond.Message).To(ContainSubstring("outside the operator"))
		})

		It("should ignore drift when suspended (drift-policy=suspend)", func() {
			reconciler.DriftPolicy = DriftPolicySuspend

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

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			updateCountBefore := mockClient.UpdateCallCount

			// Simulate external drift
			awsTenant := mockClient.GetTenant(awsID)
			awsTenant.Domains = append(awsTenant.Domains, cfaws.DomainResultOutput{
				Domain: "rogue.example.com", Status: "active",
			})

			// Reconcile: should acknowledge drift without reporting or updating
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// No update was pushed
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore))

			// DriftDetected is set but Synced condition is NOT degraded
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.DriftDetected).To(BeTrue())
			syncedCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeSynced)
			// Synced condition should still be True (from the steady-state reconcile before drift)
			Expect(syncedCond).NotTo(BeNil())
			Expect(syncedCond.Status).To(Equal(metav1.ConditionTrue))
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

			// Reconcile: should requeue via rate limiter (deprecated Requeue, see controller comment)
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck // intentional use, see reconcileUpdate
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

	Context("Spec validation", func() {
		It("should reject creation when distribution uses default cert and tenant has no cert", func() {
			// Configure distribution to use CloudFront default certificate
			mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
				ID:                        distributionId,
				DomainName:                "d111111abcdef8.cloudfront.net",
				UsesCloudFrontDefaultCert: true,
			}

			// Create tenant WITHOUT any certificate customization or managed cert
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Attempt create: should fail validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// Verify validation failure condition
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonValidationFailed))
			Expect(readyCond.Message).To(ContainSubstring("customizations.certificate.arn"))
			Expect(readyCond.Message).To(ContainSubstring("managedCertificateRequest"))

			// Verify AWS create was NOT called
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should allow creation when distribution uses default cert but tenant has managed cert", func() {
			// Distribution uses CloudFront default cert
			mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
				ID:                        distributionId,
				DomainName:                "d111111abcdef8.cloudfront.net",
				UsesCloudFrontDefaultCert: true,
			}

			// Tenant provides a managed certificate request
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "self-hosted",
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Create: validation should pass
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))

			// Verify creation succeeded
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ID).NotTo(BeEmpty())
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should allow creation when distribution uses default cert but tenant has custom cert", func() {
			mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
				ID:                        distributionId,
				DomainName:                "d111111abcdef8.cloudfront.net",
				UsesCloudFrontDefaultCert: true,
			}

			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.Customizations = &cloudfrontv1alpha1.Customizations{
				Certificate: &cloudfrontv1alpha1.CertificateCustomization{
					Arn: "arn:aws:acm:us-east-1:123456789012:certificate/custom-cert",
				},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Create: validation should pass
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should reject creation when required parameters are missing", func() {
			// Distribution requires "origin-domain" parameter with no default
			mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
				ID:                   distributionId,
				DomainName:           "d111111abcdef8.cloudfront.net",
				ViewerCertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/wildcard",
				ParameterDefinitions: []cfaws.ParameterDefinitionOutput{
					{Name: "origin-domain", Required: true, DefaultValue: ""},
					{Name: "cache-ttl", Required: true, DefaultValue: "300"}, // has default
					{Name: "log-bucket", Required: false, DefaultValue: ""},  // optional
				},
			}

			// Create tenant without the required "origin-domain" parameter
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Attempt create: should fail validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			// Verify validation failure
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonValidationFailed))
			Expect(readyCond.Message).To(ContainSubstring("origin-domain"))
			// "cache-ttl" has a default, so it should NOT appear in the error
			Expect(readyCond.Message).NotTo(ContainSubstring("cache-ttl"))

			// AWS create should NOT have been called
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should allow creation when required parameters are provided", func() {
			mockClient.DistributionInfos[distributionId] = &cfaws.DistributionInfo{
				ID:                   distributionId,
				DomainName:           "d111111abcdef8.cloudfront.net",
				ViewerCertificateArn: "arn:aws:acm:us-east-1:123456789012:certificate/wildcard",
				ParameterDefinitions: []cfaws.ParameterDefinitionOutput{
					{Name: "origin-domain", Required: true, DefaultValue: ""},
				},
			}

			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.Parameters = []cloudfrontv1alpha1.Parameter{
				{Name: "origin-domain", Value: "api.example.com"},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should reject creation when resource name is too short", func() {
			shortName := "ab"
			shortNN := types.NamespacedName{Name: shortName, Namespace: tenantNamespace}
			tenant := newTestTenant(shortName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: shortNN})

			// Attempt create: should fail name validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: shortNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			Expect(k8sClient.Get(ctx, shortNN, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonValidationFailed))
			Expect(readyCond.Message).To(ContainSubstring("resource name"))
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should reject creation when resource name exceeds 128 characters", func() {
			// 129 chars: "a" + 127 * "b" + "c" — valid for K8s (max 253) but too long for CloudFront (max 128)
			longName := "a" + strings.Repeat("b", 127) + "c"
			longNN := types.NamespacedName{Name: longName, Namespace: tenantNamespace}
			tenant := newTestTenant(longName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: longNN})

			// Attempt create: should fail name validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: longNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			Expect(k8sClient.Get(ctx, longNN, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonValidationFailed))
			Expect(readyCond.Message).To(ContainSubstring("resource name"))
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should accept creation when resource name is valid", func() {
			validName := "my-tenant.prod"
			validNN := types.NamespacedName{Name: validName, Namespace: tenantNamespace}
			tenant := newTestTenant(validName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: validNN})

			// Create: should pass validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: validNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should reject name validation even when distribution info fetch fails", func() {
			mockClient.GetDistributionInfoError = cfaws.ErrAccessDenied

			shortName := "xy"
			shortNN := types.NamespacedName{Name: shortName, Namespace: tenantNamespace}
			tenant := newTestTenant(shortName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: shortNN})

			// Attempt create: should still fail name validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: shortNN})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			Expect(k8sClient.Get(ctx, shortNN, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonValidationFailed))
			Expect(readyCond.Message).To(ContainSubstring("resource name"))
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should proceed with creation if distribution info fetch fails (graceful degradation)", func() {
			// Make GetDistributionInfo return an error
			mockClient.GetDistributionInfoError = cfaws.ErrAccessDenied

			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Create: should proceed despite validation info being unavailable
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})
	})

	Context("Managed certificate lifecycle", func() {
		It("should track managed certificate status through lifecycle", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "self-hosted",
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Set managed cert as pending-validation
			mockClient.ManagedCertDetails[awsID] = &cfaws.ManagedCertificateDetailsOutput{
				CertificateStatus:   "pending-validation",
				ValidationTokenHost: "self-hosted",
				ValidationTokenDetails: []cfaws.ValidationTokenDetailOutput{
					{
						Domain:       "example.com",
						RedirectFrom: "http://example.com/.well-known/pki-validation/token",
						RedirectTo:   "http://d111.cloudfront.net/.well-known/pki-validation/token",
					},
				},
			}

			// Reconcile: should set CertificateReady=False with pending status
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			// Should poll more frequently when cert is pending
			Expect(result.RequeueAfter).To(Equal(requeueShort))

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			certCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeCertificateReady)
			Expect(certCond).NotTo(BeNil())
			Expect(certCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(certCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonCertPending))
			// Should include validation token details
			Expect(certCond.Message).To(ContainSubstring("validation tokens"))
			Expect(tenant.Status.ManagedCertificateStatus).To(Equal("pending-validation"))
		})

		It("should persist cert ARN to spec and push update on next reconcile when managed cert is issued", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "self-hosted",
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID

			// Set tenant as deployed but with inactive domains
			mockClient.SetTenantStatus(awsID, "Deployed")
			awsTenant := mockClient.GetTenant(awsID)
			awsTenant.Domains = []cfaws.DomainResultOutput{
				{Domain: "example.com", Status: "inactive"},
			}

			// Set managed cert as issued
			certArn := "arn:aws:acm:us-east-1:123456789012:certificate/managed-cert-1234"
			mockClient.ManagedCertDetails[awsID] = &cfaws.ManagedCertificateDetailsOutput{
				CertificateArn:      certArn,
				CertificateStatus:   "issued",
				ValidationTokenHost: "self-hosted",
			}

			updateCountBefore := mockClient.UpdateCallCount

			// Reconcile 1: persists cert ARN to spec but does NOT call AWS update
			// (returns immediately to avoid resourceVersion conflicts)
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// No AWS update yet — only the K8s spec was updated
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore))

			// Verify cert ARN was persisted to the spec
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Spec.Customizations).NotTo(BeNil())
			Expect(tenant.Spec.Customizations.Certificate).NotTo(BeNil())
			Expect(tenant.Spec.Customizations.Certificate.Arn).To(Equal(certArn))

			// Reconcile 2: three-way diff detects spec change (generation bumped
			// by the spec update), pushes update to AWS
			_, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(mockClient.UpdateCallCount).To(Equal(updateCountBefore + 1))

			// Verify cert ARN is tracked in status
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.CertificateArn).To(Equal(certArn))
			Expect(tenant.Status.ManagedCertificateStatus).To(Equal("issued"))
		})

		It("should report certificate failure in CertificateReady condition", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			tenant.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Set managed cert as failed
			mockClient.ManagedCertDetails[awsID] = &cfaws.ManagedCertificateDetailsOutput{
				CertificateStatus:   "validation-timed-out",
				ValidationTokenHost: "cloudfront",
			}

			// Reconcile
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// Verify CertificateReady shows failure
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			certCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeCertificateReady)
			Expect(certCond).NotTo(BeNil())
			Expect(certCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(certCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonCertFailed))
			Expect(certCond.Message).To(ContainSubstring("validation-timed-out"))
		})
	})

	Context("DNS record management", func() {
		const hostedZoneId = "Z1234567890ABC"

		newDNSTenant := func() *cloudfrontv1alpha1.DistributionTenant {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			ttl := int64(300)
			tenant.Spec.DNS = &cloudfrontv1alpha1.DNSConfig{
				Provider:     "route53",
				HostedZoneId: strPtr(hostedZoneId),
				TTL:          &ttl,
			}
			return tenant
		}

		It("should create DNS records before the tenant and wait for propagation", func() {
			mockDNSClient.ChangeStatus = "PENDING"

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Reconcile 1: add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Reconcile 2: upsert DNS records, get PENDING status
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockDNSClient.UpsertCallCount).To(Equal(1))
			Expect(mockClient.CreateCallCount).To(Equal(0))

			// Verify status has changeId and DNSReady=False
			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.DNSChangeId).NotTo(BeEmpty())
			Expect(tenant.Status.DNSTarget).To(Equal("d111111abcdef8.cloudfront.net"))
			dnsCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeDNSReady)
			Expect(dnsCond).NotTo(BeNil())
			Expect(dnsCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(dnsCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDNSPropagating))

			// Reconcile 3: poll DNS, still PENDING
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockDNSClient.GetChangeCallCount).To(Equal(1))
			Expect(mockClient.CreateCallCount).To(Equal(0))

			// DNS propagates
			mockDNSClient.ChangeStatus = dnsStatusInsync

			// Reconcile 4: DNS INSYNC → create tenant
			result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort))
			Expect(mockClient.CreateCallCount).To(Equal(1))

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.ID).NotTo(BeEmpty())
			Expect(tenant.Status.DNSChangeId).To(BeEmpty())
		})

		It("should skip cert SAN check when managedCertificateRequest is set", func() {
			mockDNSClient.ChangeStatus = dnsStatusInsync

			tenant := newDNSTenant()
			tenant.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			}
			// Distribution uses default cert but tenant has managed cert
			mockClient.DistributionInfos[distributionId].UsesCloudFrontDefaultCert = true
			mockClient.DistributionInfos[distributionId].ViewerCertificateArn = ""

			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// Upsert DNS
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// DNS INSYNC → create tenant
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// ACM should NOT have been called since managedCertificateRequest is set
			Expect(mockACMClient.GetSANsCallCount).To(Equal(0))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should reject creation when cert SANs don't cover the domain", func() {
			// Certificate doesn't cover the tenant's domain
			mockACMClient.CertificateSANs["arn:aws:acm:us-east-1:123456789012:certificate/wildcard-cert"] = []string{
				"*.other.com",
			}

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Should fail SAN validation
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			readyCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeReady)
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonCertSANMismatch))
			Expect(readyCond.Message).To(ContainSubstring("example.com"))

			Expect(mockDNSClient.UpsertCallCount).To(Equal(0))
			Expect(mockClient.CreateCallCount).To(Equal(0))
		})

		It("should pass SAN check with wildcard certificate", func() {
			mockDNSClient.ChangeStatus = dnsStatusInsync
			mockACMClient.CertificateSANs["arn:aws:acm:us-east-1:123456789012:certificate/wildcard-cert"] = []string{
				"*.example.com",
			}

			tenant := newDNSTenant()
			tenant.Spec.Domains = []cloudfrontv1alpha1.DomainSpec{
				{Domain: "foo.example.com"},
			}
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// Upsert DNS (SAN check passes)
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// DNS INSYNC → create tenant
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockACMClient.GetSANsCallCount).To(Equal(1))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should handle hosted zone not found as terminal error", func() {
			mockDNSClient.UpsertError = cfaws.ErrHostedZoneNotFound

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Should get terminal error
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			dnsCond := meta.FindStatusCondition(tenant.Status.Conditions, cloudfrontv1alpha1.ConditionTypeDNSReady)
			Expect(dnsCond).NotTo(BeNil())
			Expect(dnsCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(dnsCond.Reason).To(Equal(cloudfrontv1alpha1.ReasonDNSError))
		})

		It("should handle DNS access denied as terminal error", func() {
			mockDNSClient.UpsertError = cfaws.ErrDNSAccessDenied

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueLong))
		})

		It("should delete DNS records when tenant is deleted", func() {
			mockDNSClient.ChangeStatus = dnsStatusInsync

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + upsert DNS + create tenant
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			awsID := tenant.Status.ID
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Mark for deletion
			Expect(k8sClient.Delete(ctx, tenant)).To(Succeed())

			// Reconcile: disable tenant
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Simulate deployment completing
			mockClient.SetTenantStatus(awsID, "Deployed")

			// Reconcile: delete tenant + clean up DNS
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			// DNS records should have been deleted
			Expect(mockDNSClient.DeleteCallCount).To(Equal(1))
			Expect(mockClient.DeleteCallCount).To(Equal(1))
		})

		It("should use connection group routing endpoint when configured", func() {
			mockDNSClient.ChangeStatus = dnsStatusInsync
			connGroupId := "cg-custom-123"
			mockClient.ConnectionGroupEndpoints[connGroupId] = "cg-custom-123.cloudfront.net"

			tenant := newDNSTenant()
			tenant.Spec.ConnectionGroupId = &connGroupId
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			// Upsert DNS
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			Expect(k8sClient.Get(ctx, namespacedName, tenant)).To(Succeed())
			Expect(tenant.Status.DNSTarget).To(Equal("cg-custom-123.cloudfront.net"))
			Expect(mockClient.GetConnectionGroupCallCount).To(Equal(1))
		})

		It("should not create DNS records when dns is not configured", func() {
			tenant := newTestTenant(tenantName, tenantNamespace, distributionId)
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer + create tenant
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())

			Expect(mockDNSClient.UpsertCallCount).To(Equal(0))
			Expect(mockClient.CreateCallCount).To(Equal(1))
		})

		It("should retry when DNS throttling occurs", func() {
			mockDNSClient.UpsertError = cfaws.ErrDNSThrottling

			tenant := newDNSTenant()
			Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

			// Add finalizer
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

			// Should back off on throttling
			result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(requeueShort * 2))
		})
	})
})

// newTestTenant creates a DistributionTenant CR for testing.
//
//nolint:unparam
func newTestTenant(name, namespace, distributionId string) *cloudfrontv1alpha1.DistributionTenant {
	enabled := true
	return &cloudfrontv1alpha1.DistributionTenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: cloudfrontv1alpha1.DistributionTenantSpec{
			DistributionId: distributionId,
			Domains: []cloudfrontv1alpha1.DomainSpec{
				{Domain: "example.com"},
			},
			Enabled: &enabled,
		},
	}
}
