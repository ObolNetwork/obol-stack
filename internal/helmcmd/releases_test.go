package helmcmd

import "testing"

func TestParseReleaseHealth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want ReleaseHealth
	}{
		{"empty", "", ReleasesMissing},
		{"null", "null", ReleasesMissing},
		{"empty list", "[]", ReleasesMissing},
		{"deployed", `[{"name":"a","status":"deployed"},{"name":"b","status":"deployed"}]`, ReleasesDeployed},
		{"failed", `[{"name":"a","status":"deployed"},{"name":"b","status":"failed"}]`, ReleasesUnhealthy},
		{"pending", `[{"name":"a","status":"pending-install"}]`, ReleasesUnhealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReleaseHealth([]byte(tc.in))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
	if _, err := ParseReleaseHealth([]byte("not json")); err == nil {
		t.Error("expected parse error")
	}
}
