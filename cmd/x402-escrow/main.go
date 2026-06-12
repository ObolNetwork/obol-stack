// Command x402-escrow is the escrow facilitator for ServiceBounty rewards:
// it verifies and holds Permit2 batch-transfer vouchers (reserve), settles
// them on-chain via permitTransferFrom (capture), and drops them store-only
// (void — the voucher deadline is the hard on-chain guarantee).
//
// Configuration is environment-driven:
//
//	OBOL_ESCROW_TOKEN       bearer token for /escrow/* (empty = no auth, dev only)
//	OBOL_ESCROW_STATE_DIR   file-backed JSON state dir (default /data)
//	OBOL_ESCROW_KEY         hex private key for local settlement signing
//	OBOL_ESCROW_SIGNER_URL  remote-signer base URL (used when no key is set)
//	OBOL_ESCROW_RPC_BASE    per-network JSON-RPC base (default in-cluster eRPC)
//	OBOL_ESCROW_NETWORKS    csv chain aliases served (default base,base-sepolia)
//
// With neither key nor signer URL, capture returns 503 while reserve/void
// keep working (vouchers cannot be verified either — the spender binding has
// nothing to bind to).
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"

	"github.com/ObolNetwork/obol-stack/internal/erc8004"
	"github.com/ObolNetwork/obol-stack/internal/x402/escrow"
)

type config struct {
	Token     string
	StateDir  string
	KeyHex    string
	SignerURL string
	RPCBase   string
	Networks  []string
}

// loadConfig resolves the environment with defaults; get is injectable for
// tests (pass os.Getenv in main).
func loadConfig(get func(string) string) config {
	cfg := config{
		Token:     get("OBOL_ESCROW_TOKEN"),
		StateDir:  get("OBOL_ESCROW_STATE_DIR"),
		KeyHex:    get("OBOL_ESCROW_KEY"),
		SignerURL: get("OBOL_ESCROW_SIGNER_URL"),
		RPCBase:   get("OBOL_ESCROW_RPC_BASE"),
	}
	if cfg.StateDir == "" {
		cfg.StateDir = "/data"
	}
	if cfg.RPCBase == "" {
		cfg.RPCBase = erc8004.DefaultRPCBase
	}
	csv := get("OBOL_ESCROW_NETWORKS")
	if csv == "" {
		csv = "base,base-sepolia"
	}
	for _, part := range strings.Split(csv, ",") {
		if n := strings.TrimSpace(part); n != "" {
			cfg.Networks = append(cfg.Networks, n)
		}
	}
	return cfg
}

func main() {
	listen := flag.String("listen", ":8403", "Listen address")
	flag.Parse()

	cfg := loadConfig(os.Getenv)

	store, err := escrow.NewStore(cfg.StateDir)
	if err != nil {
		log.Fatalf("open state dir: %v", err)
	}

	var spender common.Address
	var submitter escrow.Submitter
	switch {
	case cfg.KeyHex != "":
		key, err := crypto.HexToECDSA(strings.TrimPrefix(strings.TrimPrefix(cfg.KeyHex, "0x"), "0X"))
		if err != nil {
			log.Fatalf("parse OBOL_ESCROW_KEY: %v", err)
		}
		spender = crypto.PubkeyToAddress(key.PublicKey)
		submitter = &escrow.EthSubmitter{RPCBase: cfg.RPCBase, Key: key}
		log.Printf("settling locally as %s", spender.Hex())
	case cfg.SignerURL != "":
		signer := erc8004.NewRemoteSigner(cfg.SignerURL)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		addr, err := signer.GetAddress(ctx)
		cancel()
		if err != nil {
			log.Fatalf("resolve remote signer address at %s: %v", cfg.SignerURL, err)
		}
		spender = addr
		submitter = &escrow.EthSubmitter{RPCBase: cfg.RPCBase, Signer: signer, SignerAddress: addr}
		log.Printf("settling via remote signer %s as %s", cfg.SignerURL, spender.Hex())
	default:
		log.Printf("no OBOL_ESCROW_KEY or OBOL_ESCROW_SIGNER_URL: capture disabled, reserve/void still served")
	}
	if cfg.Token == "" {
		log.Printf("warning: OBOL_ESCROW_TOKEN is empty — escrow routes are unauthenticated (mirrors HTTPGateway omitting the Authorization header)")
	}

	srv := escrow.NewServer(store, escrow.ServerOptions{
		Token:     cfg.Token,
		Spender:   spender,
		Networks:  cfg.Networks,
		Submitter: submitter,
	})

	server := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("x402-escrow listening on %s (state: %s, networks: %s)", *listen, cfg.StateDir, strings.Join(cfg.Networks, ", "))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}
