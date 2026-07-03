package main

import (
	"strings"
	"testing"
)

func TestResolveX402scanOrigin_Explicit(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		errPart string
	}{
		{in: "https://store.example.com", want: "https://store.example.com"},
		{in: "https://store.example.com/some/path", want: "https://store.example.com"},
		{in: "http://store.example.com", errPart: "must be https"},
		{in: "https://obol.stack:8080", errPart: "no public hostname"},
		{in: "https://localhost:8080", errPart: "no public hostname"},
		{in: "https://abc-def.trycloudflare.com", errPart: "quick-tunnel"},
		{in: "not a url", errPart: "invalid origin"},
	} {
		got, err := resolveX402scanOrigin(nil, tc.in)
		if tc.errPart != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errPart) {
				t.Fatalf("%q: expected error containing %q, got %v", tc.in, tc.errPart, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%q: %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
