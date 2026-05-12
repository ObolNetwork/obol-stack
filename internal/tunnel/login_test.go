package tunnel

import (
	"reflect"
	"testing"
)

func TestRouteDNSArgs(t *testing.T) {
	tests := []struct {
		name       string
		tunnelName string
		hostname   string
		overwrite  bool
		want       []string
	}{
		{
			name:       "default (no overwrite)",
			tunnelName: "obol-stack-foo",
			hostname:   "inference.example.com",
			overwrite:  false,
			want:       []string{"tunnel", "route", "dns", "obol-stack-foo", "inference.example.com"},
		},
		{
			name:       "overwrite-dns inserted before tunnel/hostname",
			tunnelName: "obol-stack-foo",
			hostname:   "inference.example.com",
			overwrite:  true,
			want:       []string{"tunnel", "route", "dns", "--overwrite-dns", "obol-stack-foo", "inference.example.com"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := routeDNSArgs(tc.tunnelName, tc.hostname, tc.overwrite)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("routeDNSArgs(%q, %q, %v) = %v; want %v",
					tc.tunnelName, tc.hostname, tc.overwrite, got, tc.want)
			}
		})
	}
}
