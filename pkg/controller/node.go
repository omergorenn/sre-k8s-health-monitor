package controller

import (
	"context"
	"fmt"
	"time"

	"github.com/omergorenn/sre-k8s-health-monitor/pkg/rabbitmq"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"go.uber.org/zap"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

type NodeReconciler struct {
	client.Client
	PrometheusAPI  v1.API
	RabbitmqClient rabbitmq.RabbitClient
}

func NewNodeReconciler(mgr manager.Manager, promAPI v1.API, rabbitClient rabbitmq.RabbitClient) *NodeReconciler {
	return &NodeReconciler{
		Client:         mgr.GetClient(),
		PrometheusAPI:  promAPI,
		RabbitmqClient: rabbitClient,
	}
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	const cpuThreshold = 0.0
	const memoryThreshold = 0.0

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Retrieve the node's internal IP
	nodeInternalIP, err := getNodeInternalIP(&node)
	if err != nil {
		zap.L().Info("Failed to get internal IP for node", zap.String("node", node.Name), zap.Error(err))
		return reconcile.Result{}, err
	}
	prometheusInstance := fmt.Sprintf("%s:9100", nodeInternalIP)

	// Query Prometheus for node CPU and memory usage
	cpuQuery := fmt.Sprintf(`100 - (avg by (instance) (rate(node_cpu_seconds_total{instance="%s", mode="idle"}[5m])) * 100)`, prometheusInstance)
	cpuResult, err := r.queryPrometheus(cpuQuery)
	if err != nil {
		zap.L().Info("Failed to query Prometheus for CPU usage", zap.Error(err))
		return reconcile.Result{}, err
	}

	memoryQuery := fmt.Sprintf(`(node_memory_MemTotal_bytes{instance="%s"} - node_memory_MemAvailable_bytes{instance="%s"}) / node_memory_MemTotal_bytes{instance="%s"} * 100`, prometheusInstance, prometheusInstance, prometheusInstance)
	memoryResult, err := r.queryPrometheus(memoryQuery)
	if err != nil {
		zap.L().Info("Failed to query Prometheus for memory usage", zap.Error(err))
		return reconcile.Result{}, err
	}

	// Check if the node is hitting limits
	if r.isNodeUnderPressure(cpuResult, cpuThreshold) || r.isNodeUnderPressure(memoryResult, memoryThreshold) {
		message := fmt.Sprintf("Node %s is reaching its resource limits", node.Name)
		err = r.RabbitmqClient.PublishEvent("node_alerts", message)
		if err != nil {
			return reconcile.Result{}, err
		}
		zap.L().Info("Resource usage is low on the node, message sent to queue")

	}

	return reconcile.Result{}, nil
}

func (r *NodeReconciler) queryPrometheus(query string) (model.Value, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result, warnings, err := r.PrometheusAPI.Query(ctx, query, time.Now())
	if err != nil {
		return nil, err
	}
	if len(warnings) > 0 {
		zap.L().Info("Prometheus warnings", zap.Any("warnings", warnings))
	}

	return result, nil
}

func (r *NodeReconciler) isNodeUnderPressure(result model.Value, threshold float64) bool {
	vector, ok := result.(model.Vector)
	if !ok {
		zap.L().Info("Prometheus query result is not a vector")
		return false
	}

	for _, sample := range vector {
		if float64(sample.Value) > threshold {
			return true
		}
	}
	return false
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}

// Helper function to get the node's internal IP
func getNodeInternalIP(node *corev1.Node) (string, error) {
	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			return address.Address, nil
		}
	}
	return "", fmt.Errorf("internal IP not found for node %s", node.Name)
}
