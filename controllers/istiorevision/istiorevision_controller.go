/*
Copyright 2023.

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

package istiorevision

import (
	"context"
	"fmt"
	"reflect"
	"regexp"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"maistra.io/istio-operator/api/v1alpha1"
	"maistra.io/istio-operator/pkg/helm"
	"maistra.io/istio-operator/pkg/kube"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	networkingv1alpha3 "istio.io/client-go/pkg/apis/networking/v1alpha3"
	"istio.io/istio/pkg/ptr"
)

// IstioRevisionReconciler reconciles an IstioRevision object
type IstioRevisionReconciler struct {
	RestClientGetter genericclioptions.RESTClientGetter
	client.Client
	Scheme *runtime.Scheme
}

func NewIstioRevisionReconciler(client client.Client, scheme *runtime.Scheme, restConfig *rest.Config) *IstioRevisionReconciler {
	return &IstioRevisionReconciler{
		RestClientGetter: helm.NewRESTClientGetter(restConfig),
		Client:           client,
		Scheme:           scheme,
	}
}

// charts to deploy in the istio namespace
var userCharts = []string{"base", "istiod"}

// +kubebuilder:rbac:groups=operator.istio.io,resources=istiorevisions,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.istio.io,resources=istiorevisions/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=operator.istio.io,resources=istiorevisions/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources="*",verbs="*"
// +kubebuilder:rbac:groups="networking.k8s.io",resources="networkpolicies",verbs="*"
// +kubebuilder:rbac:groups="policy",resources="poddisruptionbudgets",verbs="*"
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=clusterroles;clusterrolebindings;roles;rolebindings,verbs="*"
// +kubebuilder:rbac:groups="apps",resources=deployments;daemonsets,verbs="*"
// +kubebuilder:rbac:groups="admissionregistration.k8s.io",resources=validatingwebhookconfigurations;mutatingwebhookconfigurations,verbs="*"
// +kubebuilder:rbac:groups="autoscaling",resources=horizontalpodautoscalers,verbs="*"
// +kubebuilder:rbac:groups="apiextensions.k8s.io",resources=customresourcedefinitions,verbs=get;list;watch
// +kubebuilder:rbac:groups="k8s.cni.cncf.io",resources=network-attachment-definitions,verbs="*"
// +kubebuilder:rbac:groups="security.openshift.io",resources=securitycontextconstraints,resourceNames=privileged,verbs=use
// +kubebuilder:rbac:groups="networking.istio.io",resources=envoyfilters,verbs="*"

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.1/pkg/reconcile
func (r *IstioRevisionReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx).WithName("reconciler")
	var rev v1alpha1.IstioRevision
	if err := r.Client.Get(ctx, req.NamespacedName, &rev); err != nil {
		if errors.IsNotFound(err) {
			logger.V(2).Info("IstioRevision not found. Skipping reconciliation")
			return ctrl.Result{}, nil
		}
		logger.Error(err, "failed to get IstioRevision from cluster")
	}

	if rev.DeletionTimestamp != nil {
		if err := r.uninstallHelmCharts(&rev); err != nil {
			return ctrl.Result{}, err
		}

		if err := kube.RemoveFinalizer(ctx, &rev, r.Client); err != nil {
			logger.Info("failed to remove finalizer")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if rev.Spec.Version == "" {
		return ctrl.Result{}, fmt.Errorf("no spec.version set")
	}

	if !kube.HasFinalizer(&rev) {
		err := kube.AddFinalizer(ctx, &rev, r.Client)
		if err != nil {
			logger.Info("failed to add finalizer")
			return ctrl.Result{}, err
		}
	}

	values, err := applyOverridesToIstioRevision(&rev, rev.Spec.GetValues())
	if err != nil {
		err = r.updateStatus(ctx, logger, &rev, values, err)
		return ctrl.Result{}, err
	}

	logger.Info("Installing components", "values", values)
	err = r.installHelmCharts(ctx, &rev, values)

	logger.Info("Reconciliation done. Updating status.")
	err = r.updateStatus(ctx, logger, &rev, values, err)

	return ctrl.Result{}, err
}

func applyOverridesToIstioRevision(rev *v1alpha1.IstioRevision, values helm.HelmValues) (helm.HelmValues, error) {
	if err := values.Set("global.istioNamespace", rev.Namespace); err != nil {
		return nil, err
	}
	return values, nil
}

func (r *IstioRevisionReconciler) installHelmCharts(ctx context.Context, rev *v1alpha1.IstioRevision, values helm.HelmValues) error {
	ownerReference := metav1.OwnerReference{
		APIVersion:         v1alpha1.GroupVersion.String(),
		Kind:               v1alpha1.IstioRevisionKind,
		Name:               rev.Name,
		UID:                rev.UID,
		Controller:         ptr.Of(true),
		BlockOwnerDeletion: ptr.Of(true),
	}

	if cniEnabled, err := isCNIEnabled(values); err != nil {
		return err
	} else if cniEnabled {
		if err := helm.UpgradeOrInstallCharts(ctx, r.RestClientGetter, []string{"cni"}, values,
			rev.Spec.Version, rev.Name, rev.Namespace, ownerReference); err != nil {
			return err
		}
	}

	if err := helm.UpgradeOrInstallCharts(ctx, r.RestClientGetter, userCharts, values,
		rev.Spec.Version, rev.Name, rev.Namespace, ownerReference); err != nil {
		return err
	}
	return nil
}

func (r *IstioRevisionReconciler) uninstallHelmCharts(rev *v1alpha1.IstioRevision) error {
	if err := helm.UninstallCharts(r.RestClientGetter, []string{"cni"}, rev.Name, rev.Namespace); err != nil {
		return err
	}

	if err := helm.UninstallCharts(r.RestClientGetter, userCharts, rev.Name, rev.Namespace); err != nil {
		return err
	}
	return nil
}

func isCNIEnabled(values helm.HelmValues) (bool, error) {
	enabled, _, err := values.GetBool("istio_cni.enabled")
	return enabled, err
}

// SetupWithManager sets up the controller with the Manager.
func (r *IstioRevisionReconciler) SetupWithManager(mgr ctrl.Manager) error {
	clusterScopedResourceHandler := handler.EnqueueRequestsFromMapFunc(mapOwnerAnnotationsToReconcileRequest)

	return ctrl.NewControllerManagedBy(mgr).
		For(&v1alpha1.IstioRevision{}).

		// namespaced resources
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&appsv1.DaemonSet{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.ResourceQuota{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Owns(&networkingv1alpha3.EnvoyFilter{}).

		// TODO: only register NetAttachDef if the CRD is installed (may also need to watch for CRD creation)
		// Owns(&multusv1.NetworkAttachmentDefinition{}).

		// cluster-scoped resources
		Watches(&rbacv1.ClusterRole{}, clusterScopedResourceHandler).
		Watches(&rbacv1.ClusterRoleBinding{}, clusterScopedResourceHandler).
		Watches(&admissionv1.MutatingWebhookConfiguration{}, clusterScopedResourceHandler).
		Watches(&admissionv1.ValidatingWebhookConfiguration{},
			clusterScopedResourceHandler,
			builder.WithPredicates(validatingWebhookConfigPredicate{})).

		// +lint-watches:ignore: CustomResourceDefinition (prevents `make lint-watches` from bugging us about CRDs)
		Complete(r)
}

func (r *IstioRevisionReconciler) updateStatus(ctx context.Context, log logr.Logger, rev *v1alpha1.IstioRevision, values helm.HelmValues, err error) error {
	reconciledCondition := r.determineReconciledCondition(err)
	readyCondition, err := r.determineReadyCondition(ctx, rev, values)
	if err != nil {
		return err
	}

	status := rev.Status.DeepCopy()
	status.ObservedGeneration = rev.Generation
	status.SetCondition(reconciledCondition)
	status.SetCondition(readyCondition)
	status.State = deriveState(reconciledCondition, readyCondition)

	if reflect.DeepEqual(rev.Status, *status) {
		return nil
	}

	statusErr := r.Client.Status().Patch(ctx, rev, kube.NewStatusPatch(*status))
	if statusErr != nil {
		log.Error(statusErr, "failed to patch status")

		// ensure that we retry the reconcile by returning the status error
		// (but without overriding the original error)
		if err == nil {
			return statusErr
		}
	}
	return err
}

func deriveState(reconciledCondition, readyCondition v1alpha1.IstioRevisionCondition) v1alpha1.IstioRevisionConditionReason {
	if reconciledCondition.Status == metav1.ConditionFalse {
		return reconciledCondition.Reason
	} else if readyCondition.Status == metav1.ConditionFalse {
		return readyCondition.Reason
	}

	return v1alpha1.IstioRevisionConditionReasonHealthy
}

func (r *IstioRevisionReconciler) determineReconciledCondition(err error) v1alpha1.IstioRevisionCondition {
	if err == nil {
		return v1alpha1.IstioRevisionCondition{
			Type:   v1alpha1.IstioRevisionConditionTypeReconciled,
			Status: metav1.ConditionTrue,
		}
	}

	return v1alpha1.IstioRevisionCondition{
		Type:    v1alpha1.IstioRevisionConditionTypeReconciled,
		Status:  metav1.ConditionFalse,
		Reason:  v1alpha1.IstioRevisionConditionReasonReconcileError,
		Message: fmt.Sprintf("error reconciling resource: %v", err),
	}
}

func (r *IstioRevisionReconciler) determineReadyCondition(ctx context.Context,
	rev *v1alpha1.IstioRevision, values helm.HelmValues,
) (v1alpha1.IstioRevisionCondition, error) {
	notReady := func(reason v1alpha1.IstioRevisionConditionReason, message string) v1alpha1.IstioRevisionCondition {
		return v1alpha1.IstioRevisionCondition{
			Type:    v1alpha1.IstioRevisionConditionTypeReady,
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		}
	}

	if istiodKey, err := istiodDeploymentKey(rev, values); err == nil {
		istiod := appsv1.Deployment{}
		if err := r.Client.Get(ctx, istiodKey, &istiod); err != nil {
			if errors.IsNotFound(err) {
				return notReady(v1alpha1.IstioRevisionConditionReasonIstiodNotReady, "istiod Deployment not found"), nil
			}
			return notReady(v1alpha1.IstioRevisionConditionReasonReconcileError, fmt.Sprintf("failed to get readiness: %v", err)), nil
		}

		if istiod.Status.Replicas == 0 {
			return notReady(v1alpha1.IstioRevisionConditionReasonIstiodNotReady, "istiod Deployment is scaled to zero replicas"), nil
		} else if istiod.Status.ReadyReplicas < istiod.Status.Replicas {
			return notReady(v1alpha1.IstioRevisionConditionReasonIstiodNotReady, "not all istiod pods are ready"), nil
		}
	} else {
		return v1alpha1.IstioRevisionCondition{}, err
	}

	if cniEnabled, err := isCNIEnabled(values); err != nil {
		return v1alpha1.IstioRevisionCondition{}, err
	} else if cniEnabled {
		cni := appsv1.DaemonSet{}
		if err := r.Client.Get(ctx, cniDaemonSetKey(rev), &cni); err != nil {
			if errors.IsNotFound(err) {
				return notReady(v1alpha1.IstioRevisionConditionReasonCNINotReady, "istio-cni-node DaemonSet not found"), nil
			}
			return notReady(v1alpha1.IstioRevisionConditionReasonReconcileError, fmt.Sprintf("failed to get readiness: %v", err)), nil
		}

		if cni.Status.CurrentNumberScheduled == 0 {
			return notReady(v1alpha1.IstioRevisionConditionReasonCNINotReady, "no istio-cni-node pods are currently scheduled"), nil
		} else if cni.Status.NumberReady < cni.Status.CurrentNumberScheduled {
			return notReady(v1alpha1.IstioRevisionConditionReasonCNINotReady, "not all istio-cni-node pods are ready"), nil
		}
	}

	return v1alpha1.IstioRevisionCondition{
		Type:   v1alpha1.IstioRevisionConditionTypeReady,
		Status: metav1.ConditionTrue,
	}, nil
}

func cniDaemonSetKey(rev *v1alpha1.IstioRevision) client.ObjectKey {
	return client.ObjectKey{
		Namespace: rev.Namespace,
		Name:      "istio-cni-node",
	}
}

func istiodDeploymentKey(rev *v1alpha1.IstioRevision, values helm.HelmValues) (client.ObjectKey, error) {
	revision, _, err := values.GetString("revision")
	if err != nil {
		return client.ObjectKey{}, err
	}

	name := "istiod"
	if revision != "" {
		name += "-" + revision
	}

	return client.ObjectKey{
		Namespace: rev.Namespace,
		Name:      name,
	}, nil
}

func mapOwnerAnnotationsToReconcileRequest(ctx context.Context, obj client.Object) []reconcile.Request {
	annotations := obj.GetAnnotations()
	if annotations == nil {
		return nil
	}

	namespacedName, kind, apiGroup := helm.GetOwnerFromAnnotations(annotations)
	if namespacedName != nil && kind == v1alpha1.IstioRevisionKind && apiGroup == v1alpha1.GroupVersion.Group {
		return []reconcile.Request{{NamespacedName: *namespacedName}}
	}
	return nil
}

type validatingWebhookConfigPredicate struct {
	predicate.Funcs
}

func (v validatingWebhookConfigPredicate) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil || e.ObjectNew == nil {
		return false
	}

	if matched, _ := regexp.MatchString("istiod-.*-validator|istio-validator.*", e.ObjectNew.GetName()); matched {
		// Istiod updates the caBundle and failurePolicy fields in istiod-<ns>-validator and istio-validator[-<rev>]-<ns>
		// webhook configs. We must ignore changes to these fields to prevent an endless update loop.
		clearIgnoredFields(e.ObjectOld)
		clearIgnoredFields(e.ObjectNew)
		return !reflect.DeepEqual(e.ObjectNew, e.ObjectOld)
	}
	return true
}

func clearIgnoredFields(obj client.Object) {
	obj.SetResourceVersion("")
	obj.SetGeneration(0)
	obj.SetManagedFields(nil)
	if webhookConfig, ok := obj.(*admissionv1.ValidatingWebhookConfiguration); ok {
		for i := 0; i < len(webhookConfig.Webhooks); i++ {
			webhookConfig.Webhooks[i].FailurePolicy = nil
		}
	}
}
