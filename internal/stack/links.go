package stack

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/ObolNetwork/obol-stack/internal/config"
)

// Dashboard link kinds understood by DashboardURL.
const (
	LinkRoot       = ""
	LinkOffer      = "offer"
	LinkStorefront = "storefront"
	LinkPurchases  = "purchases"
	LinkListings   = "listings"
	LinkAgent      = "agent"
)

// DashboardURL builds a link into the local obol-stack front-end on top of
// base (normally LocalIngressURL). It only emits routes that exist in the
// front-end today:
//
//	root        /
//	offer       /marketplace/<ns%2Fname>   ([slug] route: ONE path segment,
//	                                       "ns/name" percent-encoded; the
//	                                       page renders once the offer is
//	                                       Ready and in the published catalog)
//	storefront  /storefront
//	purchases   /marketplace/purchases
//	listings    /marketplace/listings      (all of this stack's offers,
//	                                       incl. not-yet-Ready ones)
//	agent       /                          (AgentInstanceCard ids are
//	                                       #agent-<runtime>-<id>, but cards
//	                                       render after a client fetch and
//	                                       nothing scrolls to the hash on
//	                                       load, so a deep anchor would be a
//	                                       no-op)
//
// TODO(lean-stack §5): switch to the planned routes once the front-end ships
// them — /agents/<ns>/<name>, /marketplace/listings/<ns>/<name>,
// /marketplace/purchases/<ns>/<name>, /networks/<name>/<id>, /models.
func DashboardURL(base, kind, ns, name string) string {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	switch kind {
	case LinkOffer:
		if ns == "" || name == "" {
			return base + "/marketplace/listings"
		}
		return base + "/marketplace/" + url.PathEscape(ns+"/"+name)
	case LinkStorefront:
		return base + "/storefront"
	case LinkPurchases:
		return base + "/marketplace/purchases"
	case LinkListings:
		return base + "/marketplace/listings"
	default: // LinkRoot, LinkAgent and anything unknown
		return base + "/"
	}
}

// First-run marker files (in cfg.ConfigDir) gating one-time browser opens.
const (
	// MarkerUIOpened is written after the dashboard was auto-opened on the
	// first successful `obol stack up`.
	MarkerUIOpened = ".ui-opened"
	// MarkerFirstOfferOpened is written after the browser was auto-opened for
	// the first offer created on this stack.
	MarkerFirstOfferOpened = ".first-offer-opened"
)

// MarkerExists reports whether the named first-run marker is present.
func MarkerExists(cfg *config.Config, name string) bool {
	_, err := os.Stat(filepath.Join(cfg.ConfigDir, name))
	return err == nil
}

// WriteMarker records the named first-run marker. Best-effort: a failure only
// means the one-time action may repeat.
func WriteMarker(cfg *config.Config, name string) error {
	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfg.ConfigDir, name), nil, 0o644)
}

// OnFirstRun runs open unless the marker exists, and writes the marker only
// when open reports success — so a first run where no browser could be opened
// (SSH, headless, CI) does not burn the one-time open. Returns whether open
// ran and succeeded.
func OnFirstRun(cfg *config.Config, marker string, open func() bool) bool {
	if MarkerExists(cfg, marker) {
		return false
	}
	if !open() {
		return false
	}
	_ = WriteMarker(cfg, marker)
	return true
}
