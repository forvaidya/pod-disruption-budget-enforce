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

func TestCheckNamespaceConfig(t *testing.T) {
	zaplog, _ := zap.NewProduction()
	logger := zapr.NewLogger(zaplog)

	tests := []struct {
		name        string
		namespace   *corev1.Namespace
		wantValid   bool
		wantError   string
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
			wantValid: true,
			wantError: "",
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
			wantValid: false,
			wantError: "incomplete PDB configuration",
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
			wantValid: false,
			wantError: "incomplete PDB configuration",
		},
		{
			name: "no labels present",
			namespace: &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:   "test",
					Labels: map[string]string{},
				},
			},
			wantValid: false,
			wantError: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := runtime.NewScheme()
			corev1.AddToScheme(scheme)
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(tt.namespace).Build()
			handler := NewHandler(client, logger)

			valid, err := handler.checkNamespaceConfig(context.Background(), "test")

			assert.Equal(t, tt.wantValid, valid)
			if tt.wantError != "" {
				assert.Contains(t, err, tt.wantError)
			} else {
				assert.Equal(t, "", err)
			}
		})
	}
}

func TestValidatorHasPDB(t *testing.T) {
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
		name        string
		objects     []runtime.Object
		wantAllowed bool
		wantMsg     string
	}{
		{
			name:        "matching PDB exists",
			objects:     []runtime.Object{matchingPDB},
			wantAllowed: true,
			wantMsg:     "",
		},
		{
			name:        "no PDB exists",
			objects:     []runtime.Object{},
			wantAllowed: false,
			wantMsg:     "no PodDisruptionBudget",
		},
		{
			name:        "non-matching PDB exists",
			objects:     []runtime.Object{nonMatchingPDB},
			wantAllowed: false,
			wantMsg:     "no PodDisruptionBudget",
		},
		{
			name:        "both matching and non-matching exist",
			objects:     []runtime.Object{matchingPDB, nonMatchingPDB},
			wantAllowed: true,
			wantMsg:     "",
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
			handler := NewHandler(fakeClient, logger)

			allowed, msg := handler.hasPDB(context.Background(), "default", podLabels)

			assert.Equal(t, tt.wantAllowed, allowed)
			if tt.wantMsg != "" {
				assert.Contains(t, msg, tt.wantMsg)
			} else {
				assert.Equal(t, "", msg)
			}
		})
	}
}

func TestValidatingHandlerHandle(t *testing.T) {
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

	minAvailVal := intstr.FromInt(1)
	matchingPDB := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pdb",
			Namespace: "default",
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
			},
			MinAvailable: &minAvailVal,
		},
	}

	namespaceWithConfig := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Labels: map[string]string{
				"pdb-min-available":    "2",
				"pdb-max-unavailable":  "1",
			},
		},
	}

	namespaceIncompleteConfig := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
			Labels: map[string]string{
				"pdb-min-available": "2",
			},
		},
	}

	namespaceNoConfig := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "default",
			Labels: map[string]string{},
		},
	}

	tests := []struct {
		name        string
		method      string
		contentType string
		body        []byte
		objects     []runtime.Object
		wantStatus  int
		wantAllowed bool
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
			name:   "incomplete namespace config - should reject",
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
			objects:     []runtime.Object{namespaceIncompleteConfig},
			wantStatus:  http.StatusOK,
			wantAllowed: false,
		},
		{
			name:   "no namespace config - should allow",
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
			objects:     []runtime.Object{namespaceNoConfig},
			wantStatus:  http.StatusOK,
			wantAllowed: true,
		},
		{
			name:   "config present with matching PDB - should allow",
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
			objects:     []runtime.Object{namespaceWithConfig, matchingPDB},
			wantStatus:  http.StatusOK,
			wantAllowed: true,
		},
		{
			name:   "config present without matching PDB - should reject",
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
			objects:     []runtime.Object{namespaceWithConfig},
			wantStatus:  http.StatusOK,
			wantAllowed: false,
		},
		{
			name:   "bare pod with enforcement enabled - should reject",
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
			objects:     []runtime.Object{namespaceWithConfig},
			wantStatus:  http.StatusOK,
			wantAllowed: false,
		},
		{
			name:   "bare pod with no enforcement - should allow",
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
			objects:     []runtime.Object{namespaceNoConfig},
			wantStatus:  http.StatusOK,
			wantAllowed: true,
		},
		{
			name:   "statefulset with config and matching PDB - should allow",
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
			objects:     []runtime.Object{namespaceWithConfig, matchingPDB},
			wantStatus:  http.StatusOK,
			wantAllowed: true,
		},
		{
			name:   "non-apps resource - should allow",
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
						Kind:      metav1.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
						Object:    runtime.RawExtension{Raw: deploymentRaw},
					},
				}
				body, _ := json.Marshal(review)
				return body
			}(),
			objects:     []runtime.Object{namespaceWithConfig},
			wantStatus:  http.StatusOK,
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
			handler := NewHandler(fakeClient, logger)

			req := httptest.NewRequest(tt.method, "/validate", nil)
			if tt.body != nil {
				req.Body = io.NopCloser(bytes.NewReader(tt.body))
			}
			req.Header.Set("Content-Type", tt.contentType)

			w := httptest.NewRecorder()
			handler.Handle(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantStatus == http.StatusOK && w.Body.Len() > 0 {
				var review admissionv1.AdmissionReview
				_ = json.NewDecoder(w.Body).Decode(&review)
				if review.Response != nil {
					assert.Equal(t, tt.wantAllowed, review.Response.Allowed)
				}
			}
		})
	}
}
