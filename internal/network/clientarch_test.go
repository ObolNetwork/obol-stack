package network

import "testing"

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
