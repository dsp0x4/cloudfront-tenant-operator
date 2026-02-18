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
	"regexp"
	"strings"
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

	cloudfrontv1alpha1 "github.com/dsp0x4/cloudfront-tenant-operator/api/v1alpha1"
	cfaws "github.com/dsp0x4/cloudfront-tenant-operator/internal/aws"
	cfmetrics "github.com/dsp0x4/cloudfront-tenant-operator/internal/metrics"
)

const (
	requeueShort = 30 * time.Second
	requeueLong  = 5 * time.Minute
)

// tenantNameRegex validates the resource name against CloudFront naming
// constraints: 3-128 characters, must start and end with a lowercase
// alphanumeric, middle characters may include dots and hyphens.
// This is the lowercase equivalent of CloudFront's own pattern.
var tenantNameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9.\-]{1,126}[a-z0-9]$`)

// DistributionTenantReconciler reconciles a DistributionTenant object.
type DistributionTenantReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	CFClient    cfaws.CloudFrontClient
	Recorder    record.EventRecorder
	DriftPolicy DriftPolicy
}

// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=distributiontenants,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=distributiontenants/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=cloudfront-tenant-operator.io,resources=distributiontenants/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// Reconcile is the main reconciliation loop for DistributionTenant resources.
//
// Reconcile duration and total counts are already tracked by controller-runtime's
// built-in metrics (controller_runtime_reconcile_time_seconds, controller_runtime_reconcile_total).
func (r *DistributionTenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	// 1. Fetch the DistributionTenant CR
	var tenant cloudfrontv1alpha1.DistributionTenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// 2. Handle deletion
	if !tenant.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &tenant)
	}

	// 3. Ensure finalizer
	if !controllerutil.ContainsFinalizer(&tenant, cloudfrontv1alpha1.FinalizerName) {
		controllerutil.AddFinalizer(&tenant, cloudfrontv1alpha1.FinalizerName)
		if err := r.Update(ctx, &tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// 4. Create or update
	if tenant.Status.ID == "" {
		return r.reconcileCreate(ctx, &tenant)
	}
	return r.reconcileExisting(ctx, &tenant, log)
}

// reconcileDelete handles the three-step deletion flow: disable -> wait -> delete.
//
// Each step ends by returning a result, so each reconcile does at most ONE
// write to the K8s API (either a status update or a metadata update to remove
// the finalizer). This avoids resourceVersion conflicts from multiple writes
// within the same reconciliation.
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
		out, err := r.CFClient.UpdateDistributionTenant(ctx, &cfaws.UpdateDistributionTenantInput{
			ID:             tenant.Status.ID,
			DistributionId: tenant.Spec.DistributionId,
			IfMatch:        awsTenant.ETag,
			Domains:        specToAWSDomains(tenant.Spec.Domains),
			Enabled:        &enabled,
		})
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to disable distribution tenant: %w", err)
		}
		// Update cached ETag and status from the disable response so the
		// status subresource reflects the real AWS state (e.g. InProgress).
		tenant.Status.ETag = out.ETag
		tenant.Status.DistributionTenantStatus = out.Status
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDisabling, "Distribution tenant is being disabled before deletion")
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Step 2: Wait for Deployed status
	if awsTenant.Status != "Deployed" {
		log.Info("Waiting for distribution tenant to finish deploying before deletion",
			"id", tenant.Status.ID, "status", awsTenant.Status)
		// Keep distributionTenantStatus in sync with what AWS reports.
		tenant.Status.DistributionTenantStatus = awsTenant.Status
		tenant.Status.ETag = awsTenant.ETag
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDeleting, "Waiting for deployment to complete before deletion")
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
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
// It performs spec validation against the distribution configuration before
// calling the AWS API, giving users clear error messages for misconfigurations.
func (r *DistributionTenantReconciler) reconcileCreate(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Creating distribution tenant in AWS", "name", tenant.Name)

	// Validate spec against distribution configuration before calling AWS
	if res, err, handled := r.validateSpec(ctx, tenant); handled {
		return res, err
	}

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
	tenant.Status.ObservedGeneration = tenant.Generation
	updateStatusFromAWS(tenant, out)

	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
		cloudfrontv1alpha1.ReasonCreating, "Distribution tenant creation is in progress")
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonInSync, "Spec is in sync with AWS")

	// Set initial CertificateReady condition
	updateCertificateCondition(tenant, nil)

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
		// Use a short RequeueAfter rather than the deprecated Requeue field.
		// The next reconcile sees an empty ID and calls reconcileCreate.
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get distribution tenant: %w", err)
	}

	// Update cached ETag and status
	tenant.Status.ETag = awsTenant.ETag
	tenant.Status.DistributionTenantStatus = awsTenant.Status
	updateStatusFromAWS(tenant, awsTenant)

	// Fetch managed certificate details if configured
	var certDetails *cfaws.ManagedCertificateDetailsOutput
	if tenant.Spec.ManagedCertificateRequest != nil {
		certDetails, err = r.CFClient.GetManagedCertificateDetails(ctx, tenant.Status.ID)
		if err != nil {
			log.Error(err, "Failed to get managed certificate details", "id", tenant.Status.ID)
			// Non-fatal: continue reconciliation, cert status will be stale
		}
		if certDetails != nil {
			tenant.Status.CertificateArn = certDetails.CertificateArn
			tenant.Status.ManagedCertificateStatus = certDetails.CertificateStatus
		}
	}

	// Three-way diff: compare spec, last-reconciled generation, and AWS state.
	// We do NOT gate on "InProgress" for updates: CloudFront uses ETags for
	// optimistic concurrency, so submitting a new update while the previous
	// one is still deploying is safe. This avoids unnecessary delays for spec
	// changes, cert attachment, and drift correction. The delete flow has its
	// own "InProgress" gate in reconcileDelete.
	change := detectChanges(tenant, awsTenant)
	log.V(1).Info("Change detection result", "id", tenant.Status.ID, "change", change.String(),
		"generation", tenant.Generation, "observedGeneration", tenant.Status.ObservedGeneration)

	switch change {
	case changeSpec:
		if awsTenant.Status == "InProgress" {
			log.Info("Submitting update while previous deployment is still in progress",
				"id", tenant.Status.ID, "generation", tenant.Generation)
		} else {
			log.Info("Spec changed since last reconciliation, updating AWS",
				"id", tenant.Status.ID, "generation", tenant.Generation)
		}
		// Validate spec before update
		if res, err, handled := r.validateSpec(ctx, tenant); handled {
			return res, err
		}
		return r.reconcileUpdate(ctx, tenant, awsTenant)

	case changeDrift:
		return r.handleDrift(ctx, tenant, awsTenant, log)
	}

	// changeNone: no spec change, no drift.

	// Check managed certificate lifecycle BEFORE any status update to ensure
	// each path does at most one K8s API write per reconcile. This must run
	// before the InProgress gate: the cert can be issued while a previous
	// deployment is still in progress, and we want to persist the ARN
	// immediately so the next reconcile pushes it to AWS.
	if certDetails != nil && certDetails.CertificateStatus == "issued" && certDetails.CertificateArn != "" {
		if !allDomainsActive(tenant) {
			log.Info("Managed certificate issued, persisting ARN to spec for attachment",
				"id", tenant.Status.ID, "certArn", certDetails.CertificateArn)
			r.recordEvent(tenant, "Normal", "CertificateAttaching",
				fmt.Sprintf("Managed certificate %s issued, attaching to domains", certDetails.CertificateArn))

			if tenant.Spec.Customizations == nil {
				tenant.Spec.Customizations = &cloudfrontv1alpha1.Customizations{}
			}
			tenant.Spec.Customizations.Certificate = &cloudfrontv1alpha1.CertificateCustomization{
				Arn: certDetails.CertificateArn,
			}
			if err := r.Update(ctx, tenant); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to persist managed certificate ARN to spec: %w", err)
			}

			// The spec update bumps the generation and triggers a new reconcile
			// via the watch. That reconcile will detect changeSpec and push to AWS
			// (even if the tenant is still InProgress — that's handled above).
			return ctrl.Result{}, nil
		}
	}

	// If still deploying, set Ready=False and poll until deployed.
	if awsTenant.Status == "InProgress" {
		log.Info("Distribution tenant is still deploying", "id", tenant.Status.ID)
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDeploying, "Distribution tenant deployment is in progress")
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
			cloudfrontv1alpha1.ReasonInSync, "Spec is in sync with AWS")
		updateCertificateCondition(tenant, certDetails)
		tenant.Status.ObservedGeneration = tenant.Generation
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueShort}, nil
	}

	// Steady state: deployed and in sync.
	now := metav1.Now()
	tenant.Status.LastDriftCheckTime = &now
	tenant.Status.DriftDetected = false
	tenant.Status.ObservedGeneration = tenant.Generation

	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonDeployed, "Distribution tenant is deployed and serving traffic")
	setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionTrue,
		cloudfrontv1alpha1.ReasonInSync, "Spec is in sync with AWS")

	// Update certificate readiness condition
	updateCertificateCondition(tenant, certDetails)

	if err := r.Status().Update(ctx, tenant); err != nil {
		return ctrl.Result{}, err
	}

	// If managed certificate is pending validation, requeue more frequently
	// to detect cert issuance sooner and trigger attachment.
	if certDetails != nil && certDetails.CertificateStatus == "pending-validation" {
		return ctrl.Result{RequeueAfter: requeueShort}, nil
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
			// ETag mismatch: re-fetch and retry.
			//
			// We intentionally use the deprecated Requeue field here because:
			// 1. We want the work queue's rate limiter (exponential backoff) to
			//    handle repeated ETag conflicts, rather than picking an arbitrary
			//    fixed interval via RequeueAfter.
			// 2. Returning an error would increment controller_runtime_reconcile_errors_total,
			//    which is misleading since an ETag mismatch is expected under
			//    concurrent updates, not a controller failure.
			//
			// This is an open discussion in the controller-runtime community:
			// https://github.com/kubernetes-sigs/controller-runtime/issues/3297
			log.Info("ETag mismatch during update, will retry")
			return ctrl.Result{Requeue: true}, nil //nolint:staticcheck // see comment above
		}
		return r.handleAWSError(ctx, tenant, err, "update")
	}

	tenant.Status.ETag = out.ETag
	tenant.Status.DistributionTenantStatus = out.Status
	tenant.Status.ObservedGeneration = tenant.Generation
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

// handleDrift responds to external drift according to the configured DriftPolicy.
func (r *DistributionTenantReconciler) handleDrift(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant, awsTenant *cfaws.DistributionTenantOutput, log logr.Logger) (ctrl.Result, error) {
	log.Info("External drift detected: AWS state was modified outside the operator",
		"id", tenant.Status.ID, "driftPolicy", r.DriftPolicy)
	cfmetrics.DriftDetectedCount.Inc()

	now := metav1.Now()
	tenant.Status.LastDriftCheckTime = &now
	tenant.Status.DriftDetected = true

	switch r.DriftPolicy {
	case DriftPolicyEnforce:
		// Overwrite AWS with the K8s spec (spec is the source of truth).
		r.recordEvent(tenant, "Warning", "DriftCorrected",
			"External drift detected, enforcing K8s spec as source of truth")
		log.Info("Enforcing spec over drifted AWS state", "id", tenant.Status.ID)
		return r.reconcileUpdate(ctx, tenant, awsTenant)

	case DriftPolicySuspend:
		// Acknowledge drift but don't report it as a problem.
		log.Info("Drift detection is suspended, ignoring external changes",
			"id", tenant.Status.ID)
		// Still update the drift check time so the status is fresh.
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil

	default: // DriftPolicyReport (and fallback)
		r.recordEvent(tenant, "Warning", "DriftDetected",
			"External drift detected: AWS state differs from K8s spec (spec unchanged since last successful reconciliation)")
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonDriftDetected,
			"External drift detected: AWS state was modified outside the operator while K8s spec remained unchanged")
		if err := r.Status().Update(ctx, tenant); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}
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
		cfmetrics.ReconcileErrors.WithLabelValues(errorType).Inc()
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			reason, fmt.Sprintf("AWS %s failed: %v", operation, err))
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			reason, fmt.Sprintf("AWS %s failed: %v", operation, err))
		if statusErr := r.Status().Update(ctx, tenant); statusErr != nil {
			// If we can't persist the terminal error condition, return the
			// status update error so the reconcile retries with a fresh object.
			log.Error(statusErr, "Failed to update status after terminal AWS error")
			return ctrl.Result{}, statusErr
		}
		r.recordEvent(tenant, "Warning", reason, fmt.Sprintf("Terminal error during %s: %v", operation, err))
		// Don't return the AWS error: terminal errors should not be requeued.
		// The periodic resync (requeueLong) will re-check later.
		return ctrl.Result{RequeueAfter: requeueLong}, nil
	}

	if cfaws.IsThrottling(err) {
		log.Info("AWS API rate limited, backing off", "operation", operation)
		cfmetrics.ReconcileErrors.WithLabelValues("throttling").Inc()
		return ctrl.Result{RequeueAfter: requeueShort * 2}, nil
	}

	// Retryable error: return the error so controller-runtime applies backoff
	cfmetrics.ReconcileErrors.WithLabelValues("retryable").Inc()
	return ctrl.Result{}, fmt.Errorf("AWS %s failed (retryable): %w", operation, err)
}

// validateSpec checks the tenant's spec against the parent distribution
// configuration. It returns (result, err, handled) where handled=true means
// a validation error was found and the caller should return the result.
// err is non-nil only when the status update to persist the validation
// failure fails — in that case the caller should return the error so the
// reconcile retries with a fresh object.
func (r *DistributionTenantReconciler) validateSpec(ctx context.Context, tenant *cloudfrontv1alpha1.DistributionTenant) (ctrl.Result, error, bool) {
	log := logf.FromContext(ctx)

	var validationErrors []string

	// 0. Name validation (local, no AWS call needed):
	// metadata.name is used as the CloudFront tenant name, so it must conform
	// to CloudFront's naming constraints (3-128 chars, start/end with
	// alphanumeric, only lowercase alphanumerics, dots, and hyphens).
	if !tenantNameRegex.MatchString(tenant.Name) {
		validationErrors = append(validationErrors,
			"resource name must be 3-128 characters, start and end with a lowercase alphanumeric, "+
				"and contain only lowercase alphanumerics, dots, and hyphens")
	}

	distInfo, err := r.CFClient.GetDistributionInfo(ctx, tenant.Spec.DistributionId)
	if err != nil {
		// If we can't fetch distribution info, log a warning but let the
		// request proceed -- the AWS API will catch any real errors.
		log.Error(err, "Failed to fetch distribution info for validation; proceeding without local validation",
			"distributionId", tenant.Spec.DistributionId)
		if len(validationErrors) == 0 {
			return ctrl.Result{}, nil, false
		}
		// Fall through to report the name validation error even if we can't
		// fetch distribution info.
	}

	// The following checks require distribution info from AWS. Skip them if
	// the fetch failed (distInfo will be nil).
	if distInfo != nil {
		// 1. Certificate coverage check:
		// If the distribution uses the CloudFront default cert (*.cloudfront.net)
		// or has no custom ACM cert, the tenant MUST provide either a custom
		// certificate ARN or a managed certificate request.
		if distInfo.UsesCloudFrontDefaultCert || distInfo.ViewerCertificateArn == "" {
			hasCertCoverage := false
			if tenant.Spec.Customizations != nil && tenant.Spec.Customizations.Certificate != nil {
				hasCertCoverage = true
			}
			if tenant.Spec.ManagedCertificateRequest != nil {
				hasCertCoverage = true
			}
			if !hasCertCoverage {
				validationErrors = append(validationErrors,
					"the parent distribution does not have a custom ACM certificate; "+
						"the tenant must provide either spec.customizations.certificate.arn "+
						"or spec.managedCertificateRequest to cover its domain(s)")
			}
		}

		// 2. Managed cert validationTokenHost="cloudfront" advisory:
		// When using cloudfront-hosted validation, the domain must already be
		// pointed to CloudFront. We can't verify DNS, but we set a clear message.
		if tenant.Spec.ManagedCertificateRequest != nil &&
			tenant.Spec.ManagedCertificateRequest.ValidationTokenHost == "cloudfront" {
			log.Info("Managed certificate uses cloudfront validation: domain(s) must already have DNS CNAME pointing to CloudFront",
				"domains", domainsToStrings(tenant.Spec.Domains))
		}

		// 3. Required parameters check:
		// Any parameter marked as required in the distribution (without a default
		// value) must be provided by the tenant.
		if len(distInfo.ParameterDefinitions) > 0 {
			tenantParams := make(map[string]string, len(tenant.Spec.Parameters))
			for _, p := range tenant.Spec.Parameters {
				tenantParams[p.Name] = p.Value
			}

			var missingParams []string
			for _, pd := range distInfo.ParameterDefinitions {
				if pd.Required && pd.DefaultValue == "" {
					if _, ok := tenantParams[pd.Name]; !ok {
						missingParams = append(missingParams, pd.Name)
					}
				}
			}
			if len(missingParams) > 0 {
				validationErrors = append(validationErrors,
					fmt.Sprintf("the parent distribution requires the following parameter(s) that are not provided: %s",
						strings.Join(missingParams, ", ")))
			}
		}
	}

	if len(validationErrors) > 0 {
		msg := fmt.Sprintf("Spec validation failed: %s", strings.Join(validationErrors, "; "))
		log.Info("Spec validation failed", "errors", validationErrors)
		r.recordEvent(tenant, "Warning", cloudfrontv1alpha1.ReasonValidationFailed, msg)
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonValidationFailed, msg)
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeSynced, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonValidationFailed, msg)
		if statusErr := r.Status().Update(ctx, tenant); statusErr != nil {
			log.Error(statusErr, "Failed to update status after validation failure")
			return ctrl.Result{}, statusErr, true
		}
		// Don't requeue aggressively; the user needs to fix the spec.
		return ctrl.Result{RequeueAfter: requeueLong}, nil, true
	}

	return ctrl.Result{}, nil, false
}

// updateCertificateCondition sets the CertificateReady condition based on
// the managed certificate details and domain results from AWS.
func updateCertificateCondition(tenant *cloudfrontv1alpha1.DistributionTenant, certDetails *cfaws.ManagedCertificateDetailsOutput) {
	if tenant.Spec.ManagedCertificateRequest == nil {
		// No managed cert configured -- set condition to indicate this
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionTrue,
			cloudfrontv1alpha1.ReasonCertNotConfigured, "No managed certificate request configured")
		return
	}

	// Use managed certificate details if available for a more accurate status
	if certDetails != nil {
		switch certDetails.CertificateStatus {
		case "issued":
			if allDomainsActive(tenant) {
				setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionTrue,
					cloudfrontv1alpha1.ReasonCertValidated,
					fmt.Sprintf("Managed certificate %s is validated and active", certDetails.CertificateArn))
			} else {
				setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
					cloudfrontv1alpha1.ReasonCertAttaching,
					fmt.Sprintf("Managed certificate %s is issued, attaching to domains", certDetails.CertificateArn))
			}
		case "pending-validation":
			msg := "Managed certificate validation is pending"
			if certDetails.ValidationTokenHost == "self-hosted" && len(certDetails.ValidationTokenDetails) > 0 {
				// Include validation token details to help the user
				var tokenInfo []string
				for _, vtd := range certDetails.ValidationTokenDetails {
					if vtd.RedirectFrom != "" && vtd.RedirectTo != "" {
						tokenInfo = append(tokenInfo, fmt.Sprintf("%s: redirect %s -> %s",
							vtd.Domain, vtd.RedirectFrom, vtd.RedirectTo))
					}
				}
				if len(tokenInfo) > 0 {
					msg += "; serve these HTTP validation tokens: " + strings.Join(tokenInfo, "; ")
				}
			} else if certDetails.ValidationTokenHost == "cloudfront" {
				msg += "; ensure DNS CNAME records point to CloudFront"
			}
			setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
				cloudfrontv1alpha1.ReasonCertPending, msg)
		case "failed", "validation-timed-out", "revoked", "expired":
			setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
				cloudfrontv1alpha1.ReasonCertFailed,
				fmt.Sprintf("Managed certificate has status %q; you may need to recreate the tenant or check DNS configuration",
					certDetails.CertificateStatus))
		default:
			setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
				cloudfrontv1alpha1.ReasonCertPending,
				fmt.Sprintf("Managed certificate status: %s", certDetails.CertificateStatus))
		}
		return
	}

	// Fallback: use domain results if cert details are not available
	if allDomainsActive(tenant) {
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionTrue,
			cloudfrontv1alpha1.ReasonCertValidated, "Managed certificate is validated and active")
	} else {
		setCondition(tenant, cloudfrontv1alpha1.ConditionTypeCertificateReady, metav1.ConditionFalse,
			cloudfrontv1alpha1.ReasonCertPending, "Managed certificate validation is pending; ensure DNS CNAME records are configured")
	}
}

// allDomainsActive returns true if all domain results are "active".
func allDomainsActive(tenant *cloudfrontv1alpha1.DistributionTenant) bool {
	if len(tenant.Status.DomainResults) == 0 {
		return false
	}
	for _, dr := range tenant.Status.DomainResults {
		if dr.Status != "active" {
			return false
		}
	}
	return true
}

// domainsToStrings extracts domain strings from a DomainSpec slice.
func domainsToStrings(domains []cloudfrontv1alpha1.DomainSpec) []string {
	out := make([]string, len(domains))
	for i, d := range domains {
		out[i] = d.Domain
	}
	return out
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
		Name:           tenant.Name,
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
