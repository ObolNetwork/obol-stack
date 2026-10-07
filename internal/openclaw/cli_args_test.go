package openclaw

import (
	"reflect"
	"testing"
)

func TestCLIArgs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining []string
		want      []string
	}{
		{"separator only", []string{"--", "gateway", "health"}, []string{"gateway", "health"}},
		{"instance then separator", []string{"default", "--", "doctor"}, []string{"doctor"}},
		{"no separator", []string{"gateway", "call", "config.get"}, []string{"gateway", "call", "config.get"}},
		{"instance, no separator", []string{"default", "doctor"}, []string{"doctor"}},
		{"args before and after a later --", []string{"gateway", "call", "--", "--json"}, []string{"gateway", "call", "--", "--json"}},
		{"instance, separator, nested --", []string{"default", "--", "exec", "--", "ls"}, []string{"exec", "--", "ls"}},
		{"nothing", nil, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CLIArgs("default", tc.remaining); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("CLIArgs(%q) = %q, want %q", tc.remaining, got, tc.want)
			}
		})
	}
}
