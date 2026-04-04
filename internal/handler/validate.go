package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
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

	// Only handle Deployments on CREATE or UPDATE
	if req.Kind.Group != "apps" || req.Kind.Kind != "Deployment" ||
		(req.Operation != admissionv1.Create && req.Operation != admissionv1.Update) {
		h.sendResponse(w, string(req.UID), true, "")
		return
	}

	// Unmarshal deployment from raw object
	deployment := &appsv1.Deployment{}
	if err := json.Unmarshal(req.Object.Raw, deployment); err != nil {
		h.log.Error(err, "failed to unmarshal deployment")
		h.sendResponse(w, string(req.UID), false, "internal error: failed to parse deployment")
		return
	}

	h.log.Info("validating deployment",
		"name", deployment.Name,
		"namespace", deployment.Namespace,
		"operation", req.Operation)

	// Check if PDB exists for this deployment
	allowed, msg := h.hasPDB(r.Context(), deployment.Namespace, deployment.Spec.Template.Labels)
	if !allowed {
		h.log.Info("deployment rejected",
			"name", deployment.Name,
			"namespace", deployment.Namespace,
			"reason", msg)
	}

	h.sendResponse(w, string(req.UID), allowed, msg)
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
