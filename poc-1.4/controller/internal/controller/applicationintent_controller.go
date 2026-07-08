/*
Copyright 2026.

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

package controller

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	anv1alpha1 "github.com/cgoncalves/openshift-autonomous-ops/poc-1.4/controller/api/v1alpha1"
)

const (
	runtimeCheckInterval = 15 * time.Second
	degradedThreshold    = 90 * time.Second
)

type ApplicationIntentReconciler struct {
	client.Client
	Scheme        *runtime.Scheme
	Recorder      record.EventRecorder
	LlamaStackURL string
	ModelID        string
	APIKey         string
}

// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ApplicationIntentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	intent := &anv1alpha1.ApplicationIntent{}
	if err := r.Get(ctx, req.NamespacedName, intent); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	switch intent.Status.Phase {
	case "", anv1alpha1.PhaseAnalyzing:
		return r.reconcileDesignTime(ctx, intent)

	case anv1alpha1.PhasePendingApproval:
		if intent.Status.Approved || intent.Spec.AutoApprove {
			return r.reconcileApply(ctx, intent)
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil

	case anv1alpha1.PhaseApplying:
		return r.reconcileApply(ctx, intent)

	case anv1alpha1.PhaseActive, anv1alpha1.PhaseFulfilled, anv1alpha1.PhaseAdapting, anv1alpha1.PhaseDegraded:
		return r.reconcileRuntime(ctx, intent)

	default:
		return ctrl.Result{RequeueAfter: runtimeCheckInterval}, nil
	}
}

// reconcileDesignTime calls the LLM to analyze and recommend K8s configs.
func (r *ApplicationIntentReconciler) reconcileDesignTime(ctx context.Context, intent *anv1alpha1.ApplicationIntent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("Design-time analysis", "intent", intent.Name)

	r.setPhase(ctx, intent, anv1alpha1.PhaseAnalyzing, "Analyzing workload and generating recommendation...")

	ns := intent.Spec.Target.Namespace
	if ns == "" {
		ns = intent.Namespace
	}

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: intent.Spec.Target.Deployment, Namespace: ns}, deployment); err != nil {
		r.setPhase(ctx, intent, anv1alpha1.PhaseError, fmt.Sprintf("Target deployment not found: %v", err))
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}

	prompt := r.buildAnalysisPrompt(intent, deployment)
	llmResponse, err := r.callLLM(ctx, prompt)
	if err != nil {
		log.Error(err, "LLM call failed")
		r.setPhase(ctx, intent, anv1alpha1.PhaseError, fmt.Sprintf("AI analysis failed: %v", err))
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}

	resources, summary := r.parseLLMResponse(llmResponse)

	now := metav1.Now()
	intent.Status.Recommendation = &anv1alpha1.Recommendation{
		Summary:     summary,
		GeneratedAt: &now,
		Resources:   resources,
	}

	if intent.Spec.AutoApprove {
		intent.Status.Phase = anv1alpha1.PhaseApplying
		intent.Status.Approved = true
		intent.Status.Message = "Auto-approved. Applying recommendation..."
		r.Recorder.Event(intent, "Normal", "AutoApproved", "Recommendation auto-approved")
	} else {
		intent.Status.Phase = anv1alpha1.PhasePendingApproval
		intent.Status.Approved = false
		intent.Status.Message = "Recommendation ready for review. Set status.approved=true to apply."
		r.Recorder.Event(intent, "Normal", "PendingApproval", "AI recommendation ready for review")
	}

	if err := r.Status().Update(ctx, intent); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Recommendation generated", "resources", len(resources), "autoApprove", intent.Spec.AutoApprove)
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// reconcileApply creates the recommended K8s resources.
func (r *ApplicationIntentReconciler) reconcileApply(ctx context.Context, intent *anv1alpha1.ApplicationIntent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if intent.Status.Recommendation == nil || len(intent.Status.Recommendation.Resources) == 0 {
		r.setPhase(ctx, intent, anv1alpha1.PhaseError, "No recommendation to apply")
		return ctrl.Result{}, nil
	}

	r.setPhase(ctx, intent, anv1alpha1.PhaseApplying, "Applying recommended resources...")

	for _, res := range intent.Status.Recommendation.Resources {
		obj := &unstructured.Unstructured{}
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(res.Manifest), 4096)
		if err := decoder.Decode(obj); err != nil {
			log.Error(err, "Failed to parse manifest", "resource", res.Name)
			continue
		}

		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(obj.GroupVersionKind())
		err := r.Get(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()}, existing)
		if apierrors.IsNotFound(err) {
			if err := r.Create(ctx, obj); err != nil {
				log.Error(err, "Failed to create resource", "resource", res.Name)
				continue
			}
			log.Info("Created resource", "kind", res.Kind, "name", res.Name)
			r.Recorder.Eventf(intent, "Normal", "ResourceCreated", "Created %s/%s", res.Kind, res.Name)
		} else if err == nil {
			obj.SetResourceVersion(existing.GetResourceVersion())
			if err := r.Update(ctx, obj); err != nil {
				log.Error(err, "Failed to update resource", "resource", res.Name)
				continue
			}
			log.Info("Updated resource", "kind", res.Kind, "name", res.Name)
		} else {
			log.Error(err, "Failed to check resource", "resource", res.Name)
		}
	}

	r.setPhase(ctx, intent, anv1alpha1.PhaseActive, "Resources applied. Runtime monitoring active.")
	r.Recorder.Event(intent, "Normal", "Applied", "All recommended resources applied successfully")
	return ctrl.Result{RequeueAfter: runtimeCheckInterval}, nil
}

// reconcileRuntime monitors fulfillment without calling the LLM.
// If degraded for longer than the threshold, triggers AI re-analysis.
func (r *ApplicationIntentReconciler) reconcileRuntime(ctx context.Context, intent *anv1alpha1.ApplicationIntent) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	ns := intent.Spec.Target.Namespace
	if ns == "" {
		ns = intent.Namespace
	}

	deployment := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: intent.Spec.Target.Deployment, Namespace: ns}, deployment); err != nil {
		r.setPhase(ctx, intent, anv1alpha1.PhaseDegraded, "Target deployment not found")
		return ctrl.Result{RequeueAfter: runtimeCheckInterval}, nil
	}

	hpaName := fmt.Sprintf("%s-hpa", intent.Spec.Target.Deployment)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	hpaExists := true
	if err := r.Get(ctx, types.NamespacedName{Name: hpaName, Namespace: ns}, hpa); err != nil {
		hpaExists = false
	}

	currentReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		currentReplicas = *deployment.Spec.Replicas
	}

	now := metav1.Now()
	fulfillment := &anv1alpha1.Fulfillment{
		CurrentReplicas: currentReplicas,
		LastChecked:     &now,
	}

	isDegraded := false
	if !hpaExists {
		fulfillment.State = "Active"
		fulfillment.Message = "HPA not found — resources applied but HPA may have been deleted"
	} else if hpa.Status.CurrentReplicas >= intent.Spec.Constraints.MaxReplicas {
		cpuUtil := ""
		for _, m := range hpa.Status.CurrentMetrics {
			if m.Resource != nil && m.Resource.Name == "cpu" && m.Resource.Current.AverageUtilization != nil {
				cpuUtil = fmt.Sprintf(", CPU utilization: %d%%", *m.Resource.Current.AverageUtilization)
			}
		}
		isDegraded = true
		fulfillment.State = "Degraded"
		fulfillment.Message = fmt.Sprintf("HPA at max replicas (%d)%s. SLA may be at risk.", hpa.Status.CurrentReplicas, cpuUtil)
		r.Recorder.Event(intent, "Warning", "Degraded", fulfillment.Message)
	} else if hpa.Status.CurrentReplicas > hpa.Status.DesiredReplicas {
		fulfillment.State = "Adapting"
		fulfillment.Message = fmt.Sprintf("HPA scaling down: %d → %d replicas", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas)
	} else if hpa.Status.DesiredReplicas > hpa.Status.CurrentReplicas {
		fulfillment.State = "Adapting"
		fulfillment.Message = fmt.Sprintf("HPA scaling up: %d → %d replicas", hpa.Status.CurrentReplicas, hpa.Status.DesiredReplicas)
	} else {
		fulfillment.State = "Fulfilled"
		fulfillment.Message = "SLA met. HPA managing within constraints."
	}

	// Track degraded duration
	if isDegraded {
		if intent.Status.Fulfillment != nil && intent.Status.Fulfillment.DegradedSince != nil {
			fulfillment.DegradedSince = intent.Status.Fulfillment.DegradedSince
		} else {
			fulfillment.DegradedSince = &now
		}

		degradedDuration := now.Time.Sub(fulfillment.DegradedSince.Time)
		if degradedDuration >= degradedThreshold {
			log.Info("Degraded for too long, escalating to AI re-analysis",
				"duration", degradedDuration, "threshold", degradedThreshold)
			r.Recorder.Eventf(intent, "Warning", "Escalation",
				"Degraded for %s (threshold: %s). Triggering AI re-analysis.",
				degradedDuration.Round(time.Second), degradedThreshold)

			intent.Status.Fulfillment = fulfillment
			return r.reconcileEscalation(ctx, intent, deployment, hpa)
		}
	} else {
		fulfillment.DegradedSince = nil
	}

	intent.Status.Fulfillment = fulfillment
	intent.Status.Phase = anv1alpha1.IntentPhase(fulfillment.State)
	intent.Status.Message = fulfillment.Message

	if err := r.Status().Update(ctx, intent); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{RequeueAfter: runtimeCheckInterval}, nil
}

// reconcileEscalation calls the LLM with runtime context to generate an updated recommendation.
func (r *ApplicationIntentReconciler) reconcileEscalation(ctx context.Context, intent *anv1alpha1.ApplicationIntent, deployment *appsv1.Deployment, hpa *autoscalingv2.HorizontalPodAutoscaler) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	log.Info("AI escalation: re-analyzing with runtime context")

	ns := intent.Spec.Target.Namespace
	if ns == "" {
		ns = intent.Namespace
	}

	// Build escalation prompt with runtime context
	cpuUtil := "unknown"
	cpuTarget := "unknown"
	for _, m := range hpa.Status.CurrentMetrics {
		if m.Resource != nil && m.Resource.Name == "cpu" && m.Resource.Current.AverageUtilization != nil {
			cpuUtil = fmt.Sprintf("%d%%", *m.Resource.Current.AverageUtilization)
		}
	}
	for _, m := range hpa.Spec.Metrics {
		if m.Resource != nil && m.Resource.Name == "cpu" && m.Resource.Target.AverageUtilization != nil {
			cpuTarget = fmt.Sprintf("%d%%", *m.Resource.Target.AverageUtilization)
		}
	}

	var containers []string
	for _, c := range deployment.Spec.Template.Spec.Containers {
		req := c.Resources.Requests
		lim := c.Resources.Limits
		containers = append(containers, fmt.Sprintf(
			"  - %s: requests(cpu=%s, memory=%s) limits(cpu=%s, memory=%s)",
			c.Name, req.Cpu().String(), req.Memory().String(),
			lim.Cpu().String(), lim.Memory().String(),
		))
	}

	var objectives []string
	for _, obj := range intent.Spec.Objectives {
		objectives = append(objectives, fmt.Sprintf("- %s %s: %s", obj.Type, obj.Metric, obj.Target))
	}

	prompt := fmt.Sprintf(`The previous AI recommendation was applied but the SLA is STILL NOT MET.
The HPA has been at max replicas for over %s and cannot scale further.

CURRENT RUNTIME STATE:
- Deployment: %s (namespace: %s)
- HPA: %d/%d replicas (at max)
- CPU utilization: %s (target was %s)
- Current containers:
%s

ORIGINAL OBJECTIVES:
%s

CONSTRAINTS:
- Max replicas: %d
- Max CPU per pod: %s
- Max memory per pod: %s

The previous scaling-only strategy is insufficient. Analyze WHY the SLA
cannot be met and generate UPDATED Kubernetes resource manifests that
address the bottleneck. Consider:
- Increasing CPU/memory limits if the workload is resource-constrained
- Adjusting HPA target utilization percentage
- Adding pod anti-affinity for better distribution
- Any other Kubernetes-native approach

For each resource, output it as a separate YAML document delimited by "---".
Start with a comment block explaining your analysis and what changed from
the previous recommendation.`,
		intent.Status.Fulfillment.DegradedSince.Time.Sub(time.Now()).Abs().Round(time.Second),
		intent.Spec.Target.Deployment, ns,
		hpa.Status.CurrentReplicas, intent.Spec.Constraints.MaxReplicas,
		cpuUtil, cpuTarget,
		strings.Join(containers, "\n"),
		strings.Join(objectives, "\n"),
		intent.Spec.Constraints.MaxReplicas,
		intent.Spec.Constraints.MaxCPUPerPod,
		intent.Spec.Constraints.MaxMemoryPerPod,
	)

	r.setPhase(ctx, intent, anv1alpha1.PhaseAnalyzing, "Escalation: re-analyzing with runtime context...")
	r.Recorder.Event(intent, "Normal", "ReAnalyzing", "AI re-analysis triggered due to prolonged degradation")

	llmResponse, err := r.callLLM(ctx, prompt)
	if err != nil {
		log.Error(err, "Escalation LLM call failed")
		r.setPhase(ctx, intent, anv1alpha1.PhaseDegraded,
			fmt.Sprintf("Escalation failed: %v. Manual intervention required.", err))
		return ctrl.Result{RequeueAfter: 60 * time.Second}, nil
	}

	resources, summary := r.parseLLMResponse(llmResponse)

	now := metav1.Now()
	intent.Status.Recommendation = &anv1alpha1.Recommendation{
		Summary:     summary,
		GeneratedAt: &now,
		Resources:   resources,
	}

	if intent.Spec.AutoApprove {
		intent.Status.Phase = anv1alpha1.PhaseApplying
		intent.Status.Approved = true
		intent.Status.Message = "Escalation: auto-approved updated recommendation."
		r.Recorder.Event(intent, "Normal", "EscalationAutoApproved", "Updated recommendation auto-approved")
	} else {
		intent.Status.Phase = anv1alpha1.PhasePendingApproval
		intent.Status.Approved = false
		intent.Status.Message = "Escalation: updated recommendation ready for review."
		r.Recorder.Event(intent, "Normal", "EscalationPendingApproval", "Updated AI recommendation ready for review")
	}

	// Reset degraded timer
	if intent.Status.Fulfillment != nil {
		intent.Status.Fulfillment.DegradedSince = nil
	}

	if err := r.Status().Update(ctx, intent); err != nil {
		return ctrl.Result{}, err
	}

	log.Info("Escalation recommendation generated", "resources", len(resources))
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *ApplicationIntentReconciler) buildAnalysisPrompt(intent *anv1alpha1.ApplicationIntent, deployment *appsv1.Deployment) string {
	ns := intent.Spec.Target.Namespace
	if ns == "" {
		ns = intent.Namespace
	}

	var objectives []string
	for _, obj := range intent.Spec.Objectives {
		objectives = append(objectives, fmt.Sprintf("- %s %s: %s", obj.Type, obj.Metric, obj.Target))
	}

	var containers []string
	for _, c := range deployment.Spec.Template.Spec.Containers {
		req := c.Resources.Requests
		lim := c.Resources.Limits
		containers = append(containers, fmt.Sprintf(
			"  - %s: requests(cpu=%s, memory=%s) limits(cpu=%s, memory=%s)",
			c.Name,
			req.Cpu().String(), req.Memory().String(),
			lim.Cpu().String(), lim.Memory().String(),
		))
	}

	replicas := int32(1)
	if deployment.Spec.Replicas != nil {
		replicas = *deployment.Spec.Replicas
	}

	return fmt.Sprintf(`Analyze this workload and generate Kubernetes resource manifests to meet the SLA objectives.

DEPLOYMENT: %s (namespace: %s)
Current replicas: %d
Containers:
%s

OBJECTIVES:
%s

CONSTRAINTS:
- Max replicas: %d
- Min replicas: %d
- Max CPU per pod: %s
- Max memory per pod: %s

Generate the following Kubernetes resource manifests as valid YAML:
1. An HorizontalPodAutoscaler (autoscaling/v2) named "%s-hpa" in namespace "%s"
   - Set appropriate min/max replicas and scaling metrics based on the objectives
   - Use CPU utilization as the primary metric
2. A PodDisruptionBudget (policy/v1) named "%s-pdb" in namespace "%s"
   - Set minAvailable based on the availability objective
3. Any recommended resource limit/request changes for the deployment

For each resource, output it as a separate YAML document delimited by "---".
Include a brief analysis summary at the top as a comment block.`,
		intent.Spec.Target.Deployment, ns,
		replicas,
		strings.Join(containers, "\n"),
		strings.Join(objectives, "\n"),
		intent.Spec.Constraints.MaxReplicas,
		intent.Spec.Constraints.MinReplicas,
		intent.Spec.Constraints.MaxCPUPerPod,
		intent.Spec.Constraints.MaxMemoryPerPod,
		intent.Spec.Target.Deployment, ns,
		intent.Spec.Target.Deployment, ns,
	)
}

func (r *ApplicationIntentReconciler) callLLM(ctx context.Context, prompt string) (string, error) {
	payload := map[string]interface{}{
		"model": r.ModelID,
		"messages": []map[string]string{
			{"role": "system", "content": "You are a Kubernetes capacity planner. Generate valid YAML manifests. " +
				"Start with a comment block summarizing your analysis, then output each resource as a separate YAML document delimited by ---."},
			{"role": "user", "content": prompt},
		},
		"max_tokens": 8192,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshaling request: %w", err)
	}

	httpClient := &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", r.LlamaStackURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.APIKey)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("LLM request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("LLM returned %d: %s", resp.StatusCode, string(respBody[:min(len(respBody), 200)]))
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content          *string `json:"content"`
				ReasoningContent *string `json:"reasoning_content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding LLM response: %w", err)
	}

	if len(result.Choices) == 0 {
		return "", fmt.Errorf("no choices in LLM response")
	}

	content := ""
	if result.Choices[0].Message.Content != nil {
		content = *result.Choices[0].Message.Content
	}
	if content == "" {
		return "", fmt.Errorf("empty content in LLM response (reasoning_content may have consumed all tokens)")
	}

	return content, nil
}

func (r *ApplicationIntentReconciler) parseLLMResponse(response string) ([]anv1alpha1.ResourceManifest, string) {
	var resources []anv1alpha1.ResourceManifest
	var summary string

	lines := strings.Split(response, "\n")
	var summaryLines []string
	inComment := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			inComment = true
			summaryLines = append(summaryLines, strings.TrimPrefix(trimmed, "# "))
		} else if inComment && trimmed == "" {
			inComment = false
		} else {
			break
		}
	}
	summary = strings.Join(summaryLines, "\n")

	docs := strings.Split(response, "---")
	for _, doc := range docs {
		doc = strings.TrimSpace(doc)
		if doc == "" || strings.HasPrefix(doc, "#") {
			continue
		}

		obj := &unstructured.Unstructured{}
		decoder := yamlutil.NewYAMLOrJSONDecoder(strings.NewReader(doc), 4096)
		if err := decoder.Decode(obj); err != nil {
			continue
		}

		if obj.GetAPIVersion() == "" || obj.GetKind() == "" {
			continue
		}

		resources = append(resources, anv1alpha1.ResourceManifest{
			APIVersion: obj.GetAPIVersion(),
			Kind:       obj.GetKind(),
			Name:       obj.GetName(),
			Manifest:   doc,
		})
	}

	return resources, summary
}

func (r *ApplicationIntentReconciler) setPhase(ctx context.Context, intent *anv1alpha1.ApplicationIntent, phase anv1alpha1.IntentPhase, message string) {
	intent.Status.Phase = phase
	intent.Status.Message = message
	if err := r.Status().Update(ctx, intent); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update status")
	}
}

func (r *ApplicationIntentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.LlamaStackURL = os.Getenv("LLAMASTACK_URL")
	if r.LlamaStackURL == "" {
		r.LlamaStackURL = "http://lsd-granite-milvus-inline-service.llama-stack.svc.cluster.local:8321"
	}
	r.ModelID = os.Getenv("MODEL_ID")
	if r.ModelID == "" {
		r.ModelID = "vllm-inference/Qwen3.6-35B-A3B"
	}
	r.APIKey = os.Getenv("LLM_API_KEY")
	r.Recorder = mgr.GetEventRecorderFor("applicationintent-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&anv1alpha1.ApplicationIntent{}).
		Named("applicationintent").
		Complete(r)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
