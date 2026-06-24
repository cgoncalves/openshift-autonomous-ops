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
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"os"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	prometheusapi "github.com/prometheus/client_golang/api"
	prometheusv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"

	anv1alpha1 "github.com/cgoncalves/openshift-autonomous-ops/poc-1.2/controller/api/v1alpha1"
)

const requeueInterval = 15 * time.Second

// ApplicationIntentReconciler reconciles a ApplicationIntent object
type ApplicationIntentReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
	PromAPI  prometheusv1.API
}

// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=an.openshift.io,resources=applicationintents/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;update;patch

func (r *ApplicationIntentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	intent := &anv1alpha1.ApplicationIntent{}
	if err := r.Get(ctx, req.NamespacedName, intent); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	ns := intent.Spec.Target.Namespace
	if ns == "" {
		ns = intent.Namespace
	}

	deployment := &appsv1.Deployment{}
	deployKey := client.ObjectKey{Namespace: ns, Name: intent.Spec.Target.Deployment}
	if err := r.Get(ctx, deployKey, deployment); err != nil {
		if apierrors.IsNotFound(err) {
			r.setStatus(ctx, intent, anv1alpha1.StateDegraded, 0, *deployment.Spec.Replicas, "Target deployment not found")
			return ctrl.Result{RequeueAfter: requeueInterval}, nil
		}
		return ctrl.Result{}, err
	}

	currentReplicas := int32(1)
	if deployment.Spec.Replicas != nil {
		currentReplicas = *deployment.Spec.Replicas
	}

	p99Ms, err := r.queryP99(ctx, intent.Spec.Target.Deployment, ns)
	if err != nil {
		log.Info("No metrics available", "error", err)
		r.setStatus(ctx, intent, anv1alpha1.StateUnknown, 0, currentReplicas,
			fmt.Sprintf("No metrics: %v", err))
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	slaMs := intent.Spec.SLA.P99LatencyMs
	desired := currentReplicas

	if p99Ms > slaMs {
		desired = min32(currentReplicas+1, intent.Spec.Constraints.MaxReplicas)
		if desired != currentReplicas {
			r.setStatus(ctx, intent, anv1alpha1.StateScaling, p99Ms, currentReplicas,
				fmt.Sprintf("P99 %dms exceeds SLA %dms, scaling %d→%d", p99Ms, slaMs, currentReplicas, desired))
			r.Recorder.Eventf(intent, corev1.EventTypeWarning, "SLABreached",
				"P99 latency %dms exceeds SLA %dms, scaling up to %d replicas", p99Ms, slaMs, desired)
		} else {
			r.setStatus(ctx, intent, anv1alpha1.StateDegraded, p99Ms, currentReplicas,
				fmt.Sprintf("P99 %dms exceeds SLA %dms, at max replicas (%d)", p99Ms, slaMs, currentReplicas))
		}
	} else if p99Ms < slaMs/2 && currentReplicas > intent.Spec.Constraints.MinReplicas {
		desired = max32(currentReplicas-1, intent.Spec.Constraints.MinReplicas)
		r.setStatus(ctx, intent, anv1alpha1.StateScaling, p99Ms, currentReplicas,
			fmt.Sprintf("P99 %dms well below SLA %dms, scaling %d→%d", p99Ms, slaMs, currentReplicas, desired))
		r.Recorder.Eventf(intent, corev1.EventTypeNormal, "ScaleDown",
			"P99 latency %dms well below SLA %dms, scaling down to %d replicas", p99Ms, slaMs, desired)
	} else {
		r.setStatus(ctx, intent, anv1alpha1.StateFulfilled, p99Ms, currentReplicas,
			fmt.Sprintf("P99 %dms within SLA %dms", p99Ms, slaMs))
	}

	if desired != currentReplicas {
		deployment.Spec.Replicas = &desired
		if err := r.Update(ctx, deployment); err != nil {
			log.Error(err, "Failed to scale deployment", "desired", desired)
			return ctrl.Result{}, err
		}
		log.Info("Scaled deployment", "deployment", deployment.Name, "from", currentReplicas, "to", desired)
	}

	return ctrl.Result{RequeueAfter: requeueInterval}, nil
}

func (r *ApplicationIntentReconciler) queryP99(ctx context.Context, deploymentName, namespace string) (int64, error) {
	query := fmt.Sprintf(
		`histogram_quantile(0.99, sum(rate(http_request_duration_seconds_bucket{job="%s", namespace="%s"}[2m])) by (le))`,
		deploymentName, namespace,
	)

	result, _, err := r.PromAPI.Query(ctx, query, time.Now())
	if err != nil {
		return 0, fmt.Errorf("prometheus query failed: %w", err)
	}

	vector, ok := result.(model.Vector)
	if !ok || len(vector) == 0 {
		return 0, fmt.Errorf("no metrics found for %s/%s", namespace, deploymentName)
	}

	value := float64(vector[0].Value)
	if value != value { // NaN check
		return 0, fmt.Errorf("no recent requests for %s/%s (NaN)", namespace, deploymentName)
	}
	return int64(value * 1000), nil
}

func (r *ApplicationIntentReconciler) setStatus(ctx context.Context, intent *anv1alpha1.ApplicationIntent, state anv1alpha1.IntentState, p99Ms int64, replicas int32, message string) {
	now := metav1.Now()
	intent.Status.State = state
	intent.Status.CurrentP99Ms = p99Ms
	intent.Status.CurrentReplicas = replicas
	intent.Status.Message = message
	intent.Status.LastUpdated = &now
	if err := r.Status().Update(ctx, intent); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update status")
	}
}

func (r *ApplicationIntentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	promAddr := os.Getenv("PROMETHEUS_URL")
	if promAddr == "" {
		promAddr = "https://thanos-querier.openshift-monitoring.svc:9091"
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}

	tokenFile := "/var/run/secrets/kubernetes.io/serviceaccount/token"
	if token, err := os.ReadFile(tokenFile); err == nil {
		transport.TLSClientConfig.InsecureSkipVerify = true
		originalTransport := transport
		_ = originalTransport
		promClient, err := prometheusapi.NewClient(prometheusapi.Config{
			Address: promAddr,
			RoundTripper: &bearerTokenTransport{
				token: string(token),
				inner: transport,
			},
		})
		if err != nil {
			return fmt.Errorf("creating prometheus client: %w", err)
		}
		r.PromAPI = prometheusv1.NewAPI(promClient)
	} else {
		promClient, err := prometheusapi.NewClient(prometheusapi.Config{
			Address:      promAddr,
			RoundTripper: transport,
		})
		if err != nil {
			return fmt.Errorf("creating prometheus client: %w", err)
		}
		r.PromAPI = prometheusv1.NewAPI(promClient)
	}

	r.Recorder = mgr.GetEventRecorderFor("applicationintent-controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&anv1alpha1.ApplicationIntent{}).
		Named("applicationintent").
		Complete(r)
}

type bearerTokenTransport struct {
	token string
	inner http.RoundTripper
}

func (t *bearerTokenTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req2 := req.Clone(req.Context())
	req2.Header.Set("Authorization", "Bearer "+t.token)
	return t.inner.RoundTrip(req2)
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}
