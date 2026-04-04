package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type Handler struct {
	client client.Client
	log    logr.Logger
}

func NewHandler(c client.Client, log logr.Logger) *Handler {
	return &Handler{
		client: c,
		log:    log,
	}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
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

	// Validate this is a v1 AdmissionReview
	if admissionReview.APIVersion != "admission.k8s.io/v1" {
		http.Error(w, "unsupported admission review version", http.StatusBadRequest)
		return
	}

	req := admissionReview.Request
	if req == nil {
		http.Error(w, "no admission request", http.StatusBadRequest)
		return
	}

	// Only handle Deployments and StatefulSets on CREATE or UPDATE
	// Reject bare Pods
	if req.Operation != admissionv1.Create && req.Operation != admissionv1.Update {
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	if req.Kind.Group != "apps" {
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	// Reject bare Pods only if enforcement is enabled
	if req.Kind.Kind == "Pod" {
		// Check if enforcement is enabled in this namespace
		configValid, configErr := h.checkNamespaceConfig(r.Context(), req.Namespace)

		// If config is incomplete, reject
		if configErr != "" {
			h.log.Info("rejecting bare pod due to incomplete namespace configuration",
				"name", req.Name,
				"namespace", req.Namespace,
				"error", configErr)
			h.sendResponse(w, string(req.UID), false,
				"namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together")
			return
		}

		// If enforcement enabled (both labels present), reject bare pod
		if configValid {
			h.log.Info("rejecting bare pod (enforcement enabled)",
				"name", req.Name,
				"namespace", req.Namespace)
			h.sendResponse(w, string(req.UID), false,
				"bare pods are not allowed in enforced namespace; pods must be created by Deployment or StatefulSet")
			return
		}

		// No enforcement, allow bare pod
		h.log.Info("allowing bare pod (no enforcement)",
			"name", req.Name,
			"namespace", req.Namespace)
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	// Only allow Deployments and StatefulSets
	if req.Kind.Kind != "Deployment" && req.Kind.Kind != "StatefulSet" {
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	// Extract workload info based on Kind
	var workloadName, workloadNamespace string
	var podLabels map[string]string

	if req.Kind.Kind == "Deployment" {
		deployment := &appsv1.Deployment{}
		if err := json.Unmarshal(req.Object.Raw, deployment); err != nil {
			h.log.Error(err, "failed to unmarshal deployment")
			h.sendResponse(w, string(req.UID), false, "internal error: failed to parse deployment")
			return
		}
		workloadName = deployment.Name
		workloadNamespace = deployment.Namespace
		podLabels = deployment.Spec.Template.Labels
	} else if req.Kind.Kind == "StatefulSet" {
		sts := &appsv1.StatefulSet{}
		if err := json.Unmarshal(req.Object.Raw, sts); err != nil {
			h.log.Error(err, "failed to unmarshal statefulset")
			h.sendResponse(w, string(req.UID), false, "internal error: failed to parse statefulset")
			return
		}
		workloadName = sts.Name
		workloadNamespace = sts.Namespace
		podLabels = sts.Spec.Template.Labels
	}

	h.log.Info("validating workload",
		"kind", req.Kind.Kind,
		"name", workloadName,
		"namespace", workloadNamespace,
		"operation", req.Operation)

	// Check namespace configuration first
	configValid, configErr := h.checkNamespaceConfig(r.Context(), workloadNamespace)
	if configErr != "" {
		// Incomplete configuration - reject
		h.log.Info("workload rejected due to incomplete namespace configuration",
			"kind", req.Kind.Kind,
			"name", workloadName,
			"namespace", workloadNamespace,
			"error", configErr)
		h.sendResponse(w, string(req.UID), false, configErr)
		return
	}

	if !configValid {
		// No configuration - allow
		h.log.Info("namespace has no PDB configuration, allowing workload",
			"kind", req.Kind.Kind,
			"name", workloadName,
			"namespace", workloadNamespace)
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	// Configuration is valid and present - enforce PDB requirement
	allowed, msg := h.hasPDB(r.Context(), workloadNamespace, podLabels)
	if !allowed {
		h.log.Info("workload rejected",
			"kind", req.Kind.Kind,
			"name", workloadName,
			"namespace", workloadNamespace,
			"reason", msg)
	}

	h.sendResponse(w, string(req.UID), allowed, msg)
}

func (h *Handler) checkNamespaceConfig(ctx context.Context, namespace string) (bool, string) {
	// Get namespace to check for PDB configuration labels
	ns := &corev1.Namespace{}
	err := h.client.Get(ctx, types.NamespacedName{Name: namespace}, ns)
	if err != nil {
		h.log.Error(err, "failed to get namespace", "namespace", namespace)
		return false, "internal error: failed to get namespace"
	}

	// Look for PDB configuration labels (both required or neither)
	_, hasMin := ns.Labels["pdb-min-available"]
	_, hasMax := ns.Labels["pdb-max-unavailable"]

	// Both must be present or neither - incomplete config is an error
	if hasMin != hasMax {
		return false, "namespace has incomplete PDB configuration: both pdb-min-available and pdb-max-unavailable labels must be set together"
	}

	// If both exist, return true (enforce PDB)
	if hasMin && hasMax {
		return true, ""
	}

	// If neither exists, return false (no enforcement)
	return false, ""
}

func (h *Handler) hasPDB(ctx context.Context, namespace string, podLabels map[string]string) (bool, string) {
	pdbList := &policyv1.PodDisruptionBudgetList{}
	if err := h.client.List(ctx, pdbList, client.InNamespace(namespace)); err != nil {
		h.log.Error(err, "failed to list PDBs", "namespace", namespace)
		return false, "internal error: failed to check PodDisruptionBudgets"
	}

	// If no PDBs exist in the namespace, deployment is rejected
	if len(pdbList.Items) == 0 {
		return false, "deployment rejected: no PodDisruptionBudget in namespace " + namespace +
			" selects pod labels; create a PDB with a matching selector before deploying"
	}

	// Check each PDB to see if it selects this deployment's pods
	for _, pdb := range pdbList.Items {
		if pdb.Spec.Selector == nil {
			// PDB with nil selector matches no pods (K8s semantics)
			continue
		}

		// Convert selector to label matcher
		selector, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector)
		if err != nil {
			h.log.Error(err, "failed to parse PDB label selector",
				"pdb", pdb.Name, "namespace", namespace)
			// Don't error out — skip this PDB and check others
			continue
		}

		// Check if this PDB selects the deployment's pod template labels
		if selector.Matches(labels.Set(podLabels)) {
			h.log.Info("deployment allowed by PDB",
				"deployment", "unknown", // We don't log deployment name here
				"namespace", namespace,
				"pdb", pdb.Name)
			return true, ""
		}
	}

	// No PDB matched the deployment's pod labels
	podLabelStr := labels.Set(podLabels).String()
	return false, "deployment rejected: no PodDisruptionBudget in namespace " + namespace +
		" selects pod labels " + podLabelStr +
		"; create a PDB with a matching selector before deploying"
}

func (h *Handler) sendResponse(w http.ResponseWriter, uid string, allowed bool, msg string) {
	response := &admissionv1.AdmissionReview{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "admission.k8s.io/v1",
			Kind:       "AdmissionReview",
		},
		Response: &admissionv1.AdmissionResponse{
			UID:     types.UID(uid),
			Allowed: allowed,
		},
	}

	if !allowed && msg != "" {
		response.Response.Result = &metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    403,
			Message: msg,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
