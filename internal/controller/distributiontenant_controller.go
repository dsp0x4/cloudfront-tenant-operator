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

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	cloudfrontv1alpha1 "github.com/paolo-desantis/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/paolo-desantis/cloudfront-tenant-operator/internal/aws"
	cfmetrics "github.com/paolo-desantis/cloudfront-tenant-operator/internal/metrics"
)

const (
	requeueShort = 30 * time.Second
	requeueLong  = 5 * time.Minute
)

// DistributionTenantReconciler reconciles a DistributionTenant object.
type DistributionTenantReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	CFClient cfaws.CloudFrontClient
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=cloudfront.cloudfront-tenant-operator.io,resources=distributiontenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudfront.cloudfront-tenant-operator.io,resources=distributiontenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudfront.cloudfront-tenant-operator.io,resources=distributiontenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is the main reconciliation loop for DistributionTenant resources.
func (r *DistributionTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	start := time.Now()
	result := "success"

	defer func() {
		cfmetrics.ReconcileDuration.WithLabelValues(req.Namespace, req.Name, result).Observe(time.Since(start).Seconds())
	}()

	// 1. Fetch the DistributionTenant CR
	var tenant cloudfrontv1alpha1.DistributionTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Handle deletion
	if !tenant.DeletionTimestamp.IsZero() {
		res, err := r.reconcileDelete(ctx, &tenant)
		if err != nil {
			result = "error"
		}
		return res, err
	}

	// 3. Ensure finalizer
	if !controllerutil.ContainsFinalizer(&tenant, cloudfrontv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(&tenant, cloudfrontv1alpha1.FinalizerName)
		if err := r.Update(ctx, &tenant); err != nil {
			result = "error"
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}

	// 4. Create or update
	var res ctrl.Result
	var err error
	if tenant.Status.ID == "" {
		res, err = r.reconcileCreate(ctx, &tenant)
	} else {
		res, err = r.reconcileExisting(ctx, &tenant, log)
	}

	if err != nil {
		result = "error"
	}
	return res, err
}

// reconcileDelete handles the three-step deletion flow: disable -> wait -> delete.
func (r *DistributionTenantReconciler) reconcileDelete(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(tenant, cloudfrontv1alpha1.FinalizerName) {
		return ctrl.Result{}, nil
	}

	// If never created in AWS, just remove finalizer
	if tenant.Status.ID == "" {
		log.Info("No AWS resource to delete, removing finalizer")
		controllerutil.RemoveFinalizer(tenant, cloudfrontv1alpha1.FinalizerName)
		return ctrl.Result{}, r.Update(ctx, tenant)
	}

	// Set Deleting condition
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
		cloudfrontv1alpha1.ReasonDeleting, "Distribution tenant is being deleted")
	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	// Fetch current AWS state
	awsTenant, err := r.CFClient.GetDistributionTenant(ctx, tenant.Status.ID)
	if cfaws.IsNotFound(err) {
		// Already deleted in AWS, remove finalizer
		log.Info("AWS resource already deleted, removing finalizer")
		r.recordEvent(tenant, "Normal", "Deleted", "Distribution tenant was already deleted in AWS")
		controllerutil.RemoveFinalizer(tenant, cloudfrontv1alpha1.FinalizerName)
		return ctrl.Result{}, r.Update(ctx, tenant)
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get distribution tenant for deletion: %w", err)
	}

	// Step 1: Disable if still enabled
	if awsTenant.Enabled {
		log.Info("Disabling distribution tenant before deletion", "id", tenant.Status.ID)
		r.recordEvent(tenant, "Normal", "Disabling", "Disabling distribution tenant before deletion")
		enabled := false
		_, err := r.CFClient.UpdateDistributionTenant(ctx, &cfaws.UpdateDistributionTenantInput{
			ID:             tenant.Status.ID,
			DistributionId: tenant.Spec.DistributionId,
			IfMatch:        awsTenant.ETag,
			Domains:        specToAWSDomains(tenant.Spec.Domains),
			Enabled:        &enabled,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to disable distribution tenant: %w", err)
		}
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDisabling, "Distribution tenant is being disabled before deletion")
		_ = r.Status().Update(ctx, tenant)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Step 2: Wait for Deployed status
	if awsTenant.Status != "Deployed" {
		log.Info("Waiting for distribution tenant to finish deploying before deletion",
			"id", tenant.Status.ID, "status", awsTenant.Status)
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Step 3: Delete
	log.Info("Deleting distribution tenant from AWS", "id", tenant.Status.ID)
	if err := r.CFClient.DeleteDistributionTenant(ctx, tenant.Status.ID, awsTenant.ETag); err != nil {
		if cfaws.IsResourceNotDisabled(err) {
			// Race condition: still propagating disable. Retry.
			return ctrl.Result{RequeueAfter: requeueShort}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to delete distribution tenant: %w", err)
	}

	log.Info("Distribution tenant deleted from AWS, removing finalizer", "id", tenant.Status.ID)
	r.recordEvent(tenant, "Normal", "Deleted", fmt.Sprintf("Distribution tenant %s deleted from AWS", tenant.Status.ID))
	controllerutil.RemoveFinalizer(tenant, cloudfrontv1alpha1.FinalizerName)
	return ctrl.Result{}, r.Update(ctx, tenant)
}

// reconcileCreate handles creating a new distribution tenant in AWS.
func (r *DistributionTenantReconciler) reconcileCreate(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating distribution tenant in AWS", "name", tenant.Spec.Name)

	input := buildCreateInput(tenant)
	out, err := r.CFClient.CreateDistributionTenant(ctx, input)
	if err != nil {
		return r.handleAWSError(ctx, tenant, err, "create")
	}

	// Update status with AWS-assigned values
	tenant.Status.ID = out.ID
	tenant.Status.Arn = out.Arn
	tenant.Status.ETag = out.ETag
	tenant.Status.DistributionTenantStatus = out.Status
	updateStatusFromAWS(tenant, out)

	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
		cloudfrontv1alpha1.ReasonCreating, "Distribution tenant creation is in progress")
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonInSync, "Spec is in sync with AWS")

	// Set initial CertificateReady condition
	updateCertificateCondition(tenant)

	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	r.recordEvent(tenant, "Normal", "Created", fmt.Sprintf("Distribution tenant created in AWS with ID %s", out.ID))
	log.Info("Distribution tenant created", "id", out.ID, "status", out.Status)
	return ctrl.Result{RequeueAfter: requeueShort}, nil
}

// reconcileExisting handles steady-state reconciliation for existing tenants.
func (r *DistributionTenantReconciler) reconcileExisting(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant, log logr.Logger) (ctrl.Result, error) {
	// Fetch current AWS state
	awsTenant, err := r.CFClient.GetDistributionTenant(ctx, tenant.Status.ID)
	if cfaws.IsNotFound(err) {
		// AWS resource was deleted externally. Clear status and recreate.
		log.Info("AWS resource not found, will recreate", "id", tenant.Status.ID)
		r.recordEvent(tenant, "Warning", "ExternalDeletion", "AWS resource was deleted externally, recreating")
		tenant.Status.ID = ""
		tenant.Status.Arn = ""
		tenant.Status.ETag = ""
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonCreating, "AWS resource not found, recreating")
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{Requeue: true}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get distribution tenant: %w", err)
	}

	// Update cached ETag and status
	tenant.Status.ETag = awsTenant.ETag
	tenant.Status.DistributionTenantStatus = awsTenant.Status
	updateStatusFromAWS(tenant, awsTenant)

	// If still deploying, wait
	if awsTenant.Status == "InProgress" {
		log.Info("Distribution tenant is still deploying", "id", tenant.Status.ID)
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDeploying, "Distribution tenant deployment is in progress")
		updateCertificateCondition(tenant)
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Check if spec has changed vs AWS state (user-initiated update)
	if needsUpdate(tenant, awsTenant) {
		return r.reconcileUpdate(ctx, tenant, awsTenant)
	}

	// Check for external drift (AWS state changed outside the operator)
	if hasDrift(tenant, awsTenant) {
		log.Info("Drift detected between spec and AWS state", "id", tenant.Status.ID)
		r.recordEvent(tenant, "Warning", "DriftDetected", "External drift detected: AWS state differs from spec")
		cfmetrics.DriftDetectedCount.WithLabelValues(tenant.Namespace, tenant.Name).Inc()
		now := metav1.Now()
		tenant.Status.LastDriftCheckTime = &now
		tenant.Status.DriftDetected = true
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDriftDetected, "External drift detected: AWS state differs from K8s spec")
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	// Update drift check time
	now := metav1.Now()
	tenant.Status.LastDriftCheckTime = &now
	tenant.Status.DriftDetected = false

	// Steady state: everything is in sync
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonDeployed, "Distribution tenant is deployed and serving traffic")
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonInSync, "Spec is in sync with AWS")

	// Update certificate readiness condition
	updateCertificateCondition(tenant)

	// Update tenant status gauge
	cfmetrics.TenantCount.WithLabelValues(tenant.Namespace, "Deployed").Set(1)

	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: requeueLong}, nil
}

// reconcileUpdate pushes spec changes to AWS.
func (r *DistributionTenantReconciler) reconcileUpdate(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant, awsTenant *cfaws.DistributionTenantOutput) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Updating distribution tenant in AWS", "id", tenant.Status.ID)

	input := buildUpdateInput(tenant, awsTenant.ETag)
	out, err := r.CFClient.UpdateDistributionTenant(ctx, input)
	if err != nil {
		if cfaws.IsPreconditionFailed(err) {
			// ETag mismatch: re-fetch and retry
			log.Info("ETag mismatch during update, will retry")
			return ctrl.Result{Requeue: true}, nil
		}
		return r.handleAWSError(ctx, tenant, err, "update")
	}

	tenant.Status.ETag = out.ETag
	tenant.Status.DistributionTenantStatus = out.Status
	updateStatusFromAWS(tenant, out)

	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
		cloudfrontv1alpha1.ReasonDeploying, "Distribution tenant update is in progress")
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonUpdatePending, "Update submitted to AWS, waiting for deployment")

	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	r.recordEvent(tenant, "Normal", "Updated", "Distribution tenant update submitted to AWS")
	return ctrl.Result{RequeueAfter: requeueShort}, nil
}

// handleAWSError classifies AWS errors into terminal (set condition, don't retry)
// and retryable (return error for requeue with backoff).
func (r *DistributionTenantReconciler) handleAWSError(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant, err error, operation string) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if cfaws.IsTerminalError(err) {
		reason := cloudfrontv1alpha1.ReasonAWSError
		errorType := "unknown"
		switch {
		case errors.Is(err, cfaws.ErrDomainConflict):
			reason = cloudfrontv1alpha1.ReasonDomainConflict
			errorType = "domain_conflict"
		case errors.Is(err, cfaws.ErrAccessDenied):
			reason = cloudfrontv1alpha1.ReasonAccessDenied
			errorType = "access_denied"
		case errors.Is(err, cfaws.ErrInvalidArgument):
			reason = cloudfrontv1alpha1.ReasonInvalidSpec
			errorType = "invalid_spec"
		}

		log.Error(err, "Terminal AWS error, will not retry", "operation", operation, "reason", reason)
		cfmetrics.ReconcileErrors.WithLabelValues(tenant.Namespace, tenant.Name, errorType).Inc()
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			reason, fmt.Sprintf("AWS %s failed: %v", operation, err))
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			reason, fmt.Sprintf("AWS %s failed: %v", operation, err))
		_ = r.Status().Update(ctx, tenant)
		r.recordEvent(tenant, "Warning", reason, fmt.Sprintf("Terminal error during %s: %v", operation, err))
		// Don't return error: terminal errors should not be requeued.
		// The periodic resync (requeueLong) will re-check later.
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	if cfaws.IsThrottling(err) {
		log.Info("AWS API rate limited, backing off", "operation", operation)
		cfmetrics.ReconcileErrors.WithLabelValues(tenant.Namespace, tenant.Name, "throttling").Inc()
		return ctrl.Result{RequeueAfter: requeueShort * 2}, nil
	}

	// Retryable error: return the error so controller-runtime applies backoff
	cfmetrics.ReconcileErrors.WithLabelValues(tenant.Namespace, tenant.Name, "retryable").Inc()
	return ctrl.Result{}, fmt.Errorf("AWS %s failed (retryable): %w", operation, err)
}

// updateCertificateCondition sets the CertificateReady condition based on
// whether a managed certificate is configured and the domain results from AWS.
func updateCertificateCondition(tenant *cloudfrontv1alpha1.DistributionTenant) {
	if tenant.Spec.ManagedCertificateRequest == nil {
		// No managed cert configured -- set condition to indicate this
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionTrue,
			cloudfrontv1alpha1.ReasonCertNotConfigured, "No managed certificate request configured")
		return
	}

	// Check domain results for all domains being active
	allActive := len(tenant.Status.DomainResults) > 0
	for _, dr := range tenant.Status.DomainResults {
		if dr.Status != "active" {
			allActive = false
			break
		}
	}

	if allActive {
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionTrue,
			cloudfrontv1alpha1.ReasonCertValidated, "Managed certificate is validated and active")
	} else {
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonCertPending, "Managed certificate validation is pending; ensure DNS CNAME records are configured")
	}
}

// hasDrift checks if the AWS state has diverged from what the operator last
// applied (external drift). This is different from needsUpdate which checks
// if the user changed the spec.
func hasDrift(tenant *cloudfrontv1alpha1.DistributionTenant, awsTenant *cfaws.DistributionTenantOutput) bool {
	// Compare enabled state
	specEnabled := true
	if tenant.Spec.Enabled != nil {
		specEnabled = *tenant.Spec.Enabled
	}
	if specEnabled != awsTenant.Enabled {
		return true
	}

	// Compare distribution ID
	if tenant.Spec.DistributionId != awsTenant.DistributionId {
		return true
	}

	// Compare domains
	if len(tenant.Spec.Domains) != len(awsTenant.Domains) {
		return true
	}
	specDomains := make(map[string]bool)
	for _, d := range tenant.Spec.Domains {
		specDomains[d.Domain] = true
	}
	for _, d := range awsTenant.Domains {
		if !specDomains[d.Domain] {
			return true
		}
	}

	return false
}

// recordEvent emits a Kubernetes event if the recorder is configured.
func (r *DistributionTenantReconciler) recordEvent(tenant *cloudfrontv1alpha1.DistributionTenant, eventType, reason, message string) {
	if r.Recorder != nil {
		r.Recorder.Event(tenant, eventType, reason, message)
	}
}

// buildCreateInput converts the CRD spec to an AWS CreateDistributionTenantInput.
func buildCreateInput(tenant *cloudfrontv1alpha1.DistributionTenant) *cfaws.CreateDistributionTenantInput {
	input := &cfaws.CreateDistributionTenantInput{
		DistributionId: tenant.Spec.DistributionId,
		Name:           tenant.Spec.Name,
		Domains:        specToAWSDomains(tenant.Spec.Domains),
		Enabled:        tenant.Spec.Enabled,
	}

	if tenant.Spec.ConnectionGroupId != nil {
		input.ConnectionGroupId = tenant.Spec.ConnectionGroupId
	}

	if len(tenant.Spec.Parameters) > 0 {
		input.Parameters = specToAWSParameters(tenant.Spec.Parameters)
	}

	if tenant.Spec.Customizations != nil {
		input.Customizations = specToAWSCustomizations(tenant.Spec.Customizations)
	}

	if tenant.Spec.ManagedCertificateRequest != nil {
		input.ManagedCertificateRequest = specToAWSManagedCertRequest(tenant.Spec.ManagedCertificateRequest)
	}

	if len(tenant.Spec.Tags) > 0 {
		input.Tags = specToAWSTags(tenant.Spec.Tags)
	}

	return input
}

// buildUpdateInput converts the CRD spec to an AWS UpdateDistributionTenantInput.
func buildUpdateInput(tenant *cloudfrontv1alpha1.DistributionTenant, eTag string) *cfaws.UpdateDistributionTenantInput {
	input := &cfaws.UpdateDistributionTenantInput{
		ID:             tenant.Status.ID,
		DistributionId: tenant.Spec.DistributionId,
		IfMatch:        eTag,
		Domains:        specToAWSDomains(tenant.Spec.Domains),
		Enabled:        tenant.Spec.Enabled,
	}

	if tenant.Spec.ConnectionGroupId != nil {
		input.ConnectionGroupId = tenant.Spec.ConnectionGroupId
	}

	if len(tenant.Spec.Parameters) > 0 {
		input.Parameters = specToAWSParameters(tenant.Spec.Parameters)
	}

	if tenant.Spec.Customizations != nil {
		input.Customizations = specToAWSCustomizations(tenant.Spec.Customizations)
	}

	if tenant.Spec.ManagedCertificateRequest != nil {
		input.ManagedCertificateRequest = specToAWSManagedCertRequest(tenant.Spec.ManagedCertificateRequest)
	}

	return input
}

// needsUpdate compares the CRD spec with the AWS state to determine if an update is needed.
func needsUpdate(tenant *cloudfrontv1alpha1.DistributionTenant, awsTenant *cfaws.DistributionTenantOutput) bool {
	// Compare enabled state
	specEnabled := true
	if tenant.Spec.Enabled != nil {
		specEnabled = *tenant.Spec.Enabled
	}
	if specEnabled != awsTenant.Enabled {
		return true
	}

	// Compare domains
	if len(tenant.Spec.Domains) != len(awsTenant.Domains) {
		return true
	}
	specDomains := make(map[string]bool)
	for _, d := range tenant.Spec.Domains {
		specDomains[d.Domain] = true
	}
	for _, d := range awsTenant.Domains {
		if !specDomains[d.Domain] {
			return true
		}
	}

	// Compare parameters
	if len(tenant.Spec.Parameters) != len(awsTenant.Parameters) {
		return true
	}
	specParams := make(map[string]string)
	for _, p := range tenant.Spec.Parameters {
		specParams[p.Name] = p.Value
	}
	for _, p := range awsTenant.Parameters {
		if specParams[p.Name] != p.Value {
			return true
		}
	}

	// Compare distribution ID
	if tenant.Spec.DistributionId != awsTenant.DistributionId {
		return true
	}

	return false
}

// updateStatusFromAWS updates the CRD status fields from the AWS response.
func updateStatusFromAWS(tenant *cloudfrontv1alpha1.DistributionTenant, out *cfaws.DistributionTenantOutput) {
	if out.CreatedTime != nil {
		t := metav1.NewTime(*out.CreatedTime)
		tenant.Status.CreatedTime = &t
	}
	if out.LastModifiedTime != nil {
		t := metav1.NewTime(*out.LastModifiedTime)
		tenant.Status.LastModifiedTime = &t
	}

	tenant.Status.DomainResults = make([]cloudfrontv1alpha1.DomainResult, len(out.Domains))
	for i, d := range out.Domains {
		tenant.Status.DomainResults[i] = cloudfrontv1alpha1.DomainResult{
			Domain: d.Domain,
			Status: d.Status,
		}
	}
}

// setCondition sets a condition on the DistributionTenant, using the standard
// apimachinery meta helper.
func setCondition(tenant *cloudfrontv1alpha1.DistributionTenant, condType string, status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&tenant.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		ObservedGeneration: tenant.Generation,
		LastTransitionTime: metav1.Now(),
		Reason:             reason,
		Message:            message,
	})
}

// Spec-to-AWS conversion helpers

func specToAWSDomains(domains []cloudfrontv1alpha1.DomainSpec) []cfaws.DomainInput {
	out := make([]cfaws.DomainInput, len(domains))
	for i, d := range domains {
		out[i] = cfaws.DomainInput{Domain: d.Domain}
	}
	return out
}

func specToAWSParameters(params []cloudfrontv1alpha1.Parameter) []cfaws.ParameterInput {
	out := make([]cfaws.ParameterInput, len(params))
	for i, p := range params {
		out[i] = cfaws.ParameterInput{Name: p.Name, Value: p.Value}
	}
	return out
}

func specToAWSCustomizations(c *cloudfrontv1alpha1.Customizations) *cfaws.CustomizationsInput {
	if c == nil {
		return nil
	}
	out := &cfaws.CustomizationsInput{}

	if c.WebAcl != nil {
		out.WebAcl = &cfaws.WebAclCustomizationInput{
			Action: c.WebAcl.Action,
			Arn:    c.WebAcl.Arn,
		}
	}

	if c.Certificate != nil {
		out.Certificate = &cfaws.CertificateCustomizationInput{
			Arn: c.Certificate.Arn,
		}
	}

	if c.GeoRestrictions != nil {
		out.GeoRestrictions = &cfaws.GeoRestrictionCustomizationInput{
			RestrictionType: c.GeoRestrictions.RestrictionType,
			Locations:       c.GeoRestrictions.Locations,
		}
	}

	return out
}

func specToAWSManagedCertRequest(m *cloudfrontv1alpha1.ManagedCertificateRequest) *cfaws.ManagedCertificateRequestInput {
	if m == nil {
		return nil
	}
	return &cfaws.ManagedCertificateRequestInput{
		ValidationTokenHost:                      m.ValidationTokenHost,
		PrimaryDomainName:                        m.PrimaryDomainName,
		CertificateTransparencyLoggingPreference: m.CertificateTransparencyLoggingPreference,
	}
}

func specToAWSTags(tags []cloudfrontv1alpha1.Tag) []cfaws.TagInput {
	out := make([]cfaws.TagInput, len(tags))
	for i, t := range tags {
		out[i] = cfaws.TagInput{Key: t.Key, Value: t.Value}
	}
	return out
}

// SetupWithManager sets up the controller with the Manager.
func (r *DistributionTenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&cloudfrontv1alpha1.DistributionTenant{}).
		Named("distributiontenant").
		Complete(r)
}
