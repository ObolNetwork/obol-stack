package stack

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

func TestCheckPortsAvailable_FreePorts(t *testing.T) {
	// Use high ephemeral ports that are almost certainly free
	ports := []int{19876, 19877}
	if err := checkPortsAvailable(ports); err != nil {
		t.Fatalf("expected no error for free ports, got: %v", err)
	}
}

func TestCheckPortsAvailable_BlockedPort(t *testing.T) {
	// Bind a port to simulate a conflict
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	// Extract the port number from the listener address
	addr := ln.Addr().(*net.TCPAddr)
	blockedPort := addr.Port

	err = checkPortsAvailable([]int{blockedPort})
	if err == nil {
		t.Fatal("expected error for blocked port, got nil")
	}

	portStr := fmt.Sprintf("%d", blockedPort)
	if !strings.Contains(err.Error(), portStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Errorf("error should mention 'already in use', got: %v", err)
	}
	if !strings.Contains(err.Error(), "sudo lsof") {
		t.Errorf("error should include remediation hint, got: %v", err)
	}
}

func TestCheckPortsAvailable_MixedPorts(t *testing.T) {
	// Bind one port, leave another free
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind ephemeral port: %v", err)
	}
	defer ln.Close()

	blockedPort := ln.Addr().(*net.TCPAddr).Port

	// Pick a free port by briefly binding and releasing
	ln2, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("failed to bind second ephemeral port: %v", err)
	}
	freePort := ln2.Addr().(*net.TCPAddr).Port
	ln2.Close()

	err = checkPortsAvailable([]int{freePort, blockedPort})
	if err == nil {
		t.Fatal("expected error when one port is blocked, got nil")
	}

	// Should mention only the blocked port
	blockedStr := fmt.Sprintf("%d", blockedPort)
	if !strings.Contains(err.Error(), blockedStr) {
		t.Errorf("error should mention blocked port %d, got: %v", blockedPort, err)
	}
}

func TestFormatPorts(t *testing.T) {
	tests := []struct {
		ports    []int
		expected string
	}{
		{[]int{443}, "443"},
		{[]int{80, 443}, "80, 443"},
		{[]int{80, 8080, 443, 8443}, "80, 8080, 443, 8443"},
	}
	for _, tt := range tests {
		got := formatPorts(tt.ports)
		if got != tt.expected {
			t.Errorf("formatPorts(%v) = %q, want %q", tt.ports, got, tt.expected)
		}
	}
}
