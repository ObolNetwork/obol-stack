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
	WatchConfigWithHandler(ctx, path, interval, func(cfg *PricingConfig) error {
		if err := v.Reload(cfg); err != nil {
			log.Printf("x402-watcher: apply config failed: %v", err)
			return err
		}
		log.Printf("x402-watcher: config reloaded (%d routes)", len(cfg.Routes))
		return nil
	})
}

func WatchConfigWithHandler(ctx context.Context, path string, interval time.Duration, apply func(*PricingConfig) error) {
	if interval <= 0 {
		interval = 5 * time.Second
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
				continue
			}

			mod := info.ModTime()
			if mod.Equal(lastMod) {
				continue
			}

			lastMod = mod

			cfg, err := LoadConfig(path)
			if err != nil {
				log.Printf("x402-watcher: reload failed: %v", err)
				continue
			}

			if err := apply(cfg); err != nil {
				continue
			}
		}
	}
}
