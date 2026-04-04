package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// MutatingHandler handles mutating admission webhooks
type MutatingHandler struct {
	client client.Client
	log    logr.Logger
}

func NewMutatingHandler(c client.Client, log logr.Logger) *MutatingHandler {
	return &MutatingHandler{
		client: c,
		log:    log,
	}
}

func (h *MutatingHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "invalid content type", http.StatusBadRequest)
		return
	}

	// Read and unmarshal admission review
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	admissionReview := &admissionv1.AdmissionReview{}
	if err := json.Unmarshal(body, admissionReview); err != nil {
		h.log.Error(err, "failed to unmarshal admission review")
		http.Error(w, "failed to unmarshal admission review", http.StatusBadRequest)
		return
	}

	if admissionReview.APIVersion != "admission.k8s.io/v1" {
		http.Error(w, "unsupported admission review version", http.StatusBadRequest)
		return
	}

	req := admissionReview.Request
	if req == nil {
		http.Error(w, "no admission request", http.StatusBadRequest)
		return
	}

	// Only handle Deployments on CREATE
	if req.Kind.Group != "apps" || req.Kind.Kind != "Deployment" || req.Operation != admissionv1.Create {
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Unmarshal deployment
	deployment := &appsv1.Deployment{}
	if err := json.Unmarshal(req.Object.Raw, deployment); err != nil {
		h.log.Error(err, "failed to unmarshal deployment")
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	h.log.Info("validating mutation eligibility",
		"name", deployment.Name,
		"namespace", deployment.Namespace)

	// Check if namespace has PDB configuration labels
	hasConfig, minAvailable, maxUnavailable := h.getNamespacePDBLabels(r.Context(), deployment.Namespace)
	if !hasConfig {
		h.log.Info("namespace has no PDB configuration labels, skipping mutation",
			"namespace", deployment.Namespace)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	h.log.Info("namespace has PDB configuration, checking for existing PDB",
		"name", deployment.Name,
		"namespace", deployment.Namespace,
		"minAvailable", minAvailable.IntVal,
		"maxUnavailable", maxUnavailable.IntVal)

	// Check if PDB already exists
	pdbExists, err := h.pdbExists(r.Context(), deployment.Namespace, deployment.Name, deployment.Spec.Template.Labels)
	if err != nil {
		h.log.Error(err, "failed to check if PDB exists")
		// Continue anyway - let validating webhook catch it
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	if pdbExists {
		h.log.Info("matching PDB already exists, skipping mutation",
			"deployment", deployment.Name,
			"namespace", deployment.Namespace)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Create a default PDB with namespace-configured values
	h.log.Info("creating PDB for deployment",
		"deployment", deployment.Name,
		"namespace", deployment.Namespace,
		"minAvailable", minAvailable.IntVal,
		"maxUnavailable", maxUnavailable.IntVal)

	pdb, err := h.createPDB(r.Context(), deployment, minAvailable, maxUnavailable)
	if err != nil {
		h.log.Error(err, "failed to create PDB")
		// Don't fail the mutation - let validating webhook enforce it
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	h.log.Info("PDB created successfully",
		"pdb", pdb.Name,
		"namespace", pdb.Namespace)

	h.sendMutatingResponse(w, string(req.UID), nil)
}

func (h *MutatingHandler) pdbExists(ctx context.Context, namespace string, deploymentName string, podLabels map[string]string) (bool, error) {
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := h.client.List(ctx, pdbList, client.InNamespace(namespace)); err != nil {
		return false, err
	}

	// Check if any PDB matches the deployment's pod labels
	for _, pdb := range pdbList.Items {
		if pdb.Spec.Selector == nil {
			continue
		}

		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			h.log.Error(err, "failed to parse PDB label selector", "pdb", pdb.Name)
			continue
		}

		if selector.Matches(labels.Set(podLabels)) {
			return true, nil
		}
	}

	return false, nil
}

func (h *MutatingHandler) createPDB(ctx context.Context, deployment *appsv1.Deployment, minAvailable, maxUnavailable intstr.IntOrString) (*policyv1.PodDisruptionBudget, error) {
	// Create PDB with same name as deployment
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deployment.Name,
			Namespace: deployment.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":      "pdb-webhook",
				"app.kubernetes.io/component": "admission-controller",
				"app.kubernetes.io/managed-by": "pdb-webhook-mutator",
			},
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion:         "apps/v1",
					Kind:                "Deployment",
					Name:                deployment.Name,
					UID:                 deployment.UID,
					Controller:          boolPtr(true),
					BlockOwnerDeletion:  boolPtr(true),
				},
			},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable:   &minAvailable,
			MaxUnavailable: &maxUnavailable,
			Selector:       deployment.Spec.Selector,
		},
	}

	// Create the PDB
	if err := h.client.Create(ctx, pdb); err != nil {
		return nil, err
	}

	return pdb, nil
}

func (h *MutatingHandler) getNamespacePDBLabels(ctx context.Context, namespace string) (bool, intstr.IntOrString, intstr.IntOrString) {
	// Get namespace to check for PDB configuration labels
	ns := &corev1.Namespace{}
	err := h.client.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		h.log.Error(err, "failed to get namespace", "namespace", namespace)
		return false, intstr.IntOrString{}, intstr.IntOrString{}
	}

	// Look for PDB configuration labels on the namespace
	// Labels should be: pdb-webhook.awanipro.com/min-available and pdb-webhook.awanipro.com/max-unavailable
	minStr, hasMin := ns.Labels["pdb-webhook.awanipro.com/min-available"]
	maxStr, hasMax := ns.Labels["pdb-webhook.awanipro.com/max-unavailable"]

	// If labels don't exist, don't mutate
	if !hasMin || !hasMax {
		return false, intstr.IntOrString{}, intstr.IntOrString{}
	}

	// Parse min-available
	minVal, err := strconv.Atoi(minStr)
	if err != nil {
		h.log.Error(err, "failed to parse min-available label", "namespace", namespace, "value", minStr)
		return false, intstr.IntOrString{}, intstr.IntOrString{}
	}

	// Parse max-unavailable
	maxVal, err := strconv.Atoi(maxStr)
	if err != nil {
		h.log.Error(err, "failed to parse max-unavailable label", "namespace", namespace, "value", maxStr)
		return false, intstr.IntOrString{}, intstr.IntOrString{}
	}

	minAvailable := intOrString(minVal)
	maxUnavailable := intOrString(maxVal)

	h.log.Info("loaded PDB config from namespace labels",
		"namespace", namespace,
		"minAvailable", minVal,
		"maxUnavailable", maxVal)

	return true, minAvailable, maxUnavailable
}

func (h *MutatingHandler) sendMutatingResponse(w http.ResponseWriter, uid string, patches []map[string]interface{}) {
	response := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     types.UID(uid),
			Allowed: true,
		},
	}

	// If there are patches, include them
	if len(patches) > 0 {
		patchData, _ := json.Marshal(patches)
		patchType := admissionv1.PatchTypeJSONPatch
		response.Response.PatchType = &patchType
		response.Response.Patch = patchData
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Helper functions
func intOrString(val int) intstr.IntOrString {
	return intstr.IntOrString{
		Type:   intstr.Int,
		IntVal: int32(val),
	}
}

func boolPtr(b bool) *bool {
	return &b
}
