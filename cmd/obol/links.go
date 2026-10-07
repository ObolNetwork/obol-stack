package main

import (
	"github.com/ObolNetwork/obol-stack/internal/config"
	"github.com/ObolNetwork/obol-stack/internal/stack"
	"github.com/ObolNetwork/obol-stack/internal/ui"
)

// dashboardURL is stack.DashboardURL on top of this stack's local ingress.
func dashboardURL(cfg *config.Config, kind, ns, name string) string {
	return stack.DashboardURL(stack.LocalIngressURL(cfg), kind, ns, name)
}

// openOnFirstRun opens url once per stack (gated by marker in the config
// dir). The marker is only written when a browser actually opened, so a
// first run over SSH / headless / CI doesn't burn it.
func openOnFirstRun(cfg *config.Config, u *ui.UI, marker, url string) bool {
	return stack.OnFirstRun(cfg, marker, func() bool {
		if !u.OpenBrowser(url) {
			return false
		}
		u.Dim("  (opened in your browser)")
		return true
	})
}

// printStackUpLinks runs after a successful `obol stack up`: stack.Up already
// printed the dashboard URL; open it on the first successful up only.
func printStackUpLinks(cfg *config.Config, u *ui.UI) {
	openOnFirstRun(cfg, u, stack.MarkerUIOpened, dashboardURL(cfg, stack.LinkRoot, "", ""))
}

// printOfferLinks prints the dashboard links for a ServiceOffer. The
// marketplace detail page only resolves once the offer is Ready (it reads the
// published catalog), so the one-time auto-open for the first offer created
// on this stack targets "My listings", which shows pending offers live.
func printOfferLinks(cfg *config.Config, u *ui.UI, ns, name string, created bool) {
	listings := dashboardURL(cfg, stack.LinkListings, "", "")
	u.Blank()
	u.OpenURL("Marketplace page (once Ready)", dashboardURL(cfg, stack.LinkOffer, ns, name), false)
	u.OpenURL("Your listings", listings, false)
	if created {
		openOnFirstRun(cfg, u, stack.MarkerFirstOfferOpened, listings)
	}
}
