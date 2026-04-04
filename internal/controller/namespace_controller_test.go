package controller

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"
	ctrl "sigs.k8s.io/controller-runtime"
)

func TestNamespaceReconcile_MissingOneLabel(t *testing.T) {
	// Namespace with only pdb-min-available label (missing pdb-max-unavailable)
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			Labels: map[string]string{
				"pdb-min-available": "1",
			},
		},
	}

	scheme := setupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns).Build()
	r := &NamespaceReconciler{
		Client: c,
		Log:    log.Log,
	}

	ctx := context.Background()
	result, err := r.Reconcile(ctx, reconcileRequest("test-ns"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify no objects were modified
	updated := &corev1.Namespace{}
	err = c.Get(ctx, client.ObjectKeyFromObject(ns), updated)
	assert.NoError(t, err)
	// Labels should be unchanged
	assert.Equal(t, 1, len(updated.Labels))
}

func TestNamespaceReconcile_DeploymentWithoutPDB(t *testing.T) {
	// Setup
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			Labels: map[string]string{
				"pdb-min-available":    "1",
				"pdb-max-unavailable": "1",
			},
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deploy",
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}

	scheme := setupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, deployment).Build()
	r := &NamespaceReconciler{
		Client: c,
		Log:    log.Log,
	}

	ctx := context.Background()
	result, err := r.Reconcile(ctx, reconcileRequest("test-ns"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify deployment pod template annotation was set
	updated := &appsv1.Deployment{}
	err = c.Get(ctx, client.ObjectKeyFromObject(deployment), updated)
	assert.NoError(t, err)
	assert.NotNil(t, updated.Spec.Template.Annotations)
	_, hasRestart := updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.True(t, hasRestart, "expected restartedAt annotation")
}

func TestNamespaceReconcile_DeploymentWithPDB(t *testing.T) {
	// Setup with existing PDB
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			Labels: map[string]string{
				"pdb-min-available":    "1",
				"pdb-max-unavailable": "1",
			},
		},
	}

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deploy",
			Namespace: "test-ns",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
		},
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deploy",
			Namespace: "test-ns",
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable: &intstr.IntOrString{Type: intstr.Int, IntVal: 1},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
		},
	}

	scheme := setupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, deployment, pdb).Build()
	r := &NamespaceReconciler{
		Client: c,
		Log:    log.Log,
	}

	ctx := context.Background()
	result, err := r.Reconcile(ctx, reconcileRequest("test-ns"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify deployment was NOT modified
	updated := &appsv1.Deployment{}
	err = c.Get(ctx, client.ObjectKeyFromObject(deployment), updated)
	assert.NoError(t, err)
	// No annotations should be added
	_, hasRestart := updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.False(t, hasRestart, "deployment should not have been restarted")
}

func TestNamespaceReconcile_StatefulSetWithoutPDB(t *testing.T) {
	// Setup
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-ns",
			Labels: map[string]string{
				"pdb-min-available":    "1",
				"pdb-max-unavailable": "1",
			},
		},
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sts",
			Namespace: "test-ns",
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test-sts"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test-sts"},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx:latest",
						},
					},
				},
			},
			ServiceName: "test-sts",
		},
	}

	scheme := setupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ns, sts).Build()
	r := &NamespaceReconciler{
		Client: c,
		Log:    log.Log,
	}

	ctx := context.Background()
	result, err := r.Reconcile(ctx, reconcileRequest("test-ns"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)

	// Verify statefulset pod template annotation was set
	updated := &appsv1.StatefulSet{}
	err = c.Get(ctx, client.ObjectKeyFromObject(sts), updated)
	assert.NoError(t, err)
	assert.NotNil(t, updated.Spec.Template.Annotations)
	_, hasRestart := updated.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"]
	assert.True(t, hasRestart, "expected restartedAt annotation")
}

func TestNamespaceReconcile_NotFound(t *testing.T) {
	scheme := setupScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &NamespaceReconciler{
		Client: c,
		Log:    log.Log,
	}

	ctx := context.Background()
	result, err := r.Reconcile(ctx, reconcileRequest("non-existent"))

	assert.NoError(t, err)
	assert.Equal(t, ctrl.Result{}, result)
}

// Helper functions

func setupScheme(t *testing.T) *runtime.Scheme {
	scheme := runtime.NewScheme()
	assert.NoError(t, corev1.AddToScheme(scheme))
	assert.NoError(t, appsv1.AddToScheme(scheme))
	assert.NoError(t, policyv1.AddToScheme(scheme))
	return scheme
}

func reconcileRequest(name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name: name,
		},
	}
}

func int32Ptr(i int32) *int32 {
	return &i
}
