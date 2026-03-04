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

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
)

var _ = Describe("TenantSource Controller", func() {
	const (
		sourceName      = "test-source"
		sourceNamespace = "default"
		distributionId  = "E1XNX8R2GOAABC"
	)

	var (
		ctx            context.Context
		mockDB         *cfaws.MockDynamoDBClient
		reconciler     *TenantSourceReconciler
		namespacedName types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockDB = &cfaws.MockDynamoDBClient{}
		reconciler = &TenantSourceReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			NewDynamoDBClient: func(_ string) cfaws.DynamoDBClient {
				return mockDB
			},
		}
		namespacedName = types.NamespacedName{
			Name:      sourceName,
			Namespace: sourceNamespace,
		}
	})

	AfterEach(func() {
		// Clean up TenantSource
		source := &cloudfrontv1alpha1.TenantSource{}
		if err := k8sClient.Get(ctx, namespacedName, source); err == nil {
			source.Finalizers = nil
			_ = k8sClient.Update(ctx, source)
			_ = k8sClient.Delete(ctx, source)
		}

		// Clean up owned DistributionTenants
		var tenants cloudfrontv1alpha1.DistributionTenantList
		if err := k8sClient.List(ctx, &tenants); err == nil {
			for i := range tenants.Items {
				tenants.Items[i].Finalizers = nil
				_ = k8sClient.Update(ctx, &tenants.Items[i])
				_ = k8sClient.Delete(ctx, &tenants.Items[i])
			}
		}
	})

	newTestSource := func() *cloudfrontv1alpha1.TenantSource {
		pollInterval := metav1.Duration{Duration: 5 * time.Minute}
		return &cloudfrontv1alpha1.TenantSource{
			ObjectMeta: metav1.ObjectMeta{
				Name:      sourceName,
				Namespace: sourceNamespace,
			},
			Spec: cloudfrontv1alpha1.TenantSourceSpec{
				Provider:       "dynamodb",
				DistributionId: distributionId,
				PollInterval:   &pollInterval,
				DynamoDB: &cloudfrontv1alpha1.DynamoDBSourceConfig{
					TableName: "tenants-table",
				},
			},
		}
	}

	It("should add a finalizer and create tenants from DynamoDB scan", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
			{Name: "tenant-b", Domain: "b.example.com"},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// First reconcile: adds finalizer
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Finalizers).To(ContainElement(cloudfrontv1alpha1.TenantSourceFinalizerName))

		// Second reconcile: scans and creates tenants
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

		// Verify tenants were created
		var tenantA cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenantA)).To(Succeed())
		Expect(tenantA.Spec.DistributionId).To(Equal(distributionId))
		Expect(tenantA.Spec.Domains).To(HaveLen(1))
		Expect(tenantA.Spec.Domains[0].Domain).To(Equal("a.example.com"))
		Expect(tenantA.Labels[cloudfrontv1alpha1.TenantSourceLabelKey]).To(Equal(sourceName))
		Expect(tenantA.OwnerReferences).To(HaveLen(1))

		var tenantB cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-b", Namespace: sourceNamespace}, &tenantB)).To(Succeed())
		Expect(tenantB.Spec.Domains[0].Domain).To(Equal("b.example.com"))

		// Verify status
		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.TenantsDiscovered).To(Equal(2))
		Expect(source.Status.TenantsCreated).To(Equal(2))
		Expect(source.Status.LastPollTime).NotTo(BeNil())

		readyCond := meta.FindStatusCondition(source.Status.Conditions, cloudfrontv1alpha1.TSConditionTypeReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
		Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.TSReasonPollSucceeded))
	})

	It("should update tenants when DynamoDB items change", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Verify initial state
		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Domains[0].Domain).To(Equal("a.example.com"))

		// Change domain in DynamoDB
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "new-a.example.com"},
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		// Verify update
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Domains[0].Domain).To(Equal("new-a.example.com"))

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.TenantsUpdated).To(Equal(1))
	})

	It("should delete tenants removed from DynamoDB", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
			{Name: "tenant-b", Domain: "b.example.com"},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create both
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Remove tenant-b from DynamoDB
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
		}

		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		// tenant-a should still exist
		var tenantA cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenantA)).To(Succeed())

		// tenant-b should be deleted
		var tenantB cloudfrontv1alpha1.DistributionTenant
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-b", Namespace: sourceNamespace}, &tenantB)
		Expect(err).To(HaveOccurred())
	})

	It("should not modify user-created tenants", func() {
		// Create a user-managed tenant (no owner reference or label)
		userTenant := &cloudfrontv1alpha1.DistributionTenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "user-tenant",
				Namespace: sourceNamespace,
			},
			Spec: cloudfrontv1alpha1.DistributionTenantSpec{
				DistributionId: distributionId,
				Domains:        []cloudfrontv1alpha1.DomainSpec{{Domain: "user.example.com"}},
			},
		}
		Expect(k8sClient.Create(ctx, userTenant)).To(Succeed())

		// DynamoDB has an item with the same name
		mockDB.Items = []cfaws.TenantItem{
			{Name: "user-tenant", Domain: "dynamo.example.com"},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + poll
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		// User tenant should be unchanged
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "user-tenant", Namespace: sourceNamespace}, userTenant)).To(Succeed())
		Expect(userTenant.Spec.Domains[0].Domain).To(Equal("user.example.com"))
		Expect(userTenant.OwnerReferences).To(BeEmpty())

		// Status should report conflict
		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		readyCond := meta.FindStatusCondition(source.Status.Conditions, cloudfrontv1alpha1.TSConditionTypeReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.TSReasonConflict))
	})

	It("should populate pendingChanges in dry-run mode", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
		}

		dryRun := true
		source := newTestSource()
		source.Spec.DryRun = &dryRun
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + poll
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		// No DistributionTenant should be created
		var tenant cloudfrontv1alpha1.DistributionTenant
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenant)
		Expect(err).To(HaveOccurred())

		// Status should have pending changes
		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.PendingChanges).To(HaveLen(1))
		Expect(source.Status.PendingChanges[0].Action).To(Equal("create"))
		Expect(source.Status.PendingChanges[0].TenantName).To(Equal("tenant-a"))

		readyCond := meta.FindStatusCondition(source.Status.Conditions, cloudfrontv1alpha1.TSConditionTypeReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.TSReasonDryRunComplete))
	})

	It("should map optional attributes from DynamoDB", func() {
		enabled := false
		connGroupId := "cg-123"
		certArn := "arn:aws:acm:us-east-1:123:certificate/abc"

		mockDB.Items = []cfaws.TenantItem{
			{
				Name:              "tenant-full",
				Domain:            "full.example.com",
				Enabled:           &enabled,
				ConnectionGroupId: &connGroupId,
				CertificateArn:    &certArn,
			},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-full", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(*tenant.Spec.Enabled).To(BeFalse())
		Expect(*tenant.Spec.ConnectionGroupId).To(Equal("cg-123"))
		Expect(tenant.Spec.Customizations).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate.Arn).To(Equal(certArn))
	})

	It("should apply managed certificate request when configured", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-managed-cert", Domain: "managed.example.com"},
		}

		source := newTestSource()
		source.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.TenantSourceManagedCertificateRequest{
			ValidationTokenHost: "cloudfront",
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-managed-cert", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.ManagedCertificateRequest).NotTo(BeNil())
		Expect(tenant.Spec.ManagedCertificateRequest.ValidationTokenHost).To(Equal("cloudfront"))
		Expect(tenant.Spec.ManagedCertificateRequest.PrimaryDomainName).To(Equal("managed.example.com"))
		Expect(tenant.Spec.Customizations).To(BeNil())
	})

	It("should prefer certificateArn over managed certificate request", func() {
		certArn := "arn:aws:acm:us-east-1:123:certificate/custom"
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-custom-cert", Domain: "custom.example.com", CertificateArn: &certArn},
		}

		source := newTestSource()
		source.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.TenantSourceManagedCertificateRequest{
			ValidationTokenHost: "cloudfront",
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-custom-cert", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.ManagedCertificateRequest).To(BeNil())
		Expect(tenant.Spec.Customizations).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate.Arn).To(Equal(certArn))
	})

	It("should not generate a diff when managed cert ARN is auto-attached", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-cert", Domain: "cert.example.com"},
		}

		source := newTestSource()
		source.Spec.ManagedCertificateRequest = &cloudfrontv1alpha1.TenantSourceManagedCertificateRequest{
			ValidationTokenHost: "cloudfront",
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Simulate what the DistributionTenant controller does: auto-attach
		// the managed certificate ARN to customizations.certificate.
		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-cert", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		tenant.Spec.Customizations = &cloudfrontv1alpha1.Customizations{
			Certificate: &cloudfrontv1alpha1.CertificateCustomization{
				Arn: "arn:aws:acm:us-east-1:123:certificate/auto-attached",
			},
		}
		Expect(k8sClient.Update(ctx, &tenant)).To(Succeed())

		// Poll again -- should NOT detect a diff and should NOT update
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.TenantsUpdated).To(Equal(0))

		// Verify the auto-attached ARN is still there
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-cert", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Customizations).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate.Arn).To(Equal("arn:aws:acm:us-east-1:123:certificate/auto-attached"))
		Expect(tenant.Spec.ManagedCertificateRequest).NotTo(BeNil())
	})

	It("should handle DynamoDB scan errors gracefully", func() {
		mockDB.Err = cfaws.ErrDynamoDBTableNotFound

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Poll with error
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		readyCond := meta.FindStatusCondition(source.Status.Conditions, cloudfrontv1alpha1.TSConditionTypeReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
		Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.TSReasonPollFailed))
	})

	It("should reject unsupported providers", func() {
		source := newTestSource()
		source.Spec.Provider = "postgres"
		source.Spec.DynamoDB = nil
		source.Spec.Postgres = &cloudfrontv1alpha1.PostgresSourceConfig{
			ConnectionSecretRef: cloudfrontv1alpha1.SecretReference{Name: "test"},
			Query:               "SELECT * FROM tenants",
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Minute))

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		readyCond := meta.FindStatusCondition(source.Status.Conditions, cloudfrontv1alpha1.TSConditionTypeReady)
		Expect(readyCond).NotTo(BeNil())
		Expect(readyCond.Reason).To(Equal(cloudfrontv1alpha1.TSReasonInvalidConfig))
	})

	It("should delete owned tenants when TenantSource is deleted", func() {
		mockDB.Items = []cfaws.TenantItem{
			{Name: "tenant-a", Domain: "a.example.com"},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Verify tenant exists
		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenant)).To(Succeed())

		// Delete the TenantSource
		Expect(k8sClient.Delete(ctx, source)).To(Succeed())

		// Reconcile deletion: should delete owned tenants
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(5 * time.Second))

		// Tenant should be deleted (or deleting)
		err = k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-a", Namespace: sourceNamespace}, &tenant)
		Expect(err).To(HaveOccurred())

		// Next reconcile: no tenants left, remove finalizer
		result, err = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(reconcile.Result{}))
	})
})
