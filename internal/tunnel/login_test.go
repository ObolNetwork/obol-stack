package tunnel

import (
	"reflect"
	"strings"
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

func TestVerifyRoutedHostname(t *testing.T) {
	tests := []struct {
		name        string
		output      string
		requested   string
		wantErr     bool
		wantInError []string
	}{
		{
			name:      "added CNAME success line with matching hostname",
			output:    "2026-05-12T10:00:00Z INF Added CNAME inference.example.com which will route to this tunnel tunnelID=f37ef341-1234-5678-9abc-def012345678",
			requested: "inference.example.com",
			wantErr:   false,
		},
		{
			name:      "already-configured line with matching hostname",
			output:    "2026-05-12T10:00:00Z INF inference.example.com is already configured to route to your tunnel tunnelID=f37ef341-1234-5678-9abc-def012345678",
			requested: "inference.example.com",
			wantErr:   false,
		},
		{
			name:        "added CNAME line with mismatched FQDN (zone fallback)",
			output:      "2026-05-12T10:00:00Z INF Added CNAME inference.want.com.other.com which will route to this tunnel tunnelID=f37ef341-1234-5678-9abc-def012345678",
			requested:   "inference.want.com",
			wantErr:     true,
			wantInError: []string{"inference.want.com", "inference.want.com.other.com"},
		},
		{
			name:        "already-configured line with mismatched FQDN (zone fallback)",
			output:      "2026-05-12T10:00:00Z INF inference.v1337.org.humanresearch.ai is already configured to route to your tunnel tunnelID=f37ef341-1234-5678-9abc-def012345678",
			requested:   "inference.v1337.org",
			wantErr:     true,
			wantInError: []string{"inference.v1337.org", "inference.v1337.org.humanresearch.ai"},
		},
		{
			name:      "empty output is treated as no signal",
			output:    "",
			requested: "inference.example.com",
			wantErr:   false,
		},
		{
			name:      "mixed-case hostname tolerated",
			output:    "2026-05-12T10:00:00Z INF Added CNAME X.Want.com which will route to this tunnel tunnelID=f37ef341-1234-5678-9abc-def012345678",
			requested: "x.want.com",
			wantErr:   false,
		},
		{
			name:      "unknown log shape continues without error",
			output:    "2026-05-12T10:00:00Z INF Some new log shape we have never seen before",
			requested: "inference.example.com",
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := verifyRoutedHostname(tt.output, tt.requested)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("verifyRoutedHostname(...) = nil, want error")
				}
				for _, want := range tt.wantInError {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("error %q does not contain %q", err.Error(), want)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("verifyRoutedHostname(...) = %v, want nil", err)
			}
		})
	}
}
