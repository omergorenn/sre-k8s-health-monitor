package controller

import (
	"context"
	"fmt"
	"github.com/omergorenn/sre-k8s-health-monitor/pkg/config"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/streadway/amqp"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"time"
)

type NodeReconciler struct {
	client.Client
	PrometheusAPI v1.API
	Config        *config.Config // Use the Config struct to store both configs and secrets
}

func NewNodeReconciler(mgr manager.Manager, promAPI v1.API, appConfig *config.Config) *NodeReconciler {
	return &NodeReconciler{
		Client:        mgr.GetClient(),
		PrometheusAPI: promAPI,
		Config:        appConfig, // Pass appConfig to use in reconciler
	}
}

func (r *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	const cpuThreshold = 0.0
	const memoryThreshold = 0.0

	logger := log.FromContext(ctx)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Retrieve the node's internal IP
	nodeInternalIP, err := getNodeInternalIP(&node)
	if err != nil {
		logger.Error(err, "Failed to get internal IP for node", "node", node.Name)
		return reconcile.Result{}, err
	}
	prometheusInstance := fmt.Sprintf("%s:9100", nodeInternalIP)

	// Query Prometheus for node CPU and memory usage
	cpuQuery := fmt.Sprintf(`100 - (avg by (instance) (rate(node_cpu_seconds_total{instance="%s", mode="idle"}[5m])) * 100)`, prometheusInstance)
	cpuResult, err := r.queryPrometheus(cpuQuery)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for CPU usage")
		return reconcile.Result{}, err
	}

	memoryQuery := fmt.Sprintf(`(node_memory_MemTotal_bytes{instance="%s"} - node_memory_MemAvailable_bytes{instance="%s"}) / node_memory_MemTotal_bytes{instance="%s"} * 100`, prometheusInstance, prometheusInstance, prometheusInstance)
	memoryResult, err := r.queryPrometheus(memoryQuery)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for memory usage")
		return reconcile.Result{}, err
	}

	// Check if the node is hitting limits
	if r.isNodeUnderPressure(cpuResult, cpuThreshold) || r.isNodeUnderPressure(memoryResult, memoryThreshold) {
		// Create an event in RabbitMQ
		message := fmt.Sprintf("Node %s is reaching its resource limits", node.Name)
		err := r.createEventToQueue(message)
		if err != nil {
			logger.Error(err, "Failed to send event to RabbitMQ")
		} else {
			logger.Info("Event sent to queue", "message", message)
		}
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
		fmt.Printf("Prometheus warnings: %v\n", warnings)
	}

	return result, nil
}

func (r *NodeReconciler) isNodeUnderPressure(result model.Value, threshold float64) bool {
	vector, ok := result.(model.Vector)
	if !ok {
		fmt.Println("Prometheus query result is not a vector.")
		return false
	}

	for _, sample := range vector {
		if float64(sample.Value) > threshold {
			return true
		}
	}
	return false
}

func (r *NodeReconciler) createEventToQueue(message string) error {

	rabbitmqUser := r.Config.Secret.RabbitMqCredentials.User
	rabbitmqPassword := r.Config.Secret.RabbitMqCredentials.Password
	rabbitmqHost := r.Config.RabbitMq.Host
	rabbitmqPort := r.Config.RabbitMq.Port

	connStr := fmt.Sprintf("amqp://%s:%s@%s:%s/",
		rabbitmqUser, rabbitmqPassword, rabbitmqHost, rabbitmqPort)

	conn, err := amqp.Dial(connStr)
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open a channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"node_alerts",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to declare a queue: %v", err)
	}

	err = ch.Publish(
		"",
		q.Name,
		false,
		false,
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(message),
		})
	if err != nil {
		return fmt.Errorf("failed to publish a message: %v", err)
	}

	return nil
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
