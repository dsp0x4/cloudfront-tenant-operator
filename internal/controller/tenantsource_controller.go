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
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	"github.com/dsp0x4/cloudfront-tenant-operator/internal/tenantsource"
)

const (
	tsRequeueShort = 30 * time.Second
)

// TenantSourceReconciler reconciles a TenantSource object.
// It polls an external data source and creates, updates, or deletes
// DistributionTenant CRs to match the external state. The concrete source
// implementation is chosen at reconcile time from Backends using the value of
// spec.provider as the key.
type TenantSourceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
	Backends map[string]tenantsource.Backend
}

// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=distributiontenants,verbs=get;list;watch;create;update;patch;delete

// syncResult holds the outcome of a sync operation.
type syncResult struct {
	created        int
	updated        int
	deleted        int
	pendingChanges []cloudfrontv1alpha1.PendingChange
	errs           []string
}

// Reconcile polls the external source and syncs DistributionTenant CRs.
func (r *TenantSourceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var source cloudfrontv1alpha1.TenantSource
	if err := r.Get(ctx, req.NamespacedName, &source); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !source.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &source)
	}

	if !controllerutil.ContainsFinalizer(&source, cloudfrontv1alpha1.TenantSourceFinalizerName) {
		controllerutil.AddFinalizer(&source, cloudfrontv1alpha1.TenantSourceFinalizerName)
		if err := r.Update(ctx, &source); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}

	backend, result, err := r.resolveBackend(ctx, &source)
	if result != nil {
		return *result, err
	}

	items, err := backend.QueryTenants(ctx, &source)
	if err != nil {
		return r.handleScanError(ctx, &source, err)
	}

	targetNS := r.targetNamespace(&source)
	existingTenants, err := r.listOwnedTenants(ctx, &source, targetNS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list owned tenants: %w", err)
	}

	isDryRun := source.Spec.DryRun != nil && *source.Spec.DryRun
	sr := r.syncTenants(ctx, &source, items, existingTenants, targetNS, isDryRun)
	r.updateSyncStatus(&source, items, sr, isDryRun)

	if err := r.Status().Update(ctx, &source); err != nil {
		return ctrl.Result{}, err
	}

	if sr.created > 0 || sr.updated > 0 || sr.deleted > 0 {
		r.recordEvent(&source, "Normal", cloudfrontv1alpha1.TSReasonPollSucceeded,
			fmt.Sprintf("Synced: %d discovered, %d created, %d updated, %d deleted",
				len(items), sr.created, sr.updated, sr.deleted))
	}

	pollInterval := 5 * time.Minute
	if source.Spec.PollInterval != nil {
		pollInterval = source.Spec.PollInterval.Duration
	}

	log.V(1).Info("Poll complete", "discovered", len(items), "created", sr.created,
		"updated", sr.updated, "deleted", sr.deleted, "nextPoll", pollInterval)
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// resolveBackend looks up the Backend registered for spec.provider. If no
// backend is registered it writes an InvalidConfig condition and returns a
// non-nil ctrl.Result the caller should propagate.
func (r *TenantSourceReconciler) resolveBackend(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) (tenantsource.Backend, *ctrl.Result, error) {
	backend, ok := r.Backends[source.Spec.Provider]
	if !ok {
		msg := fmt.Sprintf("Provider %q is not registered (known providers: %s)",
			source.Spec.Provider, knownProviders(r.Backends))
		r.setCondition(source, metav1.ConditionFalse, cloudfrontv1alpha1.TSReasonInvalidConfig, msg)
		if err := r.Status().Update(ctx, source); err != nil {
			return nil, &ctrl.Result{}, err
		}
		return nil, &ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	return backend, nil, nil
}

// knownProviders returns a stable, comma-separated list of registered backend
// keys for use in error messages. An empty registry produces "<none>".
func knownProviders(backends map[string]tenantsource.Backend) string {
	if len(backends) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(backends))
	for k := range backends {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}

// handleScanError updates the condition and status when a backend query fails.
func (r *TenantSourceReconciler) handleScanError(ctx context.Context, source *cloudfrontv1alpha1.TenantSource, scanErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Error(scanErr, "Failed to query tenant source", "provider", source.Spec.Provider)

	// Invalid config surfaced by a backend (e.g. spec.dynamodb missing) is
	// reported as InvalidConfig rather than PollFailed so users see the same
	// reason whether the issue was caught during backend resolution or later.
	reason := cloudfrontv1alpha1.TSReasonPollFailed
	if errors.Is(scanErr, tenantsource.ErrSourceInvalidConfig) {
		reason = cloudfrontv1alpha1.TSReasonInvalidConfig
	}

	r.setCondition(source, metav1.ConditionFalse,
		reason,
		fmt.Sprintf("Tenant source query failed: %v", scanErr))
	if statusErr := r.Status().Update(ctx, source); statusErr != nil {
		return ctrl.Result{}, statusErr
	}
	r.recordEvent(source, "Warning", reason,
		fmt.Sprintf("Failed to query tenant source (provider=%s): %v", source.Spec.Provider, scanErr))

	if errors.Is(scanErr, tenantsource.ErrSourceAccessDenied) ||
		errors.Is(scanErr, tenantsource.ErrSourceNotFound) ||
		errors.Is(scanErr, tenantsource.ErrSourceInvalidConfig) {
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	return ctrl.Result{RequeueAfter: tsRequeueShort}, nil
}

// syncTenants performs the create/update/delete reconciliation between the
// desired state (DynamoDB items) and existing DistributionTenant CRs.
func (r *TenantSourceReconciler) syncTenants(
	ctx context.Context,
	source *cloudfrontv1alpha1.TenantSource,
	items []tenantsource.TenantItem,
	existingTenants []cloudfrontv1alpha1.DistributionTenant,
	targetNS string,
	isDryRun bool,
) syncResult {
	desired := make(map[string]tenantsource.TenantItem, len(items))
	for _, item := range items {
		desired[item.Name] = item
	}

	existing := make(map[string]*cloudfrontv1alpha1.DistributionTenant, len(existingTenants))
	for i := range existingTenants {
		existing[existingTenants[i].Name] = &existingTenants[i]
	}

	var sr syncResult

	for name, item := range desired {
		spec := r.buildTenantSpec(source, item)
		if dt, exists := existing[name]; exists {
			r.syncExistingTenant(ctx, source, dt, spec, name, isDryRun, &sr)
		} else {
			r.syncNewTenant(ctx, source, spec, item, name, targetNS, isDryRun, &sr)
		}
	}

	for name, dt := range existing {
		if _, stillExists := desired[name]; stillExists {
			continue
		}
		if !r.isOwnedBySource(dt, source) {
			continue
		}
		if isDryRun {
			sr.pendingChanges = append(sr.pendingChanges, cloudfrontv1alpha1.PendingChange{
				Action: "delete", TenantName: name, Description: "No longer present in DynamoDB",
			})
		} else if err := r.Delete(ctx, dt); err != nil {
			sr.errs = append(sr.errs, fmt.Sprintf("failed to delete tenant %q: %v", name, err))
		} else {
			sr.deleted++
		}
	}

	return sr
}

func (r *TenantSourceReconciler) syncExistingTenant(
	ctx context.Context,
	source *cloudfrontv1alpha1.TenantSource,
	dt *cloudfrontv1alpha1.DistributionTenant,
	spec cloudfrontv1alpha1.DistributionTenantSpec,
	name string,
	isDryRun bool,
	sr *syncResult,
) {
	if !r.isOwnedBySource(dt, source) {
		sr.errs = append(sr.errs,
			fmt.Sprintf("tenant %q already exists but is not managed by this TenantSource", name))
		return
	}
	if tenantSpecEqual(dt.Spec, spec) {
		return
	}
	if isDryRun {
		sr.pendingChanges = append(sr.pendingChanges, cloudfrontv1alpha1.PendingChange{
			Action: "update", TenantName: name, Description: "Spec differs from DynamoDB item",
		})
		return
	}
	applyManagedFields(&dt.Spec, spec)
	if err := r.Update(ctx, dt); err != nil {
		sr.errs = append(sr.errs, fmt.Sprintf("failed to update tenant %q: %v", name, err))
		return
	}
	sr.updated++
}

func (r *TenantSourceReconciler) syncNewTenant(
	ctx context.Context,
	source *cloudfrontv1alpha1.TenantSource,
	spec cloudfrontv1alpha1.DistributionTenantSpec,
	item tenantsource.TenantItem,
	name, targetNS string,
	isDryRun bool,
	sr *syncResult,
) {
	var conflictCheck cloudfrontv1alpha1.DistributionTenant
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: targetNS}, &conflictCheck); err == nil {
		if !r.isOwnedBySource(&conflictCheck, source) {
			sr.errs = append(sr.errs,
				fmt.Sprintf("tenant %q already exists but is not managed by this TenantSource", name))
			return
		}
	}

	if isDryRun {
		sr.pendingChanges = append(sr.pendingChanges, cloudfrontv1alpha1.PendingChange{
			Action: "create", TenantName: name,
			Description: fmt.Sprintf("New tenant from DynamoDB (domains: %v)", item.Domains),
		})
		return
	}

	dt := &cloudfrontv1alpha1.DistributionTenant{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: targetNS,
			Labels: map[string]string{cloudfrontv1alpha1.TenantSourceLabelKey: source.Name},
		},
		Spec: spec,
	}
	if err := controllerutil.SetOwnerReference(source, dt, r.Scheme); err != nil {
		sr.errs = append(sr.errs, fmt.Sprintf("failed to set owner reference on %q: %v", name, err))
		return
	}
	if err := r.Create(ctx, dt); err != nil {
		sr.errs = append(sr.errs, fmt.Sprintf("failed to create tenant %q: %v", name, err))
		return
	}
	sr.created++
}

// updateSyncStatus populates the TenantSource status after a sync.
func (r *TenantSourceReconciler) updateSyncStatus(
	source *cloudfrontv1alpha1.TenantSource,
	items []tenantsource.TenantItem,
	sr syncResult,
	isDryRun bool,
) {
	now := metav1.Now()
	source.Status.LastPollTime = &now
	source.Status.TenantsDiscovered = len(items)
	source.Status.TenantsCreated = sr.created
	source.Status.TenantsUpdated = sr.updated
	source.Status.TenantsDeleted = sr.deleted

	if isDryRun {
		source.Status.PendingChanges = sr.pendingChanges
		r.setCondition(source, metav1.ConditionTrue,
			cloudfrontv1alpha1.TSReasonDryRunComplete,
			fmt.Sprintf("Dry run complete: %d discovered, %d pending changes",
				len(items), len(sr.pendingChanges)))
	} else {
		source.Status.PendingChanges = nil
		if len(sr.errs) > 0 {
			r.setCondition(source, metav1.ConditionFalse,
				cloudfrontv1alpha1.TSReasonConflict,
				fmt.Sprintf("Poll succeeded with errors: %v", sr.errs))
		} else {
			r.setCondition(source, metav1.ConditionTrue,
				cloudfrontv1alpha1.TSReasonPollSucceeded,
				fmt.Sprintf("Poll succeeded: %d discovered, %d created, %d updated, %d deleted",
					len(items), sr.created, sr.updated, sr.deleted))
		}
	}
}

func (r *TenantSourceReconciler) targetNamespace(source *cloudfrontv1alpha1.TenantSource) string {
	if source.Spec.TargetNamespace != nil && *source.Spec.TargetNamespace != "" {
		return *source.Spec.TargetNamespace
	}
	return source.Namespace
}

// reconcileDelete cleans up owned DistributionTenants and removes the finalizer.
func (r *TenantSourceReconciler) reconcileDelete(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(source, cloudfrontv1alpha1.TenantSourceFinalizerName) {
		return ctrl.Result{}, nil
	}

	// Determine target namespace
	targetNS := source.Namespace
	if source.Spec.TargetNamespace != nil && *source.Spec.TargetNamespace != "" {
		targetNS = *source.Spec.TargetNamespace
	}

	tenants, err := r.listOwnedTenants(ctx, source, targetNS)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to list owned tenants for cleanup: %w", err)
	}

	if len(tenants) > 0 {
		log.Info("Deleting owned DistributionTenants", "count", len(tenants))
		for i := range tenants {
			if err := r.Delete(ctx, &tenants[i]); client.IgnoreNotFound(err) != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete tenant %q: %w", tenants[i].Name, err)
			}
		}
		// Requeue to wait for tenants to be fully deleted before removing finalizer
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	log.Info("All owned tenants deleted, removing finalizer")
	controllerutil.RemoveFinalizer(source, cloudfrontv1alpha1.TenantSourceFinalizerName)
	if err := r.Update(ctx, source); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// listOwnedTenants returns DistributionTenants with the source label in the
// given namespace.
func (r *TenantSourceReconciler) listOwnedTenants(ctx context.Context, source *cloudfrontv1alpha1.TenantSource, namespace string) ([]cloudfrontv1alpha1.DistributionTenant, error) {
	var list cloudfrontv1alpha1.DistributionTenantList
	if err := r.List(ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{cloudfrontv1alpha1.TenantSourceLabelKey: source.Name},
	); err != nil {
		return nil, err
	}
	return list.Items, nil
}

// isOwnedBySource returns true if the DistributionTenant has an owner
// reference pointing to the given TenantSource.
func (r *TenantSourceReconciler) isOwnedBySource(dt *cloudfrontv1alpha1.DistributionTenant, source *cloudfrontv1alpha1.TenantSource) bool {
	for _, ref := range dt.OwnerReferences {
		if ref.UID == source.UID {
			return true
		}
	}
	return false
}

// buildTenantSpec creates a DistributionTenantSpec by starting from the
// TenantSource template and overlaying per-item DynamoDB values.
// Precedence: DynamoDB item value > template value > K8s default.
func (r *TenantSourceReconciler) buildTenantSpec(source *cloudfrontv1alpha1.TenantSource, item tenantsource.TenantItem) cloudfrontv1alpha1.DistributionTenantSpec {
	tmpl := source.Spec.Template

	spec := cloudfrontv1alpha1.DistributionTenantSpec{
		DistributionId: source.Spec.DistributionId,
	}

	// Domains — always from DynamoDB item.
	for _, d := range item.Domains {
		spec.Domains = append(spec.Domains, cloudfrontv1alpha1.DomainSpec{Domain: d})
	}

	// Enabled: item > template > true.
	switch {
	case item.Enabled != nil:
		spec.Enabled = item.Enabled
	case tmpl != nil && tmpl.Enabled != nil:
		spec.Enabled = tmpl.Enabled
	default:
		defaultEnabled := true
		spec.Enabled = &defaultEnabled
	}

	// ConnectionGroupId: item > template.
	spec.ConnectionGroupId = firstNonNilStr(item.ConnectionGroupId, ptrFromTemplate(tmpl, func(t *cloudfrontv1alpha1.TenantTemplate) *string { return t.ConnectionGroupId }))

	// Certificate: explicit ARN > managed cert request.
	r.buildCertificateFields(&spec, tmpl, item)

	// DNS: overlay item fields on template.
	r.buildDNSFields(&spec, tmpl, item)

	// Customizations (WebACL, GeoRestrictions): overlay item on template.
	r.buildCustomizationFields(&spec, tmpl, item)

	// Parameters: item replaces template entirely if present.
	r.buildParameterFields(&spec, tmpl, item)

	// Tags: item replaces template entirely if present.
	r.buildTagFields(&spec, tmpl, item)

	return spec
}

func (r *TenantSourceReconciler) buildCertificateFields(
	spec *cloudfrontv1alpha1.DistributionTenantSpec,
	tmpl *cloudfrontv1alpha1.TenantTemplate,
	item tenantsource.TenantItem,
) {
	if item.CertificateArn != nil {
		if spec.Customizations == nil {
			spec.Customizations = &cloudfrontv1alpha1.Customizations{}
		}
		spec.Customizations.Certificate = &cloudfrontv1alpha1.CertificateCustomization{
			Arn: *item.CertificateArn,
		}
		return
	}

	var tmplCert *cloudfrontv1alpha1.ManagedCertificateRequest
	if tmpl != nil {
		tmplCert = tmpl.ManagedCertificateRequest
	}
	if tmplCert == nil {
		return
	}

	mcr := &cloudfrontv1alpha1.ManagedCertificateRequest{
		ValidationTokenHost:                      tmplCert.ValidationTokenHost,
		PrimaryDomainName:                        tmplCert.PrimaryDomainName,
		CertificateTransparencyLoggingPreference: tmplCert.CertificateTransparencyLoggingPreference,
	}

	if item.ValidationTokenHost != nil {
		mcr.ValidationTokenHost = *item.ValidationTokenHost
	}
	if item.PrimaryDomainName != nil {
		mcr.PrimaryDomainName = *item.PrimaryDomainName
	}
	if item.CertificateTransparencyLoggingPreference != nil {
		mcr.CertificateTransparencyLoggingPreference = item.CertificateTransparencyLoggingPreference
	}

	if mcr.PrimaryDomainName == "" && len(item.Domains) > 0 {
		mcr.PrimaryDomainName = item.Domains[0]
	}

	spec.ManagedCertificateRequest = mcr
}

func (r *TenantSourceReconciler) buildDNSFields(
	spec *cloudfrontv1alpha1.DistributionTenantSpec,
	tmpl *cloudfrontv1alpha1.TenantTemplate,
	item tenantsource.TenantItem,
) {
	var base *cloudfrontv1alpha1.DNSConfig
	if tmpl != nil && tmpl.DNS != nil {
		cp := *tmpl.DNS
		base = &cp
	}

	hasOverride := item.DNSProvider != nil || item.HostedZoneId != nil ||
		item.DNSTTL != nil || item.DNSAssumeRoleArn != nil

	if base == nil && !hasOverride {
		return
	}
	if base == nil {
		base = &cloudfrontv1alpha1.DNSConfig{}
	}
	if item.DNSProvider != nil {
		base.Provider = *item.DNSProvider
	}
	if item.HostedZoneId != nil {
		base.HostedZoneId = item.HostedZoneId
	}
	if item.DNSTTL != nil {
		base.TTL = item.DNSTTL
	}
	if item.DNSAssumeRoleArn != nil {
		base.AssumeRoleArn = item.DNSAssumeRoleArn
	}
	spec.DNS = base
}

func (r *TenantSourceReconciler) buildCustomizationFields(
	spec *cloudfrontv1alpha1.DistributionTenantSpec,
	tmpl *cloudfrontv1alpha1.TenantTemplate,
	item tenantsource.TenantItem,
) {
	var tmplCustom *cloudfrontv1alpha1.Customizations
	if tmpl != nil {
		tmplCustom = tmpl.Customizations
	}

	// WebACL
	hasWebAcl := item.WebAclAction != nil
	var webAcl *cloudfrontv1alpha1.WebAclCustomization
	if tmplCustom != nil && tmplCustom.WebAcl != nil {
		cp := *tmplCustom.WebAcl
		webAcl = &cp
	}
	if hasWebAcl {
		if webAcl == nil {
			webAcl = &cloudfrontv1alpha1.WebAclCustomization{}
		}
		webAcl.Action = *item.WebAclAction
	}
	if item.WebAclArn != nil {
		if webAcl == nil {
			webAcl = &cloudfrontv1alpha1.WebAclCustomization{}
		}
		webAcl.Arn = item.WebAclArn
	}

	// GeoRestrictions
	hasGeo := item.GeoRestrictionType != nil || item.GeoLocations != nil
	var geo *cloudfrontv1alpha1.GeoRestrictionCustomization
	if tmplCustom != nil && tmplCustom.GeoRestrictions != nil {
		cp := *tmplCustom.GeoRestrictions
		geo = &cp
	}
	if hasGeo {
		if geo == nil {
			geo = &cloudfrontv1alpha1.GeoRestrictionCustomization{}
		}
		if item.GeoRestrictionType != nil {
			geo.RestrictionType = *item.GeoRestrictionType
		}
		if item.GeoLocations != nil {
			geo.Locations = item.GeoLocations
		}
	}

	if webAcl != nil || geo != nil {
		if spec.Customizations == nil {
			spec.Customizations = &cloudfrontv1alpha1.Customizations{}
		}
		spec.Customizations.WebAcl = webAcl
		spec.Customizations.GeoRestrictions = geo
	}
}

func (r *TenantSourceReconciler) buildParameterFields(
	spec *cloudfrontv1alpha1.DistributionTenantSpec,
	tmpl *cloudfrontv1alpha1.TenantTemplate,
	item tenantsource.TenantItem,
) {
	if item.Parameters != nil {
		for k, v := range item.Parameters {
			spec.Parameters = append(spec.Parameters, cloudfrontv1alpha1.Parameter{Name: k, Value: v})
		}
		return
	}
	if tmpl != nil && len(tmpl.Parameters) > 0 {
		spec.Parameters = make([]cloudfrontv1alpha1.Parameter, len(tmpl.Parameters))
		copy(spec.Parameters, tmpl.Parameters)
	}
}

func (r *TenantSourceReconciler) buildTagFields(
	spec *cloudfrontv1alpha1.DistributionTenantSpec,
	tmpl *cloudfrontv1alpha1.TenantTemplate,
	item tenantsource.TenantItem,
) {
	if item.Tags != nil {
		for k, v := range item.Tags {
			spec.Tags = append(spec.Tags, cloudfrontv1alpha1.Tag{Key: k, Value: &v})
		}
		return
	}
	if tmpl != nil && len(tmpl.Tags) > 0 {
		spec.Tags = make([]cloudfrontv1alpha1.Tag, len(tmpl.Tags))
		copy(spec.Tags, tmpl.Tags)
	}
}

// tenantSpecEqual compares two DistributionTenantSpec values for all fields
// managed by TenantSource (which is now all spec fields).
func tenantSpecEqual(a, b cloudfrontv1alpha1.DistributionTenantSpec) bool {
	if a.DistributionId != b.DistributionId {
		return false
	}
	if !domainsEqual(a.Domains, b.Domains) {
		return false
	}
	if !boolPtrEqual(a.Enabled, b.Enabled) {
		return false
	}
	if !strPtrEqual(a.ConnectionGroupId, b.ConnectionGroupId) {
		return false
	}
	if !certificateFieldsEqual(a, b) {
		return false
	}
	if !managedCertRequestEqual(a.ManagedCertificateRequest, b.ManagedCertificateRequest) {
		return false
	}
	if !dnsConfigEqual(a.DNS, b.DNS) {
		return false
	}
	if !webAclEqual(a.Customizations, b.Customizations) {
		return false
	}
	if !geoRestrictionsEqual(a.Customizations, b.Customizations) {
		return false
	}
	if !parametersEqual(a.Parameters, b.Parameters) {
		return false
	}
	if !tagsEqual(a.Tags, b.Tags) {
		return false
	}
	return true
}

func domainsEqual(a, b []cloudfrontv1alpha1.DomainSpec) bool {
	as := make([]string, len(a))
	for i, d := range a {
		as[i] = d.Domain
	}
	bs := make([]string, len(b))
	for i, d := range b {
		bs[i] = d.Domain
	}
	return stringSliceEqual(as, bs)
}

func certificateFieldsEqual(a, b cloudfrontv1alpha1.DistributionTenantSpec) bool {
	aCert := ""
	if a.Customizations != nil && a.Customizations.Certificate != nil {
		aCert = a.Customizations.Certificate.Arn
	}
	bCert := ""
	if b.Customizations != nil && b.Customizations.Certificate != nil {
		bCert = b.Customizations.Certificate.Arn
	}

	// When the desired spec (b) uses managedCertificateRequest and has no
	// explicit cert ARN, any cert ARN on the existing spec (a) was
	// auto-attached by the DistributionTenant controller after the managed
	// certificate was issued. Skip the comparison to avoid a perpetual
	// update cycle.
	autoAttached := b.ManagedCertificateRequest != nil && bCert == "" && a.ManagedCertificateRequest != nil
	return autoAttached || aCert == bCert
}

func dnsConfigEqual(a, b *cloudfrontv1alpha1.DNSConfig) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.Provider != b.Provider {
		return false
	}
	if !strPtrEqual(a.HostedZoneId, b.HostedZoneId) {
		return false
	}
	if !int64PtrEqual(a.TTL, b.TTL) {
		return false
	}
	return strPtrEqual(a.AssumeRoleArn, b.AssumeRoleArn)
}

func webAclEqual(a, b *cloudfrontv1alpha1.Customizations) bool {
	aWac := (*cloudfrontv1alpha1.WebAclCustomization)(nil)
	bWac := (*cloudfrontv1alpha1.WebAclCustomization)(nil)
	if a != nil {
		aWac = a.WebAcl
	}
	if b != nil {
		bWac = b.WebAcl
	}
	if aWac == nil && bWac == nil {
		return true
	}
	if aWac == nil || bWac == nil {
		return false
	}
	if aWac.Action != bWac.Action {
		return false
	}
	return strPtrEqual(aWac.Arn, bWac.Arn)
}

func geoRestrictionsEqual(a, b *cloudfrontv1alpha1.Customizations) bool {
	aGeo := (*cloudfrontv1alpha1.GeoRestrictionCustomization)(nil)
	bGeo := (*cloudfrontv1alpha1.GeoRestrictionCustomization)(nil)
	if a != nil {
		aGeo = a.GeoRestrictions
	}
	if b != nil {
		bGeo = b.GeoRestrictions
	}
	if aGeo == nil && bGeo == nil {
		return true
	}
	if aGeo == nil || bGeo == nil {
		return false
	}
	if aGeo.RestrictionType != bGeo.RestrictionType {
		return false
	}
	return stringSliceEqual(aGeo.Locations, bGeo.Locations)
}

func parametersEqual(a, b []cloudfrontv1alpha1.Parameter) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]string, len(a))
	for _, p := range a {
		am[p.Name] = p.Value
	}
	for _, p := range b {
		if am[p.Name] != p.Value {
			return false
		}
	}
	return true
}

func tagsEqual(a, b []cloudfrontv1alpha1.Tag) bool {
	if len(a) != len(b) {
		return false
	}
	am := make(map[string]*string, len(a))
	for i := range a {
		am[a[i].Key] = a[i].Value
	}
	for i := range b {
		if !strPtrEqual(am[b[i].Key], b[i].Value) {
			return false
		}
	}
	return true
}

// applyManagedFields copies the full desired spec onto the existing spec,
// preserving only the auto-attached certificate ARN from the DistributionTenant
// controller.
func applyManagedFields(dst *cloudfrontv1alpha1.DistributionTenantSpec, src cloudfrontv1alpha1.DistributionTenantSpec) {
	autoAttachedCert := (*cloudfrontv1alpha1.CertificateCustomization)(nil)
	if src.ManagedCertificateRequest != nil && (src.Customizations == nil || src.Customizations.Certificate == nil) {
		if dst.Customizations != nil && dst.Customizations.Certificate != nil {
			cp := *dst.Customizations.Certificate
			autoAttachedCert = &cp
		}
	}

	*dst = src

	if autoAttachedCert != nil {
		if dst.Customizations == nil {
			dst.Customizations = &cloudfrontv1alpha1.Customizations{}
		}
		dst.Customizations.Certificate = autoAttachedCert
	}
}

func managedCertRequestEqual(a, b *cloudfrontv1alpha1.ManagedCertificateRequest) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	if a.ValidationTokenHost != b.ValidationTokenHost {
		return false
	}
	if a.PrimaryDomainName != b.PrimaryDomainName {
		return false
	}
	return strPtrEqual(a.CertificateTransparencyLoggingPreference, b.CertificateTransparencyLoggingPreference)
}

func boolPtrEqual(a, b *bool) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func strPtrEqual(a, b *string) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func int64PtrEqual(a, b *int64) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[string]struct{}, len(a))
	for _, s := range a {
		set[s] = struct{}{}
	}
	for _, s := range b {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func firstNonNilStr(ptrs ...*string) *string {
	for _, p := range ptrs {
		if p != nil {
			return p
		}
	}
	return nil
}

func ptrFromTemplate[T any](tmpl *cloudfrontv1alpha1.TenantTemplate, fn func(*cloudfrontv1alpha1.TenantTemplate) T) T {
	var zero T
	if tmpl == nil {
		return zero
	}
	return fn(tmpl)
}

func (r *TenantSourceReconciler) setCondition(source *cloudfrontv1alpha1.TenantSource, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&source.Status.Conditions, metav1.Condition{
		Type:               cloudfrontv1alpha1.TSConditionTypeReady,
		Status:             status,
		ObservedGeneration: source.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
}

func (r *TenantSourceReconciler) recordEvent(source *cloudfrontv1alpha1.TenantSource, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Eventf(source, nil, eventType, reason, "Reconcile", message)
}

// SetupWithManager sets up the controller with the Manager.
func (r *TenantSourceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudfrontv1alpha1.TenantSource{},
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Owns(&cloudfrontv1alpha1.DistributionTenant{}).
		Named("tenantsource").
		Complete(r)
}
