package storefront

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// probeOrigin is a representative third-party origin sent as the Origin
// header when probing a logo host. Browsers attach Origin to cross-origin
// image fetches; sending one elicits the host's CORS response headers so we
// can tell whether fetch()/canvas consumers will be able to load the image.
const probeOrigin = "https://storefront.example"

// largeLogoBytes is the size above which we warn that the logo will be slow
// to load on catalog and storefront pages.
const largeLogoBytes = 2 << 20 // 2 MiB

// logoProbeClient performs preflight requests; overridable in tests so TLS
// httptest servers can be trusted.
var logoProbeClient = &http.Client{Timeout: 10 * time.Second}

// LogoPreflight is the result of probing a logo URL the way a browser would
// load it.
type LogoPreflight struct {
	// LoadFailure is true when the image could not be fetched at all
	// (network error, HTTP error status, or a non-image response) — the
	// logo will be broken everywhere, not just for strict consumers.
	LoadFailure bool
	// Warnings are human-readable problems found by the probe.
	Warnings []string
}

// OK reports whether the probe found no problems.
func (p LogoPreflight) OK() bool { return !p.LoadFailure && len(p.Warnings) == 0 }

// PreflightLogoURL fetches an absolute logo URL and checks the properties
// browsers and catalog consumers depend on: reachability, an image
// content-type, permissive CORS (for fetch()/canvas-based consumers), https
// (mixed content on https storefront pages), and size. Problems come back as
// Warnings rather than an error so the caller can decide whether to proceed.
// Empty and data: URIs are self-contained and probe as OK.
func PreflightLogoURL(ctx context.Context, rawURL string) LogoPreflight {
	var out LogoPreflight
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "data:") {
		return out
	}

	if strings.HasPrefix(rawURL, "http://") {
		out.Warnings = append(out.Warnings,
			"logo is served over http:// — browsers block mixed content, so it will not render on https pages (including your tunnel storefront); use an https URL")
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		out.LoadFailure = true
		out.Warnings = append(out.Warnings, fmt.Sprintf("invalid logo URL: %v", err))
		return out
	}
	req.Header.Set("Origin", probeOrigin)
	req.Header.Set("Accept", "image/*,*/*")

	resp, err := logoProbeClient.Do(req)
	if err != nil {
		out.LoadFailure = true
		out.Warnings = append(out.Warnings, fmt.Sprintf("could not fetch logo: %v", err))
		return out
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		out.LoadFailure = true
		out.Warnings = append(out.Warnings, fmt.Sprintf("logo URL returned HTTP %d", resp.StatusCode))
		return out
	}

	// Content-type: trust a specific header, sniff when absent or generic.
	sniff, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	ctype := strings.ToLower(strings.TrimSpace(resp.Header.Get("Content-Type")))
	if i := strings.Index(ctype, ";"); i >= 0 {
		ctype = strings.TrimSpace(ctype[:i])
	}
	if ctype == "" || ctype == "application/octet-stream" {
		ctype = http.DetectContentType(sniff)
	}
	if !strings.HasPrefix(ctype, "image/") {
		out.LoadFailure = true
		out.Warnings = append(out.Warnings,
			fmt.Sprintf("logo URL does not serve an image (content-type %s)", ctype))
	}

	// Plain <img> tags load without CORS, but fetch()/canvas-based consumers
	// (marketplace previews, catalog aggregators) need a permissive
	// Access-Control-Allow-Origin from the logo host.
	if acao := strings.TrimSpace(resp.Header.Get("Access-Control-Allow-Origin")); acao != "*" && acao != probeOrigin {
		out.Warnings = append(out.Warnings,
			"logo host does not send permissive CORS headers (Access-Control-Allow-Origin) — the image may not load on every site that embeds your catalog; host it on a CDN with CORS enabled or inline it as a data:image/... URI")
	}

	if resp.ContentLength > largeLogoBytes {
		out.Warnings = append(out.Warnings,
			fmt.Sprintf("logo is large (%.1f MiB) — it will load slowly; consider an image under 2 MiB", float64(resp.ContentLength)/(1<<20)))
	}
	return out
}
