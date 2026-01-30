package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	connectivityv1alpha1 "github.com/fredericrous/homelab/ddns-updater-operator/api/v1alpha1"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/assembler"
	"github.com/fredericrous/homelab/ddns-updater-operator/pkg/config"
	operrors "github.com/fredericrous/homelab/ddns-updater-operator/pkg/errors"
)

// DDNSRecordReconciler reconciles DDNSRecord objects
type DDNSRecordReconciler struct {
	client.Client
	Log       logr.Logger
	Scheme    *runtime.Scheme
	Recorder  record.EventRecorder
	Config    *config.OperatorConfig
	Assembler *assembler.Assembler
}

// SetupWithManager sets up the controller with the Manager
func (r *DDNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Config == nil {
		r.Config = config.NewDefaultConfig()
	}

	r.Assembler = assembler.NewAssembler(r.Client, r.Log.WithName("assembler"))

	opts := controller.Options{
		MaxConcurrentReconciles: r.Config.MaxConcurrentReconciles,
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&connectivityv1alpha1.DDNSRecord{}).
		// Watch Secrets for credential changes
		Watches(
			&corev1.Secret{},
			r.enqueueRequestsForSecret(),
			builder.WithPredicates(
				predicate.ResourceVersionChangedPredicate{},
			),
		).
		WithOptions(opts).
		Complete(r)
}

// Reconcile handles the reconciliation loop
// +kubebuilder:rbac:groups=connectivity.homelab.io,resources=ddnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=connectivity.homelab.io,resources=ddnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;create;update

func (r *DDNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := r.Log.WithValues("ddnsrecord", req.NamespacedName, "trace_id", generateTraceID())

	// Apply reconcile timeout
	ctx, cancel := context.WithTimeout(ctx, r.Config.ReconcileTimeout)
	defer cancel()

	ctx = logr.NewContext(ctx, log)

	log.V(1).Info("Starting reconciliation")

	// Fetch all DDNSRecords cluster-wide
	recordList := &connectivityv1alpha1.DDNSRecordList{}
	if err := r.List(ctx, recordList); err != nil {
		return ctrl.Result{}, operrors.NewTransientError("failed to list DDNSRecords", err)
	}

	if len(recordList.Items) == 0 {
		log.Info("No DDNSRecord resources found, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	// Assemble the configuration
	result, err := r.Assembler.Assemble(ctx, recordList.Items)
	if err != nil {
		r.Recorder.Event(&recordList.Items[0], corev1.EventTypeWarning, "AssemblyFailed", err.Error())
		if operrors.ShouldRetry(err) {
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Update the ddns-updater ConfigMap
	if err := r.updateDDNSConfig(ctx, result); err != nil {
		r.Recorder.Eventf(&recordList.Items[0], corev1.EventTypeWarning, "ConfigUpdateFailed", "Failed to update ddns-updater config: %v", err)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	// Update status for all DDNSRecords
	now := metav1.Now()
	var statusUpdateErrors []error
	for i := range recordList.Items {
		record := &recordList.Items[i]
		record.Status.Ready = true
		record.Status.LastSyncedAt = &now
		if err := r.Status().Update(ctx, record); err != nil {
			log.Error(err, "Failed to update DDNSRecord status", "record", record.Name)
			statusUpdateErrors = append(statusUpdateErrors, err)
		}
	}

	// If any status updates failed, requeue to retry
	if len(statusUpdateErrors) > 0 {
		log.Info("Some status updates failed, requeueing", "failedCount", len(statusUpdateErrors))
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	log.Info("Reconciliation completed successfully", "recordCount", len(recordList.Items))

	r.Recorder.Event(&recordList.Items[0], corev1.EventTypeNormal, "Synced",
		fmt.Sprintf("Successfully assembled %d DDNS records", len(recordList.Items)))

	return ctrl.Result{}, nil
}

// updateDDNSConfig updates the ddns-updater ConfigMap
func (r *DDNSRecordReconciler) updateDDNSConfig(ctx context.Context, result *assembler.AssemblyResult) error {
	log := logr.FromContextOrDiscard(ctx)

	// Compute hash of the config
	configHash := computeHash(result.ConfigJSON)

	existing := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: r.Config.DDNSConfigMapName, Namespace: r.Config.DDNSNamespace}, existing)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new ConfigMap
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      r.Config.DDNSConfigMapName,
					Namespace: r.Config.DDNSNamespace,
					Labels: map[string]string{
						"app.kubernetes.io/managed-by": "ddns-updater-operator",
					},
					Annotations: map[string]string{
						"ddns.homelab.io/config-hash": configHash,
					},
				},
				Data: map[string]string{
					"config.json": result.ConfigJSON,
				},
			}
			log.Info("Creating ddns-updater ConfigMap", "name", r.Config.DDNSConfigMapName)
			return r.Create(ctx, cm)
		}
		return err
	}

	// Check if the config hash has changed
	existingHash := ""
	if existing.Annotations != nil {
		existingHash = existing.Annotations["ddns.homelab.io/config-hash"]
	}

	if existingHash == configHash {
		log.V(1).Info("ddns-updater ConfigMap unchanged (hash match), skipping update")
		return nil
	}

	// Update ConfigMap
	existing.Data = map[string]string{
		"config.json": result.ConfigJSON,
	}
	if existing.Labels == nil {
		existing.Labels = make(map[string]string)
	}
	existing.Labels["app.kubernetes.io/managed-by"] = "ddns-updater-operator"
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	existing.Annotations["ddns.homelab.io/config-hash"] = configHash

	log.Info("Updating ddns-updater ConfigMap", "name", r.Config.DDNSConfigMapName, "hash", configHash)
	return r.Update(ctx, existing)
}

// computeHash computes a SHA256 hash of the given string
func computeHash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// enqueueRequestsForSecret returns a handler that enqueues DDNSRecord objects
// when referenced secrets change
func (r *DDNSRecordReconciler) enqueueRequestsForSecret() handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []reconcile.Request {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return nil
		}

		// List all DDNSRecords
		recordList := &connectivityv1alpha1.DDNSRecordList{}
		if err := r.List(ctx, recordList); err != nil {
			r.Log.Error(err, "Failed to list DDNSRecords")
			return nil
		}

		var requests []reconcile.Request
		for _, record := range recordList.Items {
			// Check if this DDNSRecord references the Secret
			ref := &record.Spec.ProviderConfig.CredentialsRef
			namespace := ref.Namespace
			if namespace == "" {
				namespace = record.Namespace
			}

			if secret.Name == ref.Name && secret.Namespace == namespace {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      record.Name,
						Namespace: record.Namespace,
					},
				})
			}
		}

		if len(requests) > 0 {
			r.Log.V(1).Info("Enqueuing DDNSRecords due to Secret change",
				"secret", types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace},
				"count", len(requests))
		}

		return requests
	})
}

func generateTraceID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
