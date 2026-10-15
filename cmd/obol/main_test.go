package main

import "testing"

func TestNotifiesVersionSkew(t *testing.T) {
	for cmd, want := range map[string]bool{
		"sell":    true,
		"network": true,
		"model":   true,
		"stack":   false, // stack up is the fix
		"doctor":  false, // reports it itself
		"version": false,
		"kubectl": false, // passthrough output stays clean
		"":        false,
	} {
		if got := notifiesVersionSkew(cmd); got != want {
			t.Errorf("notifiesVersionSkew(%q) = %v, want %v", cmd, got, want)
		}
	}
}
