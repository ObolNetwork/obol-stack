package ui

import (
	"bytes"
	"slices"
	"strings"
	"testing"

	"github.com/urfave/cli/v3"
)

func TestFindSimilarCommands(t *testing.T) {
	cmds := []*cli.Command{
		{Name: "stack"},
		{Name: "sell"},
		{Name: "tunnel"},
		{Name: "secret", Hidden: true},
		{Name: "agent", Aliases: []string{"agents"}},
		{Name: "helm"},
		{Name: "hermes"},
	}
	for _, tc := range []struct {
		in   string
		want []string
	}{
		{"stak", []string{"stack"}},
		{"sel", []string{"sell", "helm"}},
		{"herme", []string{"hermes", "helm"}},
		{"tun", []string{"tunnel"}},
		{"agnet", []string{"agent"}},
		{"secre", nil},
		{"zzzzzz", nil},
	} {
		got := findSimilarCommands(cmds, tc.in, 2)
		if !slices.Equal(got, tc.want) {
			t.Errorf("findSimilarCommands(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSuggestCommandOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	u := NewForTest(&stdout, &stderr)
	root := &cli.Command{Name: "obol", Commands: []*cli.Command{{Name: "stack"}}}

	u.SuggestCommand(root, "stak")

	out := stderr.String()
	for _, want := range []string{"unknown command: obol stak", "Did you mean?", "obol stack", "obol --help"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr missing %q:\n%s", want, out)
		}
	}
	if stdout.Len() != 0 {
		t.Errorf("suggestions must go to stderr, stdout got %q", stdout.String())
	}
}
