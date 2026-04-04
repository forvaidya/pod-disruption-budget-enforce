package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/zapr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGetNamespacePDBLabels(t *testing.T) {
	zaplog, _ := zap.NewProduction()
	logger := zapr.NewLogger(zaplog)

	tests := []struct {
		name         string
		namespace    *corev1.Namespace
		wantConfig   bool
		wantError    string
		wantMinVal   int32
		wantMaxVal   int32
	}{
		{
			name: "both labels present",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"pdb-min-available":    "2",
						"pdb-max-unavailable":  "1",
					},
				},
			},
			wantConfig: true,
			wantError:  "",
			wantMinVal: 2,
			wantMaxVal: 1,
		},
		{
			name: "only min label present",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"pdb-min-available": "2",
					},
				},
			},
			wantConfig: false,
			wantError:  "incomplete PDB configuration",
		},
		{
			name: "only max label present",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"pdb-max-unavailable": "1",
					},
				},
			},
			wantConfig: false,
			wantError:  "incomplete PDB configuration",
		},
		{
			name: "no labels present",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test",
					Labels: map[string]string{},
				},
			},
			wantConfig: false,
			wantError:  "",
		},
		{
			name: "invalid min value",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"pdb-min-available":    "not-a-number",
						"pdb-max-unavailable":  "1",
					},
				},
			},
			wantConfig: false,
			wantError:  "invalid pdb-min-available value",
		},
		{
			name: "invalid max value",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test",
					Labels: map[string]string{
						"pdb-min-available":    "2",
						"pdb-max-unavailable":  "not-a-number",
					},
				},
			},
			wantConfig: false,
			wantError:  "invalid pdb-max-unavailable value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			corev1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.namespace).Build()
			handler := NewMutatingHandler(client, logger)

			hasConfig, min, max, err := handler.getNamespacePDBLabels(context.Background(), "test")

			assert.Equal(t, tt.wantConfig, hasConfig)
			if tt.wantError != "" {
				assert.Contains(t, err, tt.wantError)
			} else {
				assert.Equal(t, "", err)
				if hasConfig {
					assert.Equal(t, tt.wantMinVal, min.IntVal)
					assert.Equal(t, tt.wantMaxVal, max.IntVal)
				}
			}
		})
	}
}

func TestCreatePDB(t *testing.T) {
	zaplog, _ := zap.NewProduction()
	logger := zapr.NewLogger(zaplog)

	tests := []struct {
		name           string
		deployment     *appsv1.Deployment
		minAvailable   intstr.IntOrString
		maxUnavailable intstr.IntOrString
		wantMinSet     bool
		wantMaxSet     bool
	}{
		{
			name: "minAvailable > 0 should use min",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "test"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "test"},
						},
					},
				},
			},
			minAvailable:   intstr.FromInt(2),
			maxUnavailable: intstr.FromInt(1),
			wantMinSet:     true,
			wantMaxSet:     false,
		},
		{
			name: "minAvailable = 0 should use max",
			deployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-deploy",
					Namespace: "default",
				},
				Spec: appsv1.DeploymentSpec{
					Selector: &metav1.LabelSelector{
						MatchLabels: map[string]string{"app": "test"},
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: map[string]string{"app": "test"},
						},
					},
				},
			},
			minAvailable:   intstr.FromInt(0),
			maxUnavailable: intstr.FromInt(2),
			wantMinSet:     false,
			wantMaxSet:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			corev1.AddToScheme(scheme)
			policyv1.AddToScheme(scheme)
			appsv1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			handler := NewMutatingHandler(client, logger)

			pdb, err := handler.createPDB(context.Background(), tt.deployment.Name, tt.deployment.Namespace, tt.deployment.Spec.Selector, tt.minAvailable, tt.maxUnavailable)

			require.NoError(t, err)
			assert.Equal(t, "test-deploy", pdb.Name)
			assert.Equal(t, "default", pdb.Namespace)

			if tt.wantMinSet {
				assert.NotNil(t, pdb.Spec.MinAvailable)
				assert.Equal(t, tt.minAvailable.IntVal, pdb.Spec.MinAvailable.IntVal)
				assert.Nil(t, pdb.Spec.MaxUnavailable)
			} else {
				assert.Nil(t, pdb.Spec.MinAvailable)
				assert.NotNil(t, pdb.Spec.MaxUnavailable)
				assert.Equal(t, tt.maxUnavailable.IntVal, pdb.Spec.MaxUnavailable.IntVal)
			}

			// Check labels
			assert.Equal(t, "pdb-webhook", pdb.Labels["app.kubernetes.io/name"])
			assert.Equal(t, "test-deploy", pdb.Labels["pdb-webhook.workload-name"])
		})
	}
}

func TestPDBExists(t *testing.T) {
	zaplog, _ := zap.NewProduction()
	logger := zapr.NewLogger(zaplog)

	podLabels := map[string]string{"app": "nginx"}

	minVal := intstr.FromInt(1)
	matchingPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx-pdb",
			Namespace: "default",
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "nginx"},
			},
			MinAvailable: &minVal,
		},
	}

	minVal2 := intstr.FromInt(1)
	nonMatchingPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "other-pdb",
			Namespace: "default",
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "other"},
			},
			MinAvailable: &minVal2,
		},
	}

	tests := []struct {
		name     string
		objects  []runtime.Object
		wantExists bool
	}{
		{
			name:     "matching PDB exists",
			objects:  []runtime.Object{matchingPDB},
			wantExists: true,
		},
		{
			name:     "no PDB exists",
			objects:  []runtime.Object{},
			wantExists: false,
		},
		{
			name:     "non-matching PDB exists",
			objects:  []runtime.Object{nonMatchingPDB},
			wantExists: false,
		},
		{
			name:     "both matching and non-matching exist",
			objects:  []runtime.Object{matchingPDB, nonMatchingPDB},
			wantExists: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			policyv1.AddToScheme(scheme)
			objects := make([]client.Object, len(tt.objects))
			for i, obj := range tt.objects {
				objects[i] = obj.(client.Object)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			handler := NewMutatingHandler(fakeClient, logger)

			exists, err := handler.pdbExists(context.Background(), "default", "nginx", podLabels)

			require.NoError(t, err)
			assert.Equal(t, tt.wantExists, exists)
		})
	}
}

func TestMutatingHandlerHandle(t *testing.T) {
	zaplog, _ := zap.NewProduction()
	logger := zapr.NewLogger(zaplog)

	deployment := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-deploy",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{"app": "test"},
				},
			},
		},
	}

	deploymentRaw, _ := json.Marshal(deployment)

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Labels: map[string]string{
				"pdb-min-available":    "2",
				"pdb-max-unavailable":  "1",
			},
		},
	}

	tests := []struct {
		name           string
		method         string
		contentType    string
		body           []byte
		objects        []runtime.Object
		wantStatus     int
		wantAllowed    bool
	}{
		{
			name:        "non-POST request",
			method:      http.MethodGet,
			contentType: "application/json",
			wantStatus:  http.StatusMethodNotAllowed,
		},
		{
			name:        "invalid content type",
			method:      http.MethodPost,
			contentType: "text/plain",
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "invalid JSON",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        []byte("invalid"),
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:   "valid CREATE with both labels, no PDB - should create",
			method: http.MethodPost,
			contentType: "application/json",
			body: func() []byte {
				review := &admissionv1.AdmissionReview{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "admission.k8s.io/v1",
						Kind:       "AdmissionReview",
					},
					Request: &admissionv1.AdmissionRequest{
						UID:       "test-uid",
						Operation: admissionv1.Create,
						Kind:      metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						Object:    runtime.RawExtension{Raw: deploymentRaw},
					},
				}
				body, _ := json.Marshal(review)
				return body
			}(),
			objects:    []runtime.Object{namespace},
			wantStatus: http.StatusOK,
			wantAllowed: true,
		},
		{
			name:   "valid CREATE with incomplete labels - should reject",
			method: http.MethodPost,
			contentType: "application/json",
			body: func() []byte {
				review := &admissionv1.AdmissionReview{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "admission.k8s.io/v1",
						Kind:       "AdmissionReview",
					},
					Request: &admissionv1.AdmissionRequest{
						UID:       "test-uid",
						Operation: admissionv1.Create,
						Kind:      metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
						Object:    runtime.RawExtension{Raw: deploymentRaw},
					},
				}
				body, _ := json.Marshal(review)
				return body
			}(),
			objects: []runtime.Object{
				&corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{
						Name: "default",
						Labels: map[string]string{
							"pdb-min-available": "2",
						},
					},
				},
			},
			wantStatus:  http.StatusOK,
			wantAllowed: true, // Mutation webhook allows, validation will reject
		},
		{
			name:   "bare pod - should skip",
			method: http.MethodPost,
			contentType: "application/json",
			body: func() []byte {
				review := &admissionv1.AdmissionReview{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "admission.k8s.io/v1",
						Kind:       "AdmissionReview",
					},
					Request: &admissionv1.AdmissionRequest{
						UID:       "test-uid",
						Operation: admissionv1.Create,
						Kind:      metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Pod"},
						Object:    runtime.RawExtension{Raw: deploymentRaw},
						Name:      "test-pod",
						Namespace: "default",
					},
				}
				body, _ := json.Marshal(review)
				return body
			}(),
			objects:    []runtime.Object{namespace},
			wantStatus: http.StatusOK,
			wantAllowed: true, // Mutation webhook skips Pods
		},
		{
			name:   "statefulset with both labels - should mutate",
			method: http.MethodPost,
			contentType: "application/json",
			body: func() []byte {
				review := &admissionv1.AdmissionReview{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "admission.k8s.io/v1",
						Kind:       "AdmissionReview",
					},
					Request: &admissionv1.AdmissionRequest{
						UID:       "test-uid",
						Operation: admissionv1.Create,
						Kind:      metav1.GroupVersionKind{Group: "apps", Version: "v1", Kind: "StatefulSet"},
						Object:    runtime.RawExtension{Raw: deploymentRaw},
					},
				}
				body, _ := json.Marshal(review)
				return body
			}(),
			objects:    []runtime.Object{namespace},
			wantStatus: http.StatusOK,
			wantAllowed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			corev1.AddToScheme(scheme)
			policyv1.AddToScheme(scheme)
			appsv1.AddToScheme(scheme)
			admissionv1.AddToScheme(scheme)
			objects := make([]client.Object, len(tt.objects))
			for i, obj := range tt.objects {
				objects[i] = obj.(client.Object)
			}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
			handler := NewMutatingHandler(fakeClient, logger)

			req := httptest.NewRequest(tt.method, "/mutate", nil)
			if tt.body != nil {
				req.Body = io.NopCloser(bytes.NewReader(tt.body))
			}
			req.Header.Set("Content-Type", tt.contentType)

			w := httptest.NewRecorder()
			handler.Handle(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)
		})
	}
}
