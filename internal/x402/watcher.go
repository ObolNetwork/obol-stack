package x402

import (
	"context"
	"log"
	"os"
	"time"
)

// WatchConfig polls a YAML config file for changes and reloads the Verifier
// when the file is modified. It checks the file's modification time every
// interval. This handles ConfigMap volume mount updates (kubelet symlink swaps)
// without requiring fsnotify.
//
// WatchConfig blocks until the context is cancelled.
func WatchConfig(ctx context.Context, path string, v *Verifier, interval time.Duration) {
	WatchConfigWithHandler(ctx, path, interval, verifierReloader(v))
}

// verifierReloader returns the apply func WatchConfig uses to hot-swap v.
func verifierReloader(v *Verifier) func(*PricingConfig) error {
	return func(cfg *PricingConfig) error {
		if err := v.Reload(cfg); err != nil {
			log.Printf("x402-watcher: apply config failed: %v", err)
			return err
		}
		log.Printf("x402-watcher: config reloaded (%d routes)", len(cfg.Routes))
		return nil
	}
}

func WatchConfigWithHandler(ctx context.Context, path string, interval time.Duration, apply func(*PricingConfig) error) {
	watchConfig(ctx, path, interval, apply, nil)
}

// watchEvent is the outcome of one watcher poll. It exists so tests can
// synchronise on the watcher's progress instead of sleeping.
type watchEvent int

const (
	watchStatError watchEvent = iota
	watchUnchanged
	watchLoadError
	watchApplyError
	watchApplied
)

// watchConfig is the polling loop behind WatchConfigWithHandler. observe,
// when non-nil, is called after every poll with that poll's outcome.
func watchConfig(ctx context.Context, path string, interval time.Duration, apply func(*PricingConfig) error, observe func(watchEvent)) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if observe == nil {
		observe = func(watchEvent) {}
	}

	var lastMod time.Time

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				log.Printf("x402-watcher: stat %s: %v", path, err)
				observe(watchStatError)
				continue
			}

			mod := info.ModTime()
			if mod.Equal(lastMod) {
				observe(watchUnchanged)
				continue
			}

			lastMod = mod

			cfg, err := LoadConfig(path)
			if err != nil {
				log.Printf("x402-watcher: reload failed: %v", err)
				observe(watchLoadError)
				continue
			}

			if err := apply(cfg); err != nil {
				observe(watchApplyError)
				continue
			}
			observe(watchApplied)
		}
	}
}
