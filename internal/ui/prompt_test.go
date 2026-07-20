package ui

import (
	"bytes"
	"testing"
)

func TestIsInteractive_OBOLNonInteractive(t *testing.T) {
	u := NewForTest(&bytes.Buffer{}, &bytes.Buffer{})
	u.isTTY = true // force TTY so only the env guard can suppress interactivity

	if !u.isInteractive() {
		t.Fatal("isInteractive() = false with TTY, human output, and OBOL_NONINTERACTIVE unset; want true")
	}

	t.Setenv("OBOL_NONINTERACTIVE", "true")
	if u.isInteractive() {
		t.Fatal("isInteractive() = true with OBOL_NONINTERACTIVE=true; want false")
	}
}

func TestConfirm_OBOLNonInteractiveReturnsDefault(t *testing.T) {
	u := NewForTest(&bytes.Buffer{}, &bytes.Buffer{})
	u.isTTY = true
	t.Setenv("OBOL_NONINTERACTIVE", "true")

	if got := u.Confirm("proceed?", true); !got {
		t.Fatalf("Confirm(defaultYes=true) = %v; want true without reading stdin", got)
	}
	if got := u.Confirm("proceed?", false); got {
		t.Fatalf("Confirm(defaultYes=false) = %v; want false without reading stdin", got)
	}
}
