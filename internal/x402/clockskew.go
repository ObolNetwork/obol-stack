package x402

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ClockSkewWarnThreshold is the local-vs-facilitator clock offset above
// which sellers should be warned. EIP-3009 authorizations are signed with
// validAfter/validBefore anchored to the signer's wall clock; a drifted
// host makes the facilitator (and USDC's FiatTokenV2 contract) reject
// otherwise-valid payments with "authorization is not yet valid" —
// revenue silently stops while everything looks healthy. The HTTP Date
// header has 1s resolution and rides normal request latency, so the
// threshold stays well above measurement noise.
const ClockSkewWarnThreshold = 30 * time.Second

// clockSkewProbeTimeout bounds the skew probe so preflight checks never
// hold up a sell command on a slow network.
const clockSkewProbeTimeout = 3 * time.Second

// MeasureClockSkew measures the local clock's offset from the
// facilitator's clock via the HTTP Date response header: positive means
// the local clock is ahead. The request midpoint is used as the local
// reference to cancel out request latency. Returns an error when the
// facilitator is unreachable or sends no Date header — callers treat the
// probe as best-effort.
func MeasureClockSkew(ctx context.Context, facilitatorURL string) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, clockSkewProbeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, facilitatorURL, nil)
	if err != nil {
		return 0, fmt.Errorf("build skew probe: %w", err)
	}

	start := time.Now()

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("skew probe: %w", err)
	}
	defer resp.Body.Close()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))

	elapsed := time.Since(start)

	dateHeader := resp.Header.Get("Date")
	if dateHeader == "" {
		return 0, errors.New("facilitator response has no Date header")
	}

	serverTime, err := http.ParseTime(dateHeader)
	if err != nil {
		return 0, fmt.Errorf("parse facilitator Date header: %w", err)
	}

	localMidpoint := start.Add(elapsed / 2)

	return localMidpoint.Sub(serverTime), nil
}
