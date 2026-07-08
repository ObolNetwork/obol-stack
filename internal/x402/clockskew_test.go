package x402

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestMeasureClockSkew_DetectsOffset(t *testing.T) {
	// Server reports a Date two minutes in the past → local clock appears
	// two minutes ahead (positive skew).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().Add(-2*time.Minute).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	skew, err := MeasureClockSkew(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("MeasureClockSkew: %v", err)
	}

	// Date-header resolution is 1s and the probe adds latency; a ±5s
	// tolerance keeps this robust on slow CI machines.
	if skew < 2*time.Minute-5*time.Second || skew > 2*time.Minute+5*time.Second {
		t.Errorf("skew = %v, want ~2m", skew)
	}
}

func TestMeasureClockSkew_InSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Date", time.Now().UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	skew, err := MeasureClockSkew(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("MeasureClockSkew: %v", err)
	}

	if skew.Abs() > 5*time.Second {
		t.Errorf("skew = %v, want ~0 for an in-sync server", skew)
	}
	if skew.Abs() > ClockSkewWarnThreshold {
		t.Errorf("in-sync server must not cross the warn threshold, got %v", skew)
	}
}

func TestMeasureClockSkew_NoDateHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// httptest's default server writes a Date header automatically;
		// suppress it to simulate a facilitator that omits one.
		w.Header()["Date"] = nil
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if _, err := MeasureClockSkew(context.Background(), srv.URL); err == nil {
		t.Error("expected error when the response has no Date header")
	}
}

func TestMeasureClockSkew_Unreachable(t *testing.T) {
	if _, err := MeasureClockSkew(context.Background(), "http://127.0.0.1:1"); err == nil {
		t.Error("expected error for an unreachable facilitator")
	}
}
