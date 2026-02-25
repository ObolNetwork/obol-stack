package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ObolNetwork/obol-stack/internal/inference"
	x402verifier "github.com/ObolNetwork/obol-stack/internal/x402"
)

func main() {
	listen := flag.String("listen", ":8402", "Listen address")
	upstream := flag.String("upstream", "http://ollama:11434", "Upstream inference service URL")
	wallet := flag.String("wallet", "", "USDC recipient wallet address (required)")
	price := flag.String("price", "0.001", "USDC price per request")
	chain := flag.String("chain", "base-sepolia", "Blockchain network (base, base-sepolia, polygon, polygon-amoy, avalanche, avalanche-fuji)")
	facilitator := flag.String("facilitator", "https://facilitator.x402.rs", "x402 facilitator URL")
	flag.Parse()

	if *wallet == "" {
		// Check environment variable
		*wallet = os.Getenv("X402_WALLET")
		if *wallet == "" {
			log.Fatal("--wallet flag or X402_WALLET env var required")
		}
	}
	if err := x402verifier.ValidateWallet(*wallet); err != nil {
		log.Fatalf("wallet: %v", err)
	}

	x402Chain, err := x402verifier.ResolveChain(*chain)
	if err != nil {
		log.Fatalf("chain: %v", err)
	}

	gw, err := inference.NewGateway(inference.GatewayConfig{
		ListenAddr:      *listen,
		UpstreamURL:     *upstream,
		WalletAddress:   *wallet,
		PricePerRequest: *price,
		Chain:           x402Chain,
		FacilitatorURL:  *facilitator,
	})
	if err != nil {
		log.Fatalf("failed to create gateway: %v", err)
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down...")
		if err := gw.Stop(); err != nil {
			log.Printf("shutdown error: %v", err)
		}
	}()

	if err := gw.Start(); err != nil {
		log.Fatalf("gateway error: %v", err)
	}
}
