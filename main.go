package main

import (
	"fmt"
	"github.com/omergorenn/sre-k8s-health-monitor/config"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"log"
	ctrl "sigs.k8s.io/controller-runtime"
)

func main() {
	appConfig, err := config.Get()
	if err != nil {
		log.Fatal("Error getting configs: ", err)
	}

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		log.Fatal("Failed to get kubeconfig: ", err)
	}

	// Set up logging for ctrl runtime.
	ctrl.SetLogger(zap.New())

	// Enable leader election for multiple instances
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		LeaderElection:          true,
		LeaderElectionID:        "health-monitor-leader-election",
		LeaderElectionNamespace: "default",
	})
	if err != nil {
		log.Fatal("Failed to start manager: ", err)
	}

	// Create Prometheus client
	promClient, err := api.NewClient(api.Config{
		Address: fmt.Sprintf("%s:%s", appConfig.Prometheus.Host, appConfig.Prometheus.Port),
	})
	if err != nil {
		log.Fatal("Failed to create prometheus client: ", err)
	}

	promAPI := v1.NewAPI(promClient)

	// Setup PodReconciler
	podReconciler := controller.NewPodReconciler(mgr, promAPI)
	if err = podReconciler.SetupWithManager(mgr); err != nil {
		log.Fatal("Failed to setup pod reconciler: ", err)
	}

	nodeReconciler := controller.NewNodeReconciler(mgr, promAPI)
	if err = nodeReconciler.SetupWithManager(mgr); err != nil {
		log.Fatal("Failed to setup node reconciler: ", err)
	}

	// Start the manager
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Fatal("Failed to start manager: ", err)
	}
}
