package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/serviceoffercontroller"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig for out-of-cluster runs")
	workers := flag.Int("workers", 1, "Number of reconcile workers")
	flag.Parse()

	cfg, err := loadConfig(*kubeconfig)
	if err != nil {
		log.Fatalf("load kubernetes config: %v", err)
	}

	controller, err := serviceoffercontroller.New(cfg)
	if err != nil {
		log.Fatalf("create controller: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := controller.Run(ctx, *workers); err != nil {
		log.Fatalf("run controller: %v", err)
	}
}

func loadConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	if env := os.Getenv("KUBECONFIG"); env != "" {
		return clientcmd.BuildConfigFromFlags("", env)
	}
	return rest.InClusterConfig()
}
