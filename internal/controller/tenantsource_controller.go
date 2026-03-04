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
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
)

const (
	tsRequeueShort = 30 * time.Second
)

// TenantSourceReconciler reconciles a TenantSource object.
// It polls an external data source (DynamoDB) and creates, updates, or deletes
// DistributionTenant CRs to match the external state.
type TenantSourceReconciler struct {
	client.Client
	Scheme            *runtime.Scheme
	Recorder          events.EventRecorder
	NewDynamoDBClient func(region string) cfaws.DynamoDBClient
}

// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=tenantsources/finalizers,verbs=update
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=distributiontenants,verbs=get;list;watch;create;update;patch;delete

// syncResult holds the outcome of a sync operation.
type syncResult struct {
	created        int
	updated        int
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
		return ctrl.Result{}, nil
	}

	if result, err := r.validateSourceConfig(ctx, &source); result != nil {
		return *result, err
	}

	items, err := r.scanDynamoDB(ctx, &source)
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

	if sr.created > 0 || sr.updated > 0 {
		r.recordEvent(&source, "Normal", cloudfrontv1alpha1.TSReasonPollSucceeded,
			fmt.Sprintf("Synced: %d discovered, %d created, %d updated", len(items), sr.created, sr.updated))
	}

	pollInterval := 5 * time.Minute
	if source.Spec.PollInterval != nil {
		pollInterval = source.Spec.PollInterval.Duration
	}

	log.V(1).Info("Poll complete", "discovered", len(items), "created", sr.created,
		"updated", sr.updated, "nextPoll", pollInterval)
	return ctrl.Result{RequeueAfter: pollInterval}, nil
}

// validateSourceConfig checks that the TenantSource spec is valid.
// Returns a non-nil result if the config is invalid (caller should return it).
func (r *TenantSourceReconciler) validateSourceConfig(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) (*ctrl.Result, error) {
	var msg string
	switch {
	case source.Spec.Provider != "dynamodb":
		msg = fmt.Sprintf("Provider %q is not yet supported", source.Spec.Provider)
	case source.Spec.DynamoDB == nil:
		msg = "spec.dynamodb is required when provider is 'dynamodb'"
	default:
		return nil, nil
	}

	r.setCondition(source, metav1.ConditionFalse, cloudfrontv1alpha1.TSReasonInvalidConfig, msg)
	if err := r.Status().Update(ctx, source); err != nil {
		return &ctrl.Result{}, err
	}
	return &ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
}

// scanDynamoDB builds the scan input and calls the DynamoDB client.
func (r *TenantSourceReconciler) scanDynamoDB(ctx context.Context, source *cloudfrontv1alpha1.TenantSource) ([]cfaws.TenantItem, error) {
	region := ""
	if source.Spec.DynamoDB.Region != nil {
		region = *source.Spec.DynamoDB.Region
	}
	dbClient := r.NewDynamoDBClient(region)
	return dbClient.ScanTenants(ctx, buildScanInput(source.Spec.DynamoDB))
}

// handleScanError updates the condition and status when a DynamoDB scan fails.
func (r *TenantSourceReconciler) handleScanError(ctx context.Context, source *cloudfrontv1alpha1.TenantSource, scanErr error) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Error(scanErr, "Failed to scan DynamoDB table", "table", source.Spec.DynamoDB.TableName)

	r.setCondition(source, metav1.ConditionFalse,
		cloudfrontv1alpha1.TSReasonPollFailed,
		fmt.Sprintf("DynamoDB scan failed: %v", scanErr))
	if statusErr := r.Status().Update(ctx, source); statusErr != nil {
		return ctrl.Result{}, statusErr
	}
	r.recordEvent(source, "Warning", cloudfrontv1alpha1.TSReasonPollFailed,
		fmt.Sprintf("Failed to scan DynamoDB table %q: %v", source.Spec.DynamoDB.TableName, scanErr))

	if errors.Is(scanErr, cfaws.ErrAccessDenied) || errors.Is(scanErr, cfaws.ErrDynamoDBTableNotFound) {
		return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
	}
	return ctrl.Result{RequeueAfter: tsRequeueShort}, nil
}

// syncTenants performs the create/update/delete reconciliation between the
// desired state (DynamoDB items) and existing DistributionTenant CRs.
func (r *TenantSourceReconciler) syncTenants(
	ctx context.Context,
	source *cloudfrontv1alpha1.TenantSource,
	items []cfaws.TenantItem,
	existingTenants []cloudfrontv1alpha1.DistributionTenant,
	targetNS string,
	isDryRun bool,
) syncResult {
	desired := make(map[string]cfaws.TenantItem, len(items))
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
	item cfaws.TenantItem,
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
			Description: fmt.Sprintf("New tenant from DynamoDB (domain: %s)", item.Domain),
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
	items []cfaws.TenantItem,
	sr syncResult,
	isDryRun bool,
) {
	now := metav1.Now()
	source.Status.LastPollTime = &now
	source.Status.TenantsDiscovered = len(items)
	source.Status.TenantsCreated = sr.created
	source.Status.TenantsUpdated = sr.updated

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
				fmt.Sprintf("Poll succeeded: %d discovered, %d created, %d updated",
					len(items), sr.created, sr.updated))
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

// buildTenantSpec creates a DistributionTenantSpec from the TenantSource config
// and a DynamoDB TenantItem.
func (r *TenantSourceReconciler) buildTenantSpec(source *cloudfrontv1alpha1.TenantSource, item cfaws.TenantItem) cloudfrontv1alpha1.DistributionTenantSpec {
	spec := cloudfrontv1alpha1.DistributionTenantSpec{
		DistributionId: source.Spec.DistributionId,
		Domains: []cloudfrontv1alpha1.DomainSpec{
			{Domain: item.Domain},
		},
	}

	if item.Enabled != nil {
		spec.Enabled = item.Enabled
	} else {
		defaultEnabled := true
		spec.Enabled = &defaultEnabled
	}

	if item.ConnectionGroupId != nil {
		spec.ConnectionGroupId = item.ConnectionGroupId
	}

	if item.CertificateArn != nil {
		spec.Customizations = &cloudfrontv1alpha1.Customizations{
			Certificate: &cloudfrontv1alpha1.CertificateCustomization{
				Arn: *item.CertificateArn,
			},
		}
	} else if source.Spec.ManagedCertificateRequest != nil {
		spec.ManagedCertificateRequest = &cloudfrontv1alpha1.ManagedCertificateRequest{
			ValidationTokenHost:                      source.Spec.ManagedCertificateRequest.ValidationTokenHost,
			PrimaryDomainName:                        item.Domain,
			CertificateTransparencyLoggingPreference: source.Spec.ManagedCertificateRequest.CertificateTransparencyLoggingPreference,
		}
	}

	return spec
}

// tenantSpecEqual compares two DistributionTenantSpec values for the fields
// managed by TenantSource. It intentionally ignores fields that TenantSource
// does not manage (DNS, tags, parameters).
func tenantSpecEqual(a, b cloudfrontv1alpha1.DistributionTenantSpec) bool {
	if a.DistributionId != b.DistributionId {
		return false
	}

	if len(a.Domains) != len(b.Domains) {
		return false
	}
	for i := range a.Domains {
		if a.Domains[i].Domain != b.Domains[i].Domain {
			return false
		}
	}

	if !boolPtrEqual(a.Enabled, b.Enabled) {
		return false
	}

	if !strPtrEqual(a.ConnectionGroupId, b.ConnectionGroupId) {
		return false
	}

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
	// certificate was issued. Treat it as a managed field and skip the
	// comparison to avoid a perpetual update cycle.
	autoAttached := b.ManagedCertificateRequest != nil && bCert == "" && a.ManagedCertificateRequest != nil
	if !autoAttached && aCert != bCert {
		return false
	}

	if !managedCertRequestEqual(a.ManagedCertificateRequest, b.ManagedCertificateRequest) {
		return false
	}

	return true
}

// applyManagedFields updates only the fields in dst that TenantSource owns,
// preserving unmanaged fields (DNSConfig, Tags, Parameters, and the
// auto-attached certificate ARN from the DistributionTenant controller).
func applyManagedFields(dst *cloudfrontv1alpha1.DistributionTenantSpec, src cloudfrontv1alpha1.DistributionTenantSpec) {
	dst.DistributionId = src.DistributionId
	dst.Domains = src.Domains
	dst.Enabled = src.Enabled
	dst.ConnectionGroupId = src.ConnectionGroupId
	dst.ManagedCertificateRequest = src.ManagedCertificateRequest

	if src.Customizations != nil && src.Customizations.Certificate != nil {
		if dst.Customizations == nil {
			dst.Customizations = &cloudfrontv1alpha1.Customizations{}
		}
		dst.Customizations.Certificate = src.Customizations.Certificate
	} else if src.ManagedCertificateRequest == nil {
		if dst.Customizations != nil {
			dst.Customizations.Certificate = nil
		}
	}
	// When src uses managedCertificateRequest and has no explicit certificate,
	// leave dst.Customizations.Certificate untouched — it holds the
	// auto-attached ARN from the DistributionTenant controller.
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

func buildScanInput(cfg *cloudfrontv1alpha1.DynamoDBSourceConfig) *cfaws.ScanTenantsInput {
	input := &cfaws.ScanTenantsInput{
		TableName:       cfg.TableName,
		NameAttribute:   "name",
		DomainAttribute: "domain",
	}

	if cfg.NameAttribute != nil {
		input.NameAttribute = *cfg.NameAttribute
	}
	if cfg.DomainAttribute != nil {
		input.DomainAttribute = *cfg.DomainAttribute
	}
	if cfg.EnabledAttribute != nil {
		input.EnabledAttribute = *cfg.EnabledAttribute
	}
	if cfg.ConnectionGroupIdAttribute != nil {
		input.ConnectionGroupIdAttribute = *cfg.ConnectionGroupIdAttribute
	}
	if cfg.CertificateArnAttribute != nil {
		input.CertificateArnAttribute = *cfg.CertificateArnAttribute
	}

	return input
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
		For(&cloudfrontv1alpha1.TenantSource{}).
		Owns(&cloudfrontv1alpha1.DistributionTenant{}).
		Named("tenantsource").
		Complete(r)
}
