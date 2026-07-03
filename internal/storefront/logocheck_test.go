package storefront

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pngBytes is a minimal PNG magic header — enough for http.DetectContentType
// to sniff image/png.
var pngBytes = []byte("\x89PNG\r\n\x1a\n0000000000000000")

func withProbeClient(t *testing.T, c *http.Client) {
	t.Helper()
	prev := logoProbeClient
	logoProbeClient = c
	t.Cleanup(func() { logoProbeClient = prev })
}

func newLogoServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	withProbeClient(t, srv.Client())
	return srv
}

func hasWarning(p LogoPreflight, substr string) bool {
	for _, w := range p.Warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}

func TestPreflightLogoURL_HappyPath(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(pngBytes)
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if !got.OK() {
		t.Fatalf("expected OK, got %+v", got)
	}
}

func TestPreflightLogoURL_MissingCORSWarns(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngBytes)
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if got.LoadFailure {
		t.Fatalf("missing CORS is a soft warning, not a load failure: %+v", got)
	}
	if !hasWarning(got, "CORS") {
		t.Fatalf("expected CORS warning, got %+v", got)
	}
}

func TestPreflightLogoURL_EchoedOriginIsPermissive(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
		w.Write(pngBytes)
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if !got.OK() {
		t.Fatalf("echoed Origin should pass the CORS check: %+v", got)
	}
}

func TestPreflightLogoURL_HTTPErrorIsLoadFailure(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if !got.LoadFailure || !hasWarning(got, "HTTP 404") {
		t.Fatalf("expected load failure with HTTP 404, got %+v", got)
	}
}

func TestPreflightLogoURL_NonImageIsLoadFailure(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write([]byte("<html>login required</html>"))
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if !got.LoadFailure || !hasWarning(got, "does not serve an image") {
		t.Fatalf("expected non-image load failure, got %+v", got)
	}
}

func TestPreflightLogoURL_SniffsGenericContentType(t *testing.T) {
	srv := newLogoServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(pngBytes)
	})
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if !got.OK() {
		t.Fatalf("PNG bytes behind a generic content-type should sniff as image: %+v", got)
	}
}

func TestPreflightLogoURL_UnreachableIsLoadFailure(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	withProbeClient(t, http.DefaultClient)
	got := PreflightLogoURL(context.Background(), url+"/logo.png")
	if !got.LoadFailure || !hasWarning(got, "could not fetch") {
		t.Fatalf("expected fetch failure, got %+v", got)
	}
}

func TestPreflightLogoURL_PlainHTTPWarnsMixedContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	withProbeClient(t, srv.Client())
	got := PreflightLogoURL(context.Background(), srv.URL+"/logo.png")
	if got.LoadFailure {
		t.Fatalf("http scheme is a soft warning, not a load failure: %+v", got)
	}
	if !hasWarning(got, "mixed content") {
		t.Fatalf("expected mixed-content warning for http:// URL, got %+v", got)
	}
}

func TestPreflightLogoURL_SkipsEmptyAndDataURIs(t *testing.T) {
	for _, raw := range []string{"", "data:image/png;base64,aGk="} {
		if got := PreflightLogoURL(context.Background(), raw); !got.OK() {
			t.Fatalf("%q: expected OK without probing, got %+v", raw, got)
		}
	}
}
