package tools

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type file struct {
	name, body string
	mode       int64
	symlink    string
}

func tarGz(t *testing.T, files []file) []byte {
	t.Helper()

	var buf bytes.Buffer

	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	for _, f := range files {
		hdr := &tar.Header{Name: f.name, Mode: f.mode, Size: int64(len(f.body)), Typeflag: tar.TypeReg}
		if f.symlink != "" {
			hdr = &tar.Header{Name: f.name, Linkname: f.symlink, Typeflag: tar.TypeSymlink, Mode: 0o777}
		}

		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}

		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}

		if f.symlink == "" {
			if _, err := tw.Write([]byte(f.body)); err != nil {
				t.Fatal(err)
			}
		}
	}

	tw.Close()
	gz.Close()

	return buf.Bytes()
}

func zipBytes(t *testing.T, files []file) []byte {
	t.Helper()

	var buf bytes.Buffer

	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, err := zw.Create(f.name)
		if err != nil {
			t.Fatal(err)
		}

		w.Write([]byte(f.body))
	}

	zw.Close()

	return buf.Bytes()
}

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// serve returns a server serving path → body.
func serve(t *testing.T, routes map[string][]byte) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}

		w.Write(b)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func testInstaller(binDir string, tools ...Tool) *Installer {
	return &Installer{
		BinDir:   binDir,
		Manifest: &Manifest{Tools: tools},
		Client:   http.DefaultClient,
		GOOS:     "linux",
		GOARCH:   "amd64",
	}
}

func asset(url, sha, member string) map[string]Asset {
	return map[string]Asset{"linux/amd64": {URL: url, SHA256: sha, Member: member}}
}

func TestInstallFormats(t *testing.T) {
	rawBody := []byte("#!/bin/sh\necho raw\n")
	tgz := tarGz(t, []file{
		{name: "./linux-amd64/README", body: "x"},
		{name: "linux-amd64/k3d", body: "#!/bin/sh\necho tgz\n", mode: 0o755},
	})
	zb := zipBytes(t, []file{{name: "bin/k9s", body: "#!/bin/sh\necho zip\n"}})

	srv := serve(t, map[string][]byte{"/raw": rawBody, "/a.tgz": tgz, "/a.zip": zb})
	binDir := filepath.Join(t.TempDir(), "bin") // created on demand

	in := testInstaller(binDir,
		Tool{Name: "kubectl", Version: "1.0.0", Format: FormatRaw, Assets: asset(srv.URL+"/raw", sum(rawBody), "")},
		Tool{Name: "k3d", Version: "1.0.0", Format: FormatTarGz, Assets: asset(srv.URL+"/a.tgz", sum(tgz), "linux-amd64/k3d")},
		Tool{Name: "k9s", Version: "1.0.0", Format: FormatZip, Assets: asset(srv.URL+"/a.zip", sum(zb), "bin/k9s")},
	)

	want := map[string]string{"kubectl": string(rawBody), "k3d": "#!/bin/sh\necho tgz\n", "k9s": "#!/bin/sh\necho zip\n"}

	for name, body := range want {
		if err := in.Install(context.Background(), name); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}

		p := filepath.Join(binDir, name)

		got, err := os.ReadFile(p)
		if err != nil || string(got) != body {
			t.Fatalf("%s content = %q, %v", name, got, err)
		}

		if fi, _ := os.Stat(p); fi.Mode().Perm()&0o111 == 0 {
			t.Fatalf("%s not executable: %v", name, fi.Mode())
		}
	}

	// No staging leftovers.
	entries, _ := os.ReadDir(binDir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".obol-install") {
			t.Fatalf("staging dir left behind: %s", e.Name())
		}
	}
}

func TestInstallChecksumMismatchFailsClosed(t *testing.T) {
	body := []byte("evil")
	srv := serve(t, map[string][]byte{"/kubectl": body})
	binDir := t.TempDir()

	// Pre-existing managed copy must survive a failed upgrade untouched.
	existing := filepath.Join(binDir, "kubectl")
	if err := os.WriteFile(existing, []byte("good"), 0o755); err != nil {
		t.Fatal(err)
	}

	in := testInstaller(binDir, Tool{Name: "kubectl", Version: "1.0.0", Format: FormatRaw, Assets: asset(srv.URL+"/kubectl", sum([]byte("expected")), "")})

	err := in.Install(context.Background(), "kubectl")
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("want checksum mismatch, got %v", err)
	}

	if got, _ := os.ReadFile(existing); string(got) != "good" {
		t.Fatalf("existing binary modified: %q", got)
	}

	entries, _ := os.ReadDir(binDir)
	if len(entries) != 1 {
		t.Fatalf("unexpected files left in bin dir: %v", entries)
	}
}

func TestInstallErrors(t *testing.T) {
	tgz := tarGz(t, []file{{name: "other", body: "x"}, {name: "helm", symlink: "/etc/passwd"}})
	srv := serve(t, map[string][]byte{"/a.tgz": tgz})

	in := testInstaller(t.TempDir(),
		Tool{Name: "helm", Version: "1.0.0", Format: FormatTarGz, Assets: asset(srv.URL+"/a.tgz", sum(tgz), "helm")},
		Tool{Name: "k3d", Version: "1.0.0", Format: FormatRaw, Assets: asset(srv.URL+"/404", sha64, "")},
		Tool{Name: "k9s", Version: "1.0.0", Format: FormatRaw, Assets: map[string]Asset{"darwin/arm64": {URL: srv.URL, SHA256: sha64}}},
	)

	for name, want := range map[string]string{
		"helm":  "not found", // symlink entries are never materialised
		"k3d":   "HTTP 404",
		"k9s":   "no release asset for linux/amd64",
		"nope?": "unknown tool",
	} {
		err := in.Install(context.Background(), name)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want error containing %q, got %v", name, want, err)
		}
	}
}

func TestInstallHelmPluginReplacesExisting(t *testing.T) {
	tgz := tarGz(t, []file{
		{name: "diff/plugin.yaml", body: "name: diff\nversion: \"3.15.11\"\n"},
		{name: "diff/bin/diff", body: "bin", mode: 0o755},
		{name: "README.md", body: "outside prefix"},
	})
	srv := serve(t, map[string][]byte{"/hd.tgz": tgz})

	plugins := t.TempDir()

	// Old copy installed by `helm plugin install` under a different dir name.
	old := filepath.Join(plugins, "helm-diff")
	os.MkdirAll(old, 0o755)
	os.WriteFile(filepath.Join(old, "plugin.yaml"), []byte("name: diff\nversion: 3.15.7\n"), 0o644)

	// Unrelated plugin must be left alone.
	other := filepath.Join(plugins, "secrets")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "plugin.yaml"), []byte("name: secrets\nversion: 1.0.0\n"), 0o644)

	in := testInstaller(t.TempDir(), Tool{
		Name: "helm-diff", Version: "3.15.11", Kind: KindHelmPlugin, PluginName: "diff",
		Format: FormatTarGz, Assets: asset(srv.URL+"/hd.tgz", sum(tgz), "diff"),
	})
	in.PluginsDir = plugins

	st := in.Status()
	if st[0].State != StateOutdated || st[0].Installed != "3.15.7" || !st[0].NeedsAction() {
		t.Fatalf("pre-upgrade status = %+v", st[0])
	}

	res, err := in.Ensure(context.Background(), EnsureOptions{Upgrade: true})
	if err != nil || len(res) != 1 || !res[0].Installed {
		t.Fatalf("ensure: %+v, %v", res, err)
	}

	found := FindPlugins(plugins, "diff")
	if len(found) != 1 || found[0].Version != "3.15.11" || found[0].Dir != old {
		t.Fatalf("plugins after install = %+v", found)
	}

	if fi, err := os.Stat(filepath.Join(old, "bin", "diff")); err != nil || fi.Mode().Perm()&0o111 == 0 {
		t.Fatalf("plugin binary missing or not executable: %v", err)
	}

	if _, err := os.Stat(filepath.Join(old, "README.md")); err == nil {
		t.Fatal("file outside archive prefix was extracted")
	}

	if len(FindPlugins(plugins, "secrets")) != 1 {
		t.Fatal("unrelated plugin removed")
	}

	entries, _ := os.ReadDir(plugins)
	if len(entries) != 2 {
		t.Fatalf("leftovers in plugins dir: %v", entries)
	}
}

func TestEnsureNeverTouchesPathOrEnvTools(t *testing.T) {
	body := []byte("#!/bin/sh\necho " + `'{"clientVersion":{"gitVersion":"v` + "9.9.9" + `"}}'` + "\n")
	srv := serve(t, map[string][]byte{"/kubectl": body, "/helm": body})

	binDir := t.TempDir()
	pathDir := t.TempDir()
	t.Setenv("PATH", pathDir)
	t.Setenv("OBOL_HELM", "")
	t.Setenv("OBOL_KUBECTL", "")

	kubectlPin := pin(t, "kubectl")
	writeTool(t, pathDir, "kubectl", kubectlOut(bump(t, kubectlPin, -1))) // compatible, older

	envHelm := writeTool(t, t.TempDir(), "helm", helmOut("3.0.0"))
	t.Setenv("OBOL_HELM", envHelm)

	in := testInstaller(binDir,
		Tool{Name: "kubectl", Version: kubectlPin, Format: FormatRaw, Assets: asset(srv.URL+"/kubectl", sum(body), "")},
		Tool{Name: "helm", Version: pin(t, "helm"), Format: FormatRaw, Assets: asset(srv.URL+"/helm", sum(body), "")},
	)

	res, err := in.Ensure(context.Background(), EnsureOptions{Upgrade: true})
	if err != nil || len(res) != 0 {
		t.Fatalf("expected no installs, got %+v, %v", res, err)
	}

	if _, err := os.Stat(filepath.Join(binDir, "kubectl")); err == nil {
		t.Fatal("kubectl installed despite compatible PATH copy")
	}

	for _, s := range in.Status() {
		if s.NeedsAction() {
			t.Errorf("%s reported as needing action: %+v", s.Name, s)
		}
	}
}

func TestEnsureUpgradesOnlyOutdatedManaged(t *testing.T) {
	newBody := []byte("#!/bin/sh\necho 'v3.21.3+gnew'\n")
	srv := serve(t, map[string][]byte{"/helm": newBody})

	binDir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OBOL_HELM", "")

	tool := Tool{Name: "helm", Version: "3.21.3", Format: FormatRaw, VersionArgs: []string{"version", "--short"}, Assets: asset(srv.URL+"/helm", sum(newBody), "")}
	in := testInstaller(binDir, tool)

	// Newer managed copy: never downgraded.
	writeTool(t, binDir, "helm", helmOut("3.22.0"))

	if res, err := in.Ensure(context.Background(), EnsureOptions{Upgrade: true}); err != nil || len(res) != 0 {
		t.Fatalf("newer managed helm touched: %+v, %v", res, err)
	}

	// Outdated managed copy: only replaced with Upgrade.
	writeTool(t, binDir, "helm", helmOut("3.9.0")) // different size: defeats the probe cache

	if res, _ := in.Ensure(context.Background(), EnsureOptions{}); len(res) != 0 {
		t.Fatalf("outdated helm replaced without Upgrade: %+v", res)
	}

	res, err := in.Ensure(context.Background(), EnsureOptions{Upgrade: true})
	if err != nil || len(res) != 1 || res[0].From != "3.9.0" {
		t.Fatalf("upgrade: %+v, %v", res, err)
	}

	if got, _ := os.ReadFile(filepath.Join(binDir, "helm")); !bytes.Equal(got, newBody) {
		t.Fatalf("helm not replaced: %q", got)
	}
}
