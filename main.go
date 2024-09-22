package main

import (
	"fmt"

	"github.com/omergorenn/sre-k8s-health-monitor/pkg/config"
	"github.com/omergorenn/sre-k8s-health-monitor/pkg/controller"
	"github.com/omergorenn/sre-k8s-health-monitor/pkg/rabbitmq"
	"github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"
	"go.uber.org/zap"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	appConfig, err := config.Get()
	if err != nil {
		zap.L().Fatal("Error getting configs", zap.Error(err))
	}

	restConfig, err := ctrl.GetConfig()
	if err != nil {
		zap.L().Fatal("Failed to get kubeconfig", zap.Error(err))
	}

	// Set up logging for ctrl runtime.
	ctrl.SetLogger(ctrlzap.New())

	// Enable leader election for multiple instances
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		LeaderElection:          true,
		LeaderElectionID:        "health-monitor-leader-election",
		LeaderElectionNamespace: "default",
	})
	if err != nil {
		zap.L().Fatal("Failed to start manager", zap.Error(err))
	}

	// Create Prometheus client
	promClient, err := api.NewClient(api.Config{
		Address: fmt.Sprintf("%s:%s", appConfig.Prometheus.Host, appConfig.Prometheus.Port),
	})
	if err != nil {
		zap.L().Fatal("Failed to create prometheus client", zap.Error(err))
	}

	promAPI := v1.NewAPI(promClient)
	rabbitClient, err := rabbitmq.NewClient(appConfig)
	if err != nil {
		zap.L().Fatal("Failed to create rabbit client", zap.Error(err))
	}

	// Setup PodReconciler
	podReconciler := controller.NewPodReconciler(mgr, promAPI)
	if err = podReconciler.SetupWithManager(mgr); err != nil {
		zap.L().Fatal("Failed to setup pod reconciler", zap.Error(err))

	}

	// Setup NodeReconciler with config
	nodeReconciler := controller.NewNodeReconciler(mgr, promAPI, *rabbitClient)
	if err = nodeReconciler.SetupWithManager(mgr); err != nil {
		zap.L().Fatal("Failed to setup node reconciler", zap.Error(err))
	}
	// Start the manager
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		zap.L().Fatal("Failed to start manager", zap.Error(err))
	}
}
