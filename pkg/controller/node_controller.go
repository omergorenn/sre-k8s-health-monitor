package controller

import (
	"context"
	"fmt"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"time"

	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"github.com/prometheus/common/model"
	"github.com/streadway/amqp"
)

type NodeReconciler struct {
	client.Client
	PrometheusAPI v1.API
}

// Reconcile is our main loop that checks nodes and sends events to RabbitMQ if limits are exceeded
func (r *NodeReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var node corev1.Node
	if err := r.Get(ctx, req.NamespacedName, &node); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Query Prometheus for node CPU and memory usage
	cpuQuery := `100 - (avg by (instance) (rate(node_cpu_seconds_total{mode="idle"}[5m])) * 100)`
	cpuResult, err := r.queryPrometheus(cpuQuery)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for node CPU usage")
		return reconcile.Result{}, err
	}
	fmt.Printf("Prometheus CPU result for node %s: %v\n", node.Name, cpuResult)

	memoryQuery := `(node_memory_MemTotal_bytes - node_memory_MemAvailable_bytes) / node_memory_MemTotal_bytes * 100`
	memoryResult, err := r.queryPrometheus(memoryQuery)
	if err != nil {
		logger.Error(err, "Failed to query Prometheus for node memory usage")
		return reconcile.Result{}, err
	}
	fmt.Printf("Prometheus Memory result for node %s: %v\n", node.Name, memoryResult)

	// Setting low test thresholds to trigger the RabbitMQ event for testing
	cpuTestThreshold := 3.0
	memoryTestThreshold := 20.0

	// Check if the node is hitting limits based on the test thresholds
	if r.isNodeUnderPressure(cpuResult, node.Name, cpuTestThreshold) || r.isNodeUnderPressure(memoryResult, node.Name, memoryTestThreshold) {
		// Create an event in RabbitMQ
		message := fmt.Sprintf("Node %s is reaching its resource limits", node.Name)
		err := r.createEventToQueue(node.Name, message)
		if err != nil {
			logger.Error(err, "Failed to send event to RabbitMQ")
		} else {
			fmt.Printf("Event sent to queue: %s\n", message)
		}
	}

	return reconcile.Result{}, nil
}

// queryPrometheus func queries the Prometheus for the specified metric
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

// isNodeUnderPressure func checks if a node exceeds a specified threshold that we defined above.
func (r *NodeReconciler) isNodeUnderPressure(result model.Value, nodeName string, threshold float64) bool {
	fmt.Printf("Checking node %s with threshold %.2f\n", nodeName, threshold)

	vector, ok := result.(model.Vector)
	if !ok {
		fmt.Println("Prometheus query result is not a vector.")
		return false
	}

	// Get node IP or any other field to compare properly
	nodeInternalIP := "192.168.49.2:9100" // For testing, hardcoding internal IP. Ideally, fetch it dynamically

	for _, sample := range vector {
		// Check if the Prometheus instance matches either the node name or the internal IP
		if string(sample.Metric["instance"]) == nodeInternalIP || string(sample.Metric["instance"]) == nodeName {
			fmt.Printf("Node %s metric value: %v\n", string(sample.Metric["instance"]), float64(sample.Value))
			if float64(sample.Value) > threshold {
				fmt.Printf("Node %s is over the threshold %.2f\n", nodeName, threshold)
				return true
			}
		}
	}
	return false
}
func getNodeInternalIP(clientset *kubernetes.Clientset, nodeName string) (string, error) {
	node, err := clientset.CoreV1().Nodes().Get(context.TODO(), nodeName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	for _, address := range node.Status.Addresses {
		if address.Type == corev1.NodeInternalIP {
			return address.Address, nil
		}
	}
	return "", fmt.Errorf("internal IP not found for node %s", nodeName)
}

// createEventToQueue func sends an event to RabbitMQ when node reaches limits
func (r *NodeReconciler) createEventToQueue(nodeName string, message string) error {

	conn, err := amqp.Dial("amqp://user:<passwd>@localhost:5672/")
	if err != nil {
		return fmt.Errorf("failed to connect to RabbitMQ: %v", err)
	}
	defer conn.Close()

	fmt.Println("Successfully connected to RabbitMQ")

	ch, err := conn.Channel()
	if err != nil {
		return fmt.Errorf("failed to open a channel: %v", err)
	}
	defer ch.Close()

	q, err := ch.QueueDeclare(
		"node_alerts", // queue name
		false,         // durable
		false,         // delete when unused
		false,         // exclusive
		false,         // no-wait
		nil,           // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare a queue: %v", err)
	}

	err = ch.Publish(
		"",     // exchange
		q.Name, // routing key (queue name)
		false,  // mandatory
		false,  // immediate
		amqp.Publishing{
			ContentType: "text/plain",
			Body:        []byte(message),
		})
	if err != nil {
		return fmt.Errorf("failed to publish a message: %v", err)
	}

	fmt.Printf("Sent event to queue: %s\n", message)
	return nil
}

func (r *NodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		Complete(r)
}
