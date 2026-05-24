package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/serviceoffercontroller"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

const (
	defaultLockNamespace = "x402"
	leaseName            = "serviceoffer-controller"
	leaseDuration        = 30 * time.Second
	renewDeadline        = 20 * time.Second
	retryPeriod          = 5 * time.Second
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig for out-of-cluster runs")
	workers := flag.Int("workers", 1, "Number of reconcile workers")
	leaderElect := flag.Bool("leader-elect", true, "Acquire a Lease before running the reconcile loop (disable for local dev)")
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

	if !*leaderElect {
		if err := controller.Run(ctx, *workers); err != nil {
			log.Fatalf("run controller: %v", err)
		}
		return
	}

	runWithLeaderElection(ctx, cfg, controller, *workers)
}

func runWithLeaderElection(ctx context.Context, cfg *rest.Config, controller *serviceoffercontroller.Controller, workers int) {
	podName := os.Getenv("POD_NAME")
	if podName == "" {
		// Fall back so local dev (go run ./cmd/serviceoffer-controller --leader-elect=false)
		// still works if someone forgets the flag. Identity must be unique across
		// candidates — in real deployments the downward API supplies the pod name.
		podName = "serviceoffer-controller-local"
	}

	lockNamespace := os.Getenv("POD_NAMESPACE")
	if lockNamespace == "" {
		lockNamespace = defaultLockNamespace
	}

	lock, err := resourcelock.NewFromKubeconfig(
		resourcelock.LeasesResourceLock,
		lockNamespace,
		leaseName,
		resourcelock.ResourceLockConfig{
			Identity: podName,
		},
		cfg,
		renewDeadline,
	)
	if err != nil {
		log.Fatalf("create lease lock: %v", err)
	}

	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   leaseDuration,
		RenewDeadline:   renewDeadline,
		RetryPeriod:     retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Printf("serviceoffer-controller: became leader %s", podName)
				if err := controller.Run(ctx, workers); err != nil {
					log.Printf("controller run: %v", err)
				}
			},
			OnStoppedLeading: func() {
				// On lost leadership exit non-zero so the kubelet restarts the
				// pod and the next election starts from a clean state. Trying
				// to keep running without the lease would race the new leader.
				log.Printf("serviceoffer-controller: lost leadership %s", podName)
				os.Exit(1)
			},
			OnNewLeader: func(identity string) {
				if identity != podName {
					log.Printf("serviceoffer-controller: new leader is %s", identity)
				}
			},
		},
	})
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
