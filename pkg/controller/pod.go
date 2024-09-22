package controller

import (
	"context"
	"fmt"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"slices"
	"strconv"
	"time"
)

const (
	retryAnnotation = "pod-restart/retry-count"
)

var restartableErrorReasons = []string{
	"CrashLoopBackOff", "OOMKilled", "RunContainerError",
}

type PodReconciler struct {
	client.Client
	PrometheusAPI v1.API
}

func NewPodReconciler(mgr manager.Manager, promAPI v1.API) *PodReconciler {
	return &PodReconciler{
		Client:        mgr.GetClient(),
		PrometheusAPI: promAPI,
	}
}

func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	const minPodAge = 2 * time.Minute

	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	if pod.DeletionTimestamp != nil {
		logger.Info("Pod is being deleted", "pod", pod.Name)
		return reconcile.Result{}, nil
	}

	if !isRunningOrFailed(&pod) {
		logger.Info("Pod is not in Running or Failed phase", "pod", pod.Name)
		return reconcile.Result{}, nil
	}

	if podAge := time.Since(pod.CreationTimestamp.Time); podAge < minPodAge {
		logger.Info("Pod is too new to act upon", "pod", pod.Name, "age", podAge)
		return reconcile.Result{RequeueAfter: minPodAge - podAge}, nil
	}

	// Query Prometheus for pod error metrics
	errorQuery := fmt.Sprintf(`kube_pod_container_status_waiting_reason{pod="%s",namespace="%s"}`, pod.Name, pod.Namespace)
	result, err := r.queryPrometheus(ctx, errorQuery)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for pod errors")
		return reconcile.Result{}, err
	}

	needsRestart, errorReason := r.analyzePodErrors(result)

	if errorReason != "" {
		return r.handlePodError(ctx, &pod, needsRestart, errorReason)
	}

	return reconcile.Result{}, nil
}

func (r *PodReconciler) queryPrometheus(ctx context.Context, query string) (model.Value, error) {
	logger := log.FromContext(ctx)

	result, warnings, err := r.PrometheusAPI.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		logger.Info("Prometheus warnings:", warnings)
	}

	return result, nil
}

func (r *PodReconciler) analyzePodErrors(result model.Value) (bool, string) {
	vector, ok := result.(model.Vector)
	if !ok {
		return false, ""
	}

	for _, sample := range vector {
		reason := string(sample.Metric["reason"])
		if isRestartableError(reason) {
			return true, reason
		}
	}

	return false, ""
}

func (r *PodReconciler) handlePodError(ctx context.Context, pod *corev1.Pod, needsRestart bool, errorReason string) (reconcile.Result, error) {
	const maxRetryCount = 5
	logger := log.FromContext(ctx)

	if needsRestart {
		retryCount, err := r.getRetryCount(pod)
		if err != nil {
			logger.Error(err, "Failed to get retry count", "pod", pod.Name)
			return reconcile.Result{}, err
		}

		if retryCount >= maxRetryCount {
			logger.Info("Max retry count reached", "pod", pod.Name)
			return reconcile.Result{}, nil
		}

		if err = r.incrementRetryCount(ctx, pod, retryCount); err != nil {
			logger.Info("Failed to increment retry count:", err)
		}

		logger.Info("Restarting pod due to error", "pod", pod.Name, "reason", errorReason)
		if err := r.Delete(ctx, pod); err != nil {
			logger.Error(err, "Failed to delete pod", "pod", pod.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil

	} else {
		logger.Info("Non-restartable error detected", "pod", pod.Name, "reason", errorReason)
		return reconcile.Result{RequeueAfter: time.Hour}, nil
	}
}

func isRunningOrFailed(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodFailed
}

func isRestartableError(reason string) bool {
	return slices.Contains(restartableErrorReasons, reason)
}

func (r *PodReconciler) getRetryCount(pod *corev1.Pod) (int, error) {
	retryCount := 0
	if val, exists := pod.Annotations[retryAnnotation]; exists {
		return strconv.Atoi(val)
	}
	return retryCount, nil
}

func (r *PodReconciler) incrementRetryCount(ctx context.Context, pod *corev1.Pod, retryCount int) error {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[retryAnnotation] = strconv.Itoa(retryCount + 1)

	if err := r.Update(ctx, pod); err != nil {
		return err
	}
	return nil
}

func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
