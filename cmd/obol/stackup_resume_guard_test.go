package main

import (
	"os"
	"strings"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/replay"
)

// The recorded-state replay order (and its rationale) lives in
// internal/replay.Order and is pinned by internal/replay's tests. These
// source-level guards make sure both entrypoints — `obol stack up` and
// `obol sell resume` — run that ONE list instead of drifting back to
// hand-picked inline subsets (they previously diverged: sell resume
// skipped RPCs, the eRPC overlay, storefront and models).

// replayStepCalls are the per-step functions that must only be invoked via
// internal/replay, never inline in a CLI action.
var replayStepCalls = []string{
	"model.ReconcileRecorded(",
	"network.ReconcileRecordedRPCs(",
	"network.ReconcileERPCOverlay(",
	"network.ResumeInstalled(",
	"agentcrd.ResumeAll(",
	"storefront.ReconcileRecorded(",
	"app.ResumeAll(",
	"agentsync.SyncInstances(",
}

func TestStackUpAction_ReplaysRecordedState(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(src)

	upIdx := strings.Index(body, "stack.Up(cfg")
	replayIdx := strings.Index(body, "replay.ReplayRecorded(ctx, cfg, u, replayOptions())")
	if upIdx < 0 || replayIdx < 0 {
		t.Fatalf("stack up must call stack.Up then replay.ReplayRecorded(ctx, cfg, u, replayOptions()); upIdx=%d replayIdx=%d", upIdx, replayIdx)
	}
	if replayIdx < upIdx {
		t.Error("recorded-state replay must run AFTER stack.Up — before it there is no kubeconfig/cluster")
	}
	for _, call := range replayStepCalls {
		if strings.Contains(body, call) {
			t.Errorf("main.go calls %s inline; add it to internal/replay.Order instead", call)
		}
	}
	if strings.Contains(body, "resumeSellOffers(ctx") {
		t.Error("main.go must reach resumeSellOffers only through replayOptions()")
	}
}

func TestSellResumeAction_UsesSharedReplayList(t *testing.T) {
	src, err := os.ReadFile("sell.go")
	if err != nil {
		t.Fatalf("read sell.go: %v", err)
	}
	body := string(src)

	start := strings.Index(body, "func sellResumeCommand(")
	if start < 0 {
		t.Fatal("sellResumeCommand not found")
	}
	end := strings.Index(body[start:], "\n}\n")
	action := body[start : start+end]

	if !strings.Contains(action, "replay.ReplayRecorded(ctx, cfg, u, replayOptions())") {
		t.Fatal("sell resume must run replay.ReplayRecorded with the shared replayOptions()")
	}
	if !strings.Contains(action, "sum.Errors[replay.StepSellOffers]") {
		t.Error("sell resume must still fail when the offer replay itself fails")
	}
	if !strings.Contains(action, "install-boot-unit") {
		t.Error("sell resume must keep --install-boot-unit")
	}
	for _, call := range append(replayStepCalls, "resumeSellOffers(ctx") {
		if strings.Contains(action, call) {
			t.Errorf("sell resume calls %s inline; it must come from the shared replay list", call)
		}
	}
}

// TestReplayOptions_WiresSellOffers: the shared options must carry the
// package-main offer replay, otherwise the last step silently no-ops.
func TestReplayOptions_WiresSellOffers(t *testing.T) {
	if replayOptions().ResumeSellOffers == nil {
		t.Fatal("replayOptions().ResumeSellOffers must be resumeSellOffers")
	}
	steps := replay.Steps(replayOptions())
	if steps[len(steps)-1].Name != replay.StepSellOffers {
		t.Fatalf("last replay step = %s, want %s", steps[len(steps)-1].Name, replay.StepSellOffers)
	}
}
