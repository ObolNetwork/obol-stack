package network

import (
	"errors"
	"runtime"
	"testing"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

func TestCheckClientArch(t *testing.T) {
	cases := []struct {
		client, arch string
		wantErr      bool
	}{
		{"silver", "amd64", false},
		{"silver", "arm64", true},
		{"lighthouse", "arm64", false},
		{"lighthouse", "amd64", false},
	}
	for _, c := range cases {
		err := checkClientArch(c.client, c.arch)
		if (err != nil) != c.wantErr {
			t.Errorf("checkClientArch(%q, %q) = %v, want error %v", c.client, c.arch, err, c.wantErr)
		}
	}
}

func TestTargetArch(t *testing.T) {
	orig := nodeArchsFn
	t.Cleanup(func() { nodeArchsFn = orig })

	cases := []struct {
		name  string
		archs []string
		err   error
		want  string
	}{
		{"amd64 node", []string{"amd64"}, nil, "amd64"},
		{"arm64 node", []string{"arm64"}, nil, "arm64"},
		{"mixed nodes", []string{"amd64", "arm64"}, nil, "arm64"},
		{"cluster down", nil, errors.New("connection refused"), runtime.GOARCH},
		{"no nodes", nil, nil, runtime.GOARCH},
	}
	for _, c := range cases {
		nodeArchsFn = func(*config.Config) ([]string, error) { return c.archs, c.err }
		if got := targetArch(&config.Config{}); got != c.want {
			t.Errorf("%s: targetArch = %q, want %q", c.name, got, c.want)
		}
	}
}
