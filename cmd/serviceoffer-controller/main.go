package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/serviceoffercontroller"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

func main() {
	kubeconfig := flag.String("kubeconfig", "", "Path to kubeconfig for out-of-cluster runs")
	workers := flag.Int("workers", 4, "Number of reconcile workers")
	leaderElection := flag.Bool("leader-election", true, "Enable lease-based leader election")
	leaderElectionNamespace := flag.String("leader-election-namespace", getenvDefault("POD_NAMESPACE", "x402"), "Namespace for the leader election Lease")
	leaderElectionName := flag.String("leader-election-name", "serviceoffer-controller", "Name of the leader election Lease")
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

	run := func(runCtx context.Context) error {
		return controller.Run(runCtx, *workers)
	}

	var errRun error
	if *leaderElection {
		errRun = runWithLeaderElection(ctx, cfg, *leaderElectionNamespace, *leaderElectionName, run)
	} else {
		errRun = run(ctx)
	}
	if errRun != nil {
		log.Fatalf("run controller: %v", errRun)
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

func runWithLeaderElection(ctx context.Context, cfg *rest.Config, namespace, name string, run func(context.Context) error) error {
	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("create kubernetes clientset: %w", err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("resolve hostname: %w", err)
	}

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Client: clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: fmt.Sprintf("%s-%d", hostname, time.Now().UnixNano()),
		},
	}

	errCh := make(chan error, 1)
	go leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   15 * time.Second,
		RenewDeadline:   10 * time.Second,
		RetryPeriod:     2 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(runCtx context.Context) {
				if err := run(runCtx); err != nil {
					select {
					case errCh <- err:
					default:
					}
				}
			},
			OnStoppedLeading: func() {
				if ctx.Err() == nil {
					select {
					case errCh <- fmt.Errorf("leader election lost"):
					default:
					}
				}
			},
		},
	})

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
