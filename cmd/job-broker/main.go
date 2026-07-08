// job-broker is the stack's async job service: it accepts paid requests the
// x402-verifier has already verified + settled (proxied here instead of the
// upstream for spec.async offers), replays them upstream with no
// client-facing deadline, and serves the free status pages + gated results
// under <offer>/jobs/*. See internal/jobbroker.
package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/ObolNetwork/obol-stack/internal/jobbroker"
)

func main() {
	listen := flag.String("listen", ":8090", "listen address")
	dbPath := flag.String("db", "/data/jobs.db", "SQLite database path (PVC-backed)")
	flag.Parse()

	store, err := jobbroker.OpenStore(*dbPath)
	if err != nil {
		log.Fatalf("job-broker: %v", err)
	}
	defer store.Close()

	srv := jobbroker.NewServer(store)
	stop := make(chan struct{})
	go srv.RunSweeper(10*time.Minute, stop)

	log.Printf("job-broker: listening on %s (db %s)", *listen, *dbPath)
	if err := http.ListenAndServe(*listen, srv.Handler()); err != nil {
		log.Fatalf("job-broker: %v", err)
	}
}
