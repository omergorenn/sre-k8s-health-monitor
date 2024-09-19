package main

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func main() {
	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{})
	if err != nil {
		panic("Failed to start manager")
	}

	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		panic("Failed to start manager")
	}
}
