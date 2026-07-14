package storefront

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// RenderRichText is the ONLY markdown→HTML path for operator-authored copy
// (storefront/offer descriptions). Everything it emits is served on public,
// tunnel-exposed pages, so the pipeline is deliberately strict:
//
//  1. goldmark renders a CommonMark subset with raw HTML DISABLED — anything
//     HTML-shaped in the source is escaped/omitted, never passed through.
//     Single newlines become <br> (operators write plain paragraphs and
//     expect line breaks to survive).
//  2. Headings are demoted into the h3/h4 band so seller copy can't
//     impersonate page chrome.
//  3. bluemonday sanitizes the rendered HTML against a tight allow-list:
//     p, br, strong, em, ul, ol, li, code, pre, a, h3, h4. Links keep href
//     only for https:/mailto: targets and are forced rel="nofollow".
//
// Renderers must insert the result as template.HTML (Go) or via
// dangerouslySetInnerHTML (React) — this function is the trust boundary; do
// not add a second markdown or sanitize path elsewhere.
func RenderRichText(markdown string) template.HTML {
	markdown = strings.TrimSpace(markdown)
	if markdown == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := richtextMarkdown.Convert([]byte(markdown), &buf); err != nil {
		// Fail safe: escaped plain text, never the raw input.
		return template.HTML(template.HTMLEscapeString(markdown))
	}
	rendered := demoteHeadings(buf.String())
	return template.HTML(strings.TrimSpace(richtextPolicy.Sanitize(rendered)))
}

// richtextMarkdown renders CommonMark + URL autolinking. Raw HTML stays at
// the goldmark default (disabled — html.WithUnsafe is what would let it
// through; never add it). Safe for concurrent use.
var richtextMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.Linkify),
	goldmark.WithRendererOptions(html.WithHardWraps()),
)

// richtextPolicy is the post-render allow-list. Element/attribute additions
// here are security-reviewed surface area — keep it small.
var richtextPolicy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowElements("p", "br", "strong", "em", "ul", "ol", "li", "code", "pre", "h3", "h4")
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("https", "mailto")
	p.RequireNoFollowOnLinks(true)
	return p
}()

// demoteHeadings maps h1/h2 → h3 and h5/h6 → h4 between rendering and
// sanitizing, so markdown headings survive (as small headings) instead of
// being stripped to bare text by the h3/h4-only policy. Operates on
// goldmark's own output — tags at this stage are lowercase and unadorned.
func demoteHeadings(s string) string {
	r := strings.NewReplacer(
		"<h1>", "<h3>", "</h1>", "</h3>",
		"<h2>", "<h3>", "</h2>", "</h3>",
		"<h5>", "<h4>", "</h5>", "</h4>",
		"<h6>", "<h4>", "</h6>", "</h4>",
	)
	return r.Replace(s)
}
