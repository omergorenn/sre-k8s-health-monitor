package controller

import (
	"context"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"strconv"
	"time"
)

// Constants for managing pod behavior and retries
const (
	minPodAge       = 2 * time.Minute // How long the pod should run before we take any action
	minRestartCount = 3               // Minimum number of restarts before we intervene
	maxRetryCount   = 5               // Cap the number of restart attempts to avoid looping forever
	retryAnnotation = "pod-restart/retry-count"
)

// List of reasons for restartable and non-restartable errors
var restartableErrorReasons = []string{
	"CrashLoopBackOff", "OOMKilled", "RunContainerError",
}

var nonRestartableErrorReasons = []string{
	"ImagePullBackOff", "ErrImagePull", "InvalidImageName", "CreateContainerError",
}

type PodReconciler struct {
	client.Client
	PrometheusAPI v1.API
}

// Reconcile is our core logic for monitoring pod health and managing restarts.
func (r *PodReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	// Fetching the pod
	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// If the pod is already being deleted, we don't need to take any further action
	if pod.DeletionTimestamp != nil {
		logger.Info("Pod is being deleted, no action needed", "pod", pod.Name)
		return reconcile.Result{}, nil
	}

	// Skip if the pod is not in Running or Failed phase
	if !isRunningOrFailed(&pod) {
		logger.Info("Pod is not in Running or Failed phase, skipping", "pod", pod.Name, "phase", pod.Status.Phase)
		return reconcile.Result{}, nil
	}

	// Skip if the pod is too new to act upon
	if podAge := time.Since(pod.CreationTimestamp.Time); podAge < minPodAge {
		logger.Info("Pod is too new to act upon", "pod", pod.Name, "age", podAge)
		return reconcile.Result{RequeueAfter: minPodAge - podAge}, nil
	}

	// Check pod container statuses
	needsRestart, errorReason, totalRestartCount := r.checkContainerStatus(pod.Status.ContainerStatuses)

	if errorReason != "" {
		return r.handlePodError(ctx, &pod, needsRestart, errorReason, totalRestartCount)
	}

	// No action needed if there are no errors
	return reconcile.Result{}, nil
}

// checkContainerStatus will check the container statuses for restartable or non-restartable errors.
func (r *PodReconciler) checkContainerStatus(containerStatuses []corev1.ContainerStatus) (bool, string, int) {
	totalRestartCount := 0
	for _, cs := range containerStatuses {
		totalRestartCount += int(cs.RestartCount)

		if cs.State.Waiting != nil {
			if isRestartableError(cs.State.Waiting.Reason) {
				return true, cs.State.Waiting.Reason, totalRestartCount
			}
			if isNonRestartableError(cs.State.Waiting.Reason) {
				return false, cs.State.Waiting.Reason, totalRestartCount
			}
		}

		if cs.State.Terminated != nil {
			if isRestartableError(cs.State.Terminated.Reason) {
				return true, cs.State.Terminated.Reason, totalRestartCount
			}
			if isNonRestartableError(cs.State.Terminated.Reason) {
				return false, cs.State.Terminated.Reason, totalRestartCount
			}
		}
	}
	return false, "", totalRestartCount
}

// handlePodError handles the pod errors by either restarting the pod or logging a non-restartable error.
func (r *PodReconciler) handlePodError(ctx context.Context, pod *corev1.Pod, needsRestart bool, errorReason string, totalRestartCount int) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	if needsRestart && totalRestartCount >= minRestartCount {
		// Checking the retry count before restarting
		retryCount, err := r.getRetryCount(pod)
		if err != nil {
			logger.Error(err, "Failed to get retry count", "pod", pod.Name)
			return reconcile.Result{}, err
		}

		if retryCount >= maxRetryCount {
			logger.Info("Max retry count reached, not restarting pod", "pod", pod.Name)
			return reconcile.Result{}, nil
		}

		// Increment the retry count and update the pod annotations
		r.incrementRetryCount(ctx, pod, retryCount)

		// Delete the pod to trigger a restart
		logger.Info("Restarting pod due to error", "pod", pod.Name, "reason", errorReason)
		if err := r.Delete(ctx, pod); err != nil {
			logger.Error(err, "Failed to delete pod", "pod", pod.Name)
			return reconcile.Result{}, err
		}
		return reconcile.Result{RequeueAfter: 30 * time.Second}, nil

	} else if !needsRestart {
		logger.Info("Non-restartable error detected", "pod", pod.Name, "reason", errorReason)
		return reconcile.Result{RequeueAfter: time.Hour}, nil
	}

	logger.Info("Pod restart not warranted yet", "pod", pod.Name, "restartCount", totalRestartCount)
	return reconcile.Result{}, nil
}

func isRunningOrFailed(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodRunning || pod.Status.Phase == corev1.PodFailed
}

func isRestartableError(reason string) bool {
	return contains(restartableErrorReasons, reason)
}

func isNonRestartableError(reason string) bool {
	return contains(nonRestartableErrorReasons, reason)
}

func (r *PodReconciler) getRetryCount(pod *corev1.Pod) (int, error) {
	retryCount := 0
	if val, exists := pod.Annotations[retryAnnotation]; exists {
		return strconv.Atoi(val)
	}
	return retryCount, nil
}

func (r *PodReconciler) incrementRetryCount(ctx context.Context, pod *corev1.Pod, retryCount int) {
	if pod.Annotations == nil {
		pod.Annotations = make(map[string]string)
	}
	pod.Annotations[retryAnnotation] = strconv.Itoa(retryCount + 1)
	r.Update(ctx, pod)
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// sets up the controller with the Manager
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Complete(r)
}
