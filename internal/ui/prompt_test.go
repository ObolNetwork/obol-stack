package ui

import (
	"bytes"
	"strings"
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

// A destructive confirm with no one to answer must fail naming the skip flag,
// not decline silently and exit 0 as if the action ran.
func TestConfirmOrFlag_NonInteractiveErrorsWithFlag(t *testing.T) {
	u := NewForTest(&bytes.Buffer{}, &bytes.Buffer{})
	u.isTTY = false

	ok, err := u.ConfirmOrFlag("Delete it?", "--force")
	if ok || err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("ConfirmOrFlag without a TTY = (%v, %v); want (false, error naming --force)", ok, err)
	}
}

func TestStdinPromptable_OBOLNonInteractive(t *testing.T) {
	t.Setenv("OBOL_NONINTERACTIVE", "true")

	if StdinPromptable() {
		t.Fatal("StdinPromptable() = true with OBOL_NONINTERACTIVE=true; want false")
	}
}
