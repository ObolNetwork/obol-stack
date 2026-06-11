package main

import (
	"os"
	"strings"
	"testing"
)

// TestStackUpAction_ReplaysRecordedState is a source-level guard for the
// Phase 2 record-on-write wiring (plans/stack-export-import.md), in the same
// spirit as TestStackUpAction_CallsResumeSellOffers: the `stack up` action
// must replay recorded RPC upstreams and recorded Agent CRs after stack.Up,
// and the Agent CR replay must come BEFORE sell-offer resume — agent-backed
// ServiceOffers resolve agent.ref and dangle without their Agent.
func TestStackUpAction_ReplaysRecordedState(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)

	upIdx := strings.Index(body, "stack.Up(cfg")
	rpcIdx := strings.Index(body, "network.ReconcileRecordedRPCs(")
	agentsIdx := strings.Index(body, "agentcrd.ResumeAll(")
	offersIdx := strings.Index(body, "resumeSellOffers(")

	if rpcIdx < 0 {
		t.Fatal("cmd/obol/main.go must call network.ReconcileRecordedRPCs — without it recorded remote RPCs never reach a freshly-recreated cluster")
	}
	if agentsIdx < 0 {
		t.Fatal("cmd/obol/main.go must call agentcrd.ResumeAll — without it recorded Agent CRs never reach a freshly-recreated cluster")
	}
	if upIdx < 0 || offersIdx < 0 {
		t.Fatalf("expected stack.Up and resumeSellOffers in main.go; upIdx=%d offersIdx=%d", upIdx, offersIdx)
	}
	if rpcIdx < upIdx || agentsIdx < upIdx {
		t.Error("recorded-state replay must run AFTER stack.Up — before it there is no kubeconfig/cluster")
	}
	if agentsIdx > offersIdx {
		t.Error("agentcrd.ResumeAll must run BEFORE resumeSellOffers — agent-backed ServiceOffers need their Agent CR first")
	}
}
