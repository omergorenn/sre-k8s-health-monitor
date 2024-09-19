package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/omergorenn/sre-k8s-health-monitor/pkg/controller"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	kubeconfig := filepath.Join(homedir.HomeDir(), ".kube", "config")
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading kubeconfig: %s\n", err)
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(config, ctrl.Options{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start manager: %s\n", err)
		os.Exit(1)
	}

	promClient, err := api.NewClient(api.Config{
		Address: "http://prometheus.monitoring.svc.cluster.local:9090",
	})

	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create Prometheus client: %s\n", err)
		os.Exit(1)
	}
	promAPI := v1.NewAPI(promClient)

	// Setup PodReconciler (to handle pod restarts based on errors)
	if err := (&controller.PodReconciler{
		Client:        mgr.GetClient(),
		PrometheusAPI: promAPI,
	}).SetupWithManager(mgr); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set up PodReconciler: %s\n", err)
		os.Exit(1)
	}

	// Start the manager
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to start manager: %s\n", err)
		os.Exit(1)
	}
}
