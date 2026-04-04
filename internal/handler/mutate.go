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

// ValidateNamespaceConfig checks if namespace has complete PDB configuration
// Returns error string if config is incomplete or invalid
func (h *MutatingHandler) ValidateNamespaceConfig(ns *corev1.Namespace) string {
	if ns == nil || ns.Labels == nil {
		return ""
	}

	_, hasMin := ns.Labels["pdb-min-available"]
	_, hasMax := ns.Labels["pdb-max-unavailable"]

	// Both must be present or neither - incomplete config is an error
	if hasMin != hasMax {
		h.log.Info("rejecting namespace: incomplete PDB configuration",
			"namespace", ns.Name,
			"hasMin", hasMin,
			"hasMax", hasMax)
		return "namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together"
	}

	return ""
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

	// Handle Namespace validation (CREATE and UPDATE)
	if req.Kind.Kind == "Namespace" {
		ns := &corev1.Namespace{}
		if err := json.Unmarshal(req.Object.Raw, ns); err != nil {
			h.log.Error(err, "failed to unmarshal namespace")
			h.sendMutatingResponse(w, string(req.UID), nil)
			return
		}

		// Validate namespace config completeness
		if configErr := h.ValidateNamespaceConfig(ns); configErr != "" {
			h.log.Info("rejecting namespace mutation: incomplete configuration",
				"namespace", ns.Name,
				"action", "reject",
				"reason", "incomplete-config")
			h.sendMutatingResponse(w, string(req.UID), nil)
			// Return error response for namespace
			response := &admissionv1.AdmissionReview{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "admission.k8s.io/v1",
					Kind:       "AdmissionReview",
				},
				Response: &admissionv1.AdmissionResponse{
					UID:     types.UID(req.UID),
					Allowed: false,
					Result: &metav1.Status{
						Status:  metav1.StatusFailure,
						Code:    400,
						Message: configErr,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(response)
			return
		}

		// Namespace config is valid or incomplete - allow
		h.log.Info("namespace mutation allowed",
			"namespace", ns.Name,
			"action", "allow")
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Only handle Deployments and StatefulSets on CREATE
	// Reject bare Pods
	if req.Operation != admissionv1.Create {
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	if req.Kind.Group != "apps" {
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Reject bare Pods
	if req.Kind.Kind == "Pod" {
		h.log.Info("rejecting bare pod",
			"name", req.Name,
			"namespace", req.Namespace)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Only allow Deployments and StatefulSets
	if req.Kind.Kind != "Deployment" && req.Kind.Kind != "StatefulSet" {
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Unmarshal workload (Deployment or StatefulSet)
	var workloadName, workloadNamespace string
	var podLabels map[string]string

	if req.Kind.Kind == "Deployment" {
		deployment := &appsv1.Deployment{}
		if err := json.Unmarshal(req.Object.Raw, deployment); err != nil {
			h.log.Error(err, "failed to unmarshal deployment")
			h.sendMutatingResponse(w, string(req.UID), nil)
			return
		}
		workloadName = deployment.Name
		workloadNamespace = deployment.Namespace
		podLabels = deployment.Spec.Template.Labels
	} else if req.Kind.Kind == "StatefulSet" {
		sts := &appsv1.StatefulSet{}
		if err := json.Unmarshal(req.Object.Raw, sts); err != nil {
			h.log.Error(err, "failed to unmarshal statefulset")
			h.sendMutatingResponse(w, string(req.UID), nil)
			return
		}
		workloadName = sts.Name
		workloadNamespace = sts.Namespace
		podLabels = sts.Spec.Template.Labels
	}

	h.log.Info("validating mutation eligibility",
		"kind", req.Kind.Kind,
		"name", workloadName,
		"namespace", workloadNamespace)

	// Check if namespace has PDB configuration labels (both required)
	hasConfig, minAvailable, maxUnavailable, configErr := h.getNamespacePDBLabels(r.Context(), workloadNamespace)
	if configErr != "" {
		// Incomplete configuration - reject
		h.log.Info("namespace has incomplete PDB configuration",
			"namespace", workloadNamespace,
			"error", configErr)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	if !hasConfig {
		// No PDB configuration at all - allow
		h.log.Info("namespace has no PDB configuration labels, skipping mutation",
			"namespace", workloadNamespace)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// For StatefulSets, force maxUnavailable=1 for ordinal-order updates
	if req.Kind.Kind == "StatefulSet" {
		h.log.Info("StatefulSet detected: enforcing maxUnavailable=1 for ordinal-order rolling update",
			"name", workloadName,
			"namespace", workloadNamespace)
		maxUnavailable = intOrString(1)
		minAvailable = intstr.IntOrString{Type: intstr.Int, IntVal: 0} // Clear minAvailable
	}

	h.log.Info("namespace has complete PDB configuration, checking for existing PDB",
		"kind", req.Kind.Kind,
		"name", workloadName,
		"namespace", workloadNamespace,
		"minAvailable", minAvailable.IntVal,
		"maxUnavailable", maxUnavailable.IntVal)

	// Check if PDB already exists
	pdbExists, err := h.pdbExists(r.Context(), workloadNamespace, workloadName, podLabels)
	if err != nil {
		h.log.Error(err, "failed to check if PDB exists")
		// Continue anyway - let validating webhook catch it
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	if pdbExists {
		h.log.Info("matching PDB already exists, skipping mutation",
			"kind", req.Kind.Kind,
			"name", workloadName,
			"namespace", workloadNamespace)
		h.sendMutatingResponse(w, string(req.UID), nil)
		return
	}

	// Create a default PDB with namespace-configured values
	h.log.Info("creating PDB for workload",
		"kind", req.Kind.Kind,
		"name", workloadName,
		"namespace", workloadNamespace,
		"minAvailable", minAvailable.IntVal,
		"maxUnavailable", maxUnavailable.IntVal)

	// Get selector based on kind
	var selector *metav1.LabelSelector
	if req.Kind.Kind == "Deployment" {
		deployment := &appsv1.Deployment{}
		json.Unmarshal(req.Object.Raw, deployment)
		selector = deployment.Spec.Selector
	} else if req.Kind.Kind == "StatefulSet" {
		sts := &appsv1.StatefulSet{}
		json.Unmarshal(req.Object.Raw, sts)
		selector = sts.Spec.Selector
	}

	pdb, err := h.createPDB(r.Context(), workloadName, workloadNamespace, selector, minAvailable, maxUnavailable)
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

func (h *MutatingHandler) createPDB(ctx context.Context, name, namespace string, selector *metav1.LabelSelector, minAvailable, maxUnavailable intstr.IntOrString) (*policyv1.PodDisruptionBudget, error) {
	// Create PDB with same name as workload (Deployment or StatefulSet)
	// Note: Kubernetes only allows ONE of MinAvailable or MaxUnavailable, not both
	// Priority: MinAvailable takes precedence if minAvailable > 0
	spec := policyv1.PodDisruptionBudgetSpec{
		Selector: selector,
	}

	// Only set MinAvailable if it's > 0, otherwise use MaxUnavailable
	if minAvailable.IntVal > 0 {
		spec.MinAvailable = &minAvailable
		h.log.Info("using minAvailable for PDB", "minAvailable", minAvailable.IntVal)
	} else {
		spec.MaxUnavailable = &maxUnavailable
		h.log.Info("using maxUnavailable for PDB", "maxUnavailable", maxUnavailable.IntVal)
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "pdb-webhook",
				"app.kubernetes.io/component":  "admission-controller",
				"app.kubernetes.io/managed-by": "pdb-webhook-mutator",
				"pdb-webhook.workload-name":    name,
			},
		},
		Spec: spec,
	}

	// Create the PDB
	if err := h.client.Create(ctx, pdb); err != nil {
		return nil, err
	}

	return pdb, nil
}

func (h *MutatingHandler) getNamespacePDBLabels(ctx context.Context, namespace string) (bool, intstr.IntOrString, intstr.IntOrString, string) {
	// Get namespace to check for PDB configuration labels
	ns := &corev1.Namespace{}
	err := h.client.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		h.log.Error(err, "failed to get namespace", "namespace", namespace)
		return false, intstr.IntOrString{}, intstr.IntOrString{}, ""
	}

	// Look for PDB configuration labels on the namespace
	// Labels: pdb-min-available and pdb-max-unavailable (both required)
	minStr, hasMin := ns.Labels["pdb-min-available"]
	maxStr, hasMax := ns.Labels["pdb-max-unavailable"]

	// Both must be present or neither - incomplete config is an error
	if hasMin != hasMax {
		errMsg := "incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together"
		h.log.Info(errMsg,
			"namespace", namespace,
			"hasMin", hasMin,
			"hasMax", hasMax)
		return false, intstr.IntOrString{}, intstr.IntOrString{}, errMsg
	}

	// If neither exists, allow without constraints
	if !hasMin {
		h.log.Info("namespace has no PDB config labels",
			"namespace", namespace)
		return false, intstr.IntOrString{}, intstr.IntOrString{}, ""
	}

	// Parse min-available
	minVal, err := strconv.Atoi(minStr)
	if err != nil {
		h.log.Error(err, "failed to parse pdb-min-available label", "namespace", namespace, "value", minStr)
		return false, intstr.IntOrString{}, intstr.IntOrString{}, "invalid pdb-min-available value: " + minStr
	}

	// Parse max-unavailable
	maxVal, err := strconv.Atoi(maxStr)
	if err != nil {
		h.log.Error(err, "failed to parse pdb-max-unavailable label", "namespace", namespace, "value", maxStr)
		return false, intstr.IntOrString{}, intstr.IntOrString{}, "invalid pdb-max-unavailable value: " + maxStr
	}

	minAvailable := intOrString(minVal)
	maxUnavailable := intOrString(maxVal)

	h.log.Info("loaded PDB config from namespace labels",
		"namespace", namespace,
		"minAvailable", minVal,
		"maxUnavailable", maxVal)

	return true, minAvailable, maxUnavailable, ""
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
