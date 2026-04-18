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
	"github.com/dsp0x4/cloudfront-tenant-operator/internal/tenantsource"
)

var _ = Describe("TenantSource Controller", func() {
	const (
		sourceName      = "test-source"
		sourceNamespace = "default"
		distributionId  = "E1XNX8R2GOAABC"
		testHostedZone  = "Z1234567890"
	)

	var (
		ctx            context.Context
		mockDB         *tenantsource.MockBackend
		reconciler     *TenantSourceReconciler
		namespacedName types.NamespacedName
	)

	BeforeEach(func() {
		ctx = context.Background()
		mockDB = &tenantsource.MockBackend{}
		reconciler = &TenantSourceReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			Backends: map[string]tenantsource.Backend{
				"dynamodb": mockDB,
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
			{Name: "tenant-b", Domains: []string{"b.example.com"}},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// First reconcile: adds finalizer
		result, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(time.Second))

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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"new-a.example.com"}},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
			{Name: "tenant-b", Domains: []string{"b.example.com"}},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create both
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Remove tenant-b from DynamoDB
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "user-tenant", Domains: []string{"dynamo.example.com"}},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
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

		mockDB.Items = []tenantsource.TenantItem{
			{
				Name:              "tenant-full",
				Domains:           []string{"full.example.com"},
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

	It("should apply managed certificate request from template", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-managed-cert", Domains: []string{"managed.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-custom-cert", Domains: []string{"custom.example.com"}, CertificateArn: &certArn},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			},
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
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-cert", Domains: []string{"cert.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			},
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
		mockDB.Err = tenantsource.ErrSourceNotFound

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

	It("should reject providers that have no registered backend", func() {
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
		// The message should name the missing provider and list the registered ones
		// so operators can tell whether they made a typo or need to enable a backend.
		Expect(readyCond.Message).To(ContainSubstring(`"postgres"`))
		Expect(readyCond.Message).To(ContainSubstring("dynamodb"))
	})

	It("should delete owned tenants when TenantSource is deleted", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-a", Domains: []string{"a.example.com"}},
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

	It("should apply template DNS config to created tenants", func() {
		hostedZone := testHostedZone
		var ttl int64 = 600
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-dns", Domains: []string{"dns.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			DNS: &cloudfrontv1alpha1.DNSConfig{
				Provider:     "route53",
				HostedZoneId: &hostedZone,
				TTL:          &ttl,
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-dns", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.DNS).NotTo(BeNil())
		Expect(tenant.Spec.DNS.Provider).To(Equal("route53"))
		Expect(*tenant.Spec.DNS.HostedZoneId).To(Equal(testHostedZone))
		Expect(*tenant.Spec.DNS.TTL).To(Equal(int64(600)))
	})

	It("should apply template parameters and tags", func() {
		tagVal := "prod"
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-pt", Domains: []string{"pt.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			Parameters: []cloudfrontv1alpha1.Parameter{
				{Name: "env", Value: "production"},
			},
			Tags: []cloudfrontv1alpha1.Tag{
				{Key: "environment", Value: &tagVal},
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-pt", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Parameters).To(HaveLen(1))
		Expect(tenant.Spec.Parameters[0].Name).To(Equal("env"))
		Expect(tenant.Spec.Parameters[0].Value).To(Equal("production"))
		Expect(tenant.Spec.Tags).To(HaveLen(1))
		Expect(tenant.Spec.Tags[0].Key).To(Equal("environment"))
		Expect(*tenant.Spec.Tags[0].Value).To(Equal("prod"))
	})

	It("should apply template WebACL and geo restrictions", func() {
		webAclArn := "arn:aws:wafv2:us-east-1:123:regional/webacl/test/abc"
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-waf", Domains: []string{"waf.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			Customizations: &cloudfrontv1alpha1.Customizations{
				WebAcl: &cloudfrontv1alpha1.WebAclCustomization{
					Action: "override",
					Arn:    &webAclArn,
				},
				GeoRestrictions: &cloudfrontv1alpha1.GeoRestrictionCustomization{
					RestrictionType: "whitelist",
					Locations:       []string{"US", "GB"},
				},
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-waf", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Customizations).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.WebAcl).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.WebAcl.Action).To(Equal("override"))
		Expect(*tenant.Spec.Customizations.WebAcl.Arn).To(Equal(webAclArn))
		Expect(tenant.Spec.Customizations.GeoRestrictions).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.GeoRestrictions.RestrictionType).To(Equal("whitelist"))
		Expect(tenant.Spec.Customizations.GeoRestrictions.Locations).To(Equal([]string{"US", "GB"}))
	})

	It("should allow DynamoDB items to override template fields", func() {
		hostedZone := testHostedZone
		overrideZone := "Z9999999999"
		var ttl int64 = 600
		mockDB.Items = []tenantsource.TenantItem{
			{
				Name:         "tenant-override",
				Domains:      []string{"override.example.com"},
				HostedZoneId: &overrideZone,
			},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			DNS: &cloudfrontv1alpha1.DNSConfig{
				Provider:     "route53",
				HostedZoneId: &hostedZone,
				TTL:          &ttl,
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-override", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.DNS).NotTo(BeNil())
		Expect(tenant.Spec.DNS.Provider).To(Equal("route53"))
		Expect(*tenant.Spec.DNS.HostedZoneId).To(Equal("Z9999999999"))
		Expect(*tenant.Spec.DNS.TTL).To(Equal(int64(600)))
	})

	It("should support multi-domain tenants", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-multi", Domains: []string{"a.example.com", "b.example.com", "c.example.com"}},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-multi", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Domains).To(HaveLen(3))
		Expect(tenant.Spec.Domains[0].Domain).To(Equal("a.example.com"))
		Expect(tenant.Spec.Domains[1].Domain).To(Equal("b.example.com"))
		Expect(tenant.Spec.Domains[2].Domain).To(Equal("c.example.com"))
	})

	It("should not update when template and DynamoDB produce same spec as existing CR", func() {
		hostedZone := testHostedZone
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-nodiff", Domains: []string{"nodiff.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			DNS: &cloudfrontv1alpha1.DNSConfig{
				Provider:     "route53",
				HostedZoneId: &hostedZone,
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Poll again — same data, no updates expected
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.TenantsUpdated).To(Equal(0))
		Expect(source.Status.TenantsCreated).To(Equal(0))
	})

	It("should propagate template changes to existing tenants", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-tmpl", Domains: []string{"tmpl.example.com"}},
		}

		source := newTestSource()
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Add a template with DNS
		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		hostedZone := testHostedZone
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			DNS: &cloudfrontv1alpha1.DNSConfig{
				Provider:     "route53",
				HostedZoneId: &hostedZone,
			},
		}
		Expect(k8sClient.Update(ctx, source)).To(Succeed())

		// Reconcile — should detect diff and update
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		Expect(source.Status.TenantsUpdated).To(Equal(1))

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-tmpl", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.DNS).NotTo(BeNil())
		Expect(tenant.Spec.DNS.Provider).To(Equal("route53"))
	})

	It("should allow DynamoDB to override managed certificate fields", func() {
		overrideHost := "self-hosted"
		overridePrimary := "override.example.com"
		mockDB.Items = []tenantsource.TenantItem{
			{
				Name:                "tenant-cert-override",
				Domains:             []string{"cert-override.example.com"},
				ValidationTokenHost: &overrideHost,
				PrimaryDomainName:   &overridePrimary,
			},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-cert-override", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.ManagedCertificateRequest).NotTo(BeNil())
		Expect(tenant.Spec.ManagedCertificateRequest.ValidationTokenHost).To(Equal("self-hosted"))
		Expect(tenant.Spec.ManagedCertificateRequest.PrimaryDomainName).To(Equal("override.example.com"))
	})

	It("should preserve auto-attached cert ARN on update when using managed cert", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{Name: "tenant-preserve", Domains: []string{"preserve.example.com"}},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			ManagedCertificateRequest: &cloudfrontv1alpha1.ManagedCertificateRequest{
				ValidationTokenHost: "cloudfront",
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		// Finalizer + create
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})

		// Simulate auto-attached certificate ARN
		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-preserve", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		tenant.Spec.Customizations = &cloudfrontv1alpha1.Customizations{
			Certificate: &cloudfrontv1alpha1.CertificateCustomization{
				Arn: "arn:aws:acm:us-east-1:123:certificate/auto",
			},
		}
		Expect(k8sClient.Update(ctx, &tenant)).To(Succeed())

		// Add a DNS template to trigger an update
		Expect(k8sClient.Get(ctx, namespacedName, source)).To(Succeed())
		hostedZone := "ZABC"
		source.Spec.Template.DNS = &cloudfrontv1alpha1.DNSConfig{
			Provider:     "route53",
			HostedZoneId: &hostedZone,
		}
		Expect(k8sClient.Update(ctx, source)).To(Succeed())

		// Reconcile — should update DNS but preserve auto-attached cert
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-preserve", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.DNS).NotTo(BeNil())
		Expect(tenant.Spec.DNS.Provider).To(Equal("route53"))
		Expect(tenant.Spec.Customizations).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate).NotTo(BeNil())
		Expect(tenant.Spec.Customizations.Certificate.Arn).To(Equal("arn:aws:acm:us-east-1:123:certificate/auto"))
		Expect(tenant.Spec.ManagedCertificateRequest).NotTo(BeNil())
	})

	It("should override template parameters when DynamoDB provides them", func() {
		mockDB.Items = []tenantsource.TenantItem{
			{
				Name:       "tenant-param-override",
				Domains:    []string{"param.example.com"},
				Parameters: map[string]string{"env": "staging", "region": "eu-west-1"},
			},
		}

		source := newTestSource()
		source.Spec.Template = &cloudfrontv1alpha1.TenantTemplate{
			Parameters: []cloudfrontv1alpha1.Parameter{
				{Name: "env", Value: "production"},
				{Name: "tier", Value: "standard"},
			},
		}
		Expect(k8sClient.Create(ctx, source)).To(Succeed())

		_, _ = reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		_, err := reconciler.Reconcile(ctx, reconcile.Request{NamespacedName: namespacedName})
		Expect(err).NotTo(HaveOccurred())

		var tenant cloudfrontv1alpha1.DistributionTenant
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "tenant-param-override", Namespace: sourceNamespace}, &tenant)).To(Succeed())
		Expect(tenant.Spec.Parameters).To(HaveLen(2))
		paramMap := make(map[string]string)
		for _, p := range tenant.Spec.Parameters {
			paramMap[p.Name] = p.Value
		}
		Expect(paramMap["env"]).To(Equal("staging"))
		Expect(paramMap["region"]).To(Equal("eu-west-1"))
	})
})
