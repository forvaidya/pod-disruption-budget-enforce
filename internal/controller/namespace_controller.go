package controller

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

// NamespaceReconciler watches Namespace events and triggers rolling restarts
// when PDB configuration labels are added.
type NamespaceReconciler struct {
	client.Client
	Log logr.Logger
}

// Reconcile handles namespace changes and triggers rolling restarts on workloads
// that lack PDBs when PDB configuration labels are activated.
func (r *NamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	// Fetch namespace
	ns := &corev1.Namespace{}
	err := r.Get(ctx, req.NamespacedName, ns)
	if err != nil {
		// Namespace deleted, no action needed
		r.Log.Info("namespace not found, skipping", "namespace", req.Name)
		return ctrl.Result{}, nil
	}

	// Check both labels present
	if !nsHasBothLabels(ns) {
		r.Log.Info("namespace missing one or both PDB config labels, skipping",
			"namespace", ns.Name)
		return ctrl.Result{}, nil
	}

	minVal, _ := ns.Labels["pdb-min-available"]
	maxVal, _ := ns.Labels["pdb-max-unavailable"]
	r.Log.Info("processing namespace with PDB config labels",
		"namespace", ns.Name,
		"pdb-min-available", minVal,
		"pdb-max-unavailable", maxVal)

	// List Deployments in namespace
	deploymentList := &appsv1.DeploymentList{}
	if err := r.List(ctx, deploymentList, client.InNamespace(ns.Name)); err != nil {
		r.Log.Error(err, "failed to list deployments", "namespace", ns.Name)
		return ctrl.Result{}, err
	}

	// List StatefulSets in namespace
	statefulSetList := &appsv1.StatefulSetList{}
	if err := r.List(ctx, statefulSetList, client.InNamespace(ns.Name)); err != nil {
		r.Log.Error(err, "failed to list statefulsets", "namespace", ns.Name)
		return ctrl.Result{}, err
	}

	// Process Deployments
	for i := range deploymentList.Items {
		deployment := &deploymentList.Items[i]
		if err := r.processDeployment(ctx, ns, deployment); err != nil {
			r.Log.Error(err, "failed to process deployment",
				"deployment", deployment.Name,
				"namespace", ns.Name)
			// Continue processing others on error
		}
	}

	// Process StatefulSets
	for i := range statefulSetList.Items {
		statefulSet := &statefulSetList.Items[i]
		if err := r.processStatefulSet(ctx, ns, statefulSet); err != nil {
			r.Log.Error(err, "failed to process statefulset",
				"statefulset", statefulSet.Name,
				"namespace", ns.Name)
			// Continue processing others on error
		}
	}

	return ctrl.Result{}, nil
}

// processDeployment checks if a Deployment has a matching PDB, and if not,
// triggers a rolling restart by updating the pod template annotation.
func (r *NamespaceReconciler) processDeployment(ctx context.Context, ns *corev1.Namespace, deployment *appsv1.Deployment) error {
	podLabels := deployment.Spec.Template.Labels

	// Check if PDB exists for this deployment
	pdbExists, err := hasPDB(ctx, r.Client, ns.Name, podLabels)
	if err != nil {
		return err
	}

	if pdbExists {
		r.Log.Info("deployment already has matching PDB, skipping",
			"deployment", deployment.Name,
			"namespace", ns.Name,
			"action", "skip")
		return nil
	}

	// No PDB exists - trigger rolling restart by patching pod template annotation
	r.Log.Info("triggering rolling restart for deployment",
		"deployment", deployment.Name,
		"namespace", ns.Name,
		"action", "rolling-restart",
		"reason", "no-matching-pdb")

	base := deployment.DeepCopy()
	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)

	if err := r.Patch(ctx, deployment, client.MergeFrom(base)); err != nil {
		return err
	}

	r.Log.Info("rolled out deployment",
		"deployment", deployment.Name,
		"namespace", ns.Name)
	return nil
}

// processStatefulSet checks if a StatefulSet has a matching PDB, and if not,
// triggers a rolling restart by updating the pod template annotation.
func (r *NamespaceReconciler) processStatefulSet(ctx context.Context, ns *corev1.Namespace, statefulSet *appsv1.StatefulSet) error {
	podLabels := statefulSet.Spec.Template.Labels

	// Check if PDB exists for this statefulset
	pdbExists, err := hasPDB(ctx, r.Client, ns.Name, podLabels)
	if err != nil {
		return err
	}

	if pdbExists {
		r.Log.Info("statefulset already has matching PDB, skipping",
			"statefulset", statefulSet.Name,
			"namespace", ns.Name,
			"action", "skip")
		return nil
	}

	// No PDB exists - trigger rolling restart by patching pod template annotation
	r.Log.Info("triggering rolling restart for statefulset",
		"statefulset", statefulSet.Name,
		"namespace", ns.Name,
		"action", "rolling-restart",
		"reason", "no-matching-pdb")

	base := statefulSet.DeepCopy()
	if statefulSet.Spec.Template.Annotations == nil {
		statefulSet.Spec.Template.Annotations = map[string]string{}
	}
	statefulSet.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().UTC().Format(time.RFC3339)

	if err := r.Patch(ctx, statefulSet, client.MergeFrom(base)); err != nil {
		return err
	}

	r.Log.Info("rolled out statefulset",
		"statefulset", statefulSet.Name,
		"namespace", ns.Name)
	return nil
}

// SetupWithManager sets up the controller with the given manager,
// including a predicate that only fires on inactive→active label transitions.
func (r *NamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Namespace{}).
		WithEventFilter(predicate.Funcs{
			CreateFunc: func(e event.CreateEvent) bool {
				return nsHasBothLabels(e.Object)
			},
			UpdateFunc: func(e event.UpdateEvent) bool {
				// Only fire when labels transition from inactive to active
				return !nsHasBothLabels(e.ObjectOld) && nsHasBothLabels(e.ObjectNew)
			},
			DeleteFunc: func(_ event.DeleteEvent) bool {
				return false
			},
			GenericFunc: func(_ event.GenericEvent) bool {
				return false
			},
		}).
		Complete(r)
}

// nsHasBothLabels checks if a namespace object has both PDB config labels present.
func nsHasBothLabels(obj client.Object) bool {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return false
	}
	if ns == nil || ns.Labels == nil {
		return false
	}
	_, hasMin := ns.Labels["pdb-min-available"]
	_, hasMax := ns.Labels["pdb-max-unavailable"]
	return hasMin && hasMax
}

// hasPDB checks if any PDB in the namespace matches the given pod labels.
func hasPDB(ctx context.Context, c client.Client, namespace string, podLabels map[string]string) (bool, error) {
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := c.List(ctx, pdbList, client.InNamespace(namespace)); err != nil {
		return false, err
	}

	// Check each PDB to see if it selects this workload's pods
	for _, pdb := range pdbList.Items {
		if pdb.Spec.Selector == nil {
			continue
		}

		// Convert selector to label matcher
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			// Skip this PDB and check others
			continue
		}

		// Check if this PDB selects the workload's pod template labels
		if selector.Matches(labels.Set(podLabels)) {
			return true, nil
		}
	}

	return false, nil
}
