package storefront

import (
	"regexp"
	"strings"
	"testing"
)

func TestRenderRichText_Formatting(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string
		notWant []string
	}{
		{
			name: "paragraphs and line breaks",
			in:   "First line\nSecond line\n\nNew paragraph",
			want: []string{"<br", "<p>First line", "<p>New paragraph</p>"},
		},
		{
			name: "emphasis and lists",
			in:   "We sell **audits** and *reviews*.\n\n- fast\n- cheap",
			want: []string{"<strong>audits</strong>", "<em>reviews</em>", "<ul>", "<li>fast</li>"},
		},
		{
			name: "code spans and blocks",
			in:   "Call `buy.py pay` like:\n\n```\nbuy.py pay https://x\n```",
			want: []string{"<code>buy.py pay</code>", "<pre>"},
		},
		{
			name: "https links keep href with nofollow",
			in:   "[docs](https://docs.obol.org) and mail [us](mailto:ops@acme.io)",
			want: []string{`href="https://docs.obol.org"`, `rel="nofollow"`, `href="mailto:ops@acme.io"`},
		},
		{
			name: "autolinked https URL",
			in:   "See https://acme.example/pricing for details",
			want: []string{`href="https://acme.example/pricing"`},
		},
		{
			name:    "headings demoted into h3/h4 band",
			in:      "# Big\n\n## Sub\n\n##### Tiny",
			want:    []string{"<h3>Big</h3>", "<h3>Sub</h3>", "<h4>Tiny</h4>"},
			notWant: []string{"<h1", "<h2", "<h5"},
		},
		{
			name: "empty input renders empty",
			in:   "   \n  ",
			want: []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(RenderRichText(tc.in))
			for _, w := range tc.want {
				if !strings.Contains(got, w) {
					t.Errorf("RenderRichText(%q) = %q, missing %q", tc.in, got, w)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("RenderRichText(%q) = %q, must not contain %q", tc.in, got, nw)
				}
			}
			if tc.in == "   \n  " && got != "" {
				t.Errorf("blank input must render empty, got %q", got)
			}
		})
	}
}

// TestRenderRichText_XSSCorpus is the security contract: none of these
// payloads may survive into executable or fetchable form. If a case here
// starts failing, treat it as a vulnerability, not a rendering bug.
func TestRenderRichText_XSSCorpus(t *testing.T) {
	corpus := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`<svg/onload=alert(1)>`,
		`[click](javascript:alert(1))`,
		`[click](JaVaScRiPt:alert(1))`,
		`[click](data:text/html;base64,PHNjcmlwdD4=)`,
		`[click](vbscript:msgbox)`,
		`<a href="https://ok.example" onclick="alert(1)">x</a>`,
		`<iframe src="https://evil.example"></iframe>`,
		`<style>body{display:none}</style>`,
		`<base href="https://evil.example/">`,
		`<form action="https://evil.example"><input></form>`,
		`<math><mtext></mtext><script>alert(1)</script></math>`,
		`![img](https://evil.example/x.png)`, // images not allowed in v1
		"```\n</pre><script>alert(1)</script>\n```",
		`[x]: javascript:alert(1)` + "\n[x]",
		`<details open ontoggle=alert(1)>`,
		`&lt;script&gt;alert(1)&lt;/script&gt;`,
		`<a href="  javascript:alert(1)">pad</a>`,
		`[e](https://ok.example "on'><script>alert(1)</script>")`,
	}
	// The real invariant: the output may contain dangerous strings ONLY as
	// escaped text — every actual tag must be on the allow-list, and no tag
	// may carry an attribute other than a's href/rel. Escaped payloads
	// (&lt;svg onload=...&gt;) are inert and acceptable.
	tagRe := regexp.MustCompile(`<\s*/?\s*([a-zA-Z0-9]+)([^>]*)>`)
	allowedTags := map[string]bool{
		"p": true, "br": true, "strong": true, "em": true, "ul": true,
		"ol": true, "li": true, "code": true, "pre": true, "a": true,
		"h3": true, "h4": true,
	}
	attrRe := regexp.MustCompile(`([a-zA-Z-]+)\s*=`)
	for _, payload := range corpus {
		t.Run(payload[:min(len(payload), 40)], func(t *testing.T) {
			got := string(RenderRichText(payload))
			for _, m := range tagRe.FindAllStringSubmatch(got, -1) {
				tag, attrs := strings.ToLower(m[1]), m[2]
				if !allowedTags[tag] {
					t.Fatalf("payload %q rendered %q — forbidden tag <%s>", payload, got, tag)
				}
				for _, am := range attrRe.FindAllStringSubmatch(attrs, -1) {
					attr := strings.ToLower(am[1])
					if tag == "a" && (attr == "href" || attr == "rel") {
						continue
					}
					t.Fatalf("payload %q rendered %q — forbidden attribute %q on <%s>", payload, got, attr, tag)
				}
			}
			for _, forbidden := range []string{`href="javascript`, `href="vbscript`, `href="data`} {
				if strings.Contains(strings.ToLower(got), forbidden) {
					t.Fatalf("payload %q rendered %q — contains forbidden %q", payload, got, forbidden)
				}
			}
		})
	}
}

// TestRenderRichText_OnlySchemesSurvive pins the href scheme allow-list.
func TestRenderRichText_OnlySchemesSurvive(t *testing.T) {
	got := string(RenderRichText("[a](https://ok) [b](http://insecure) [c](ftp://x) [d](mailto:x@y.z) [e](/relative)"))
	if !strings.Contains(got, `href="https://ok"`) || !strings.Contains(got, `href="mailto:x@y.z"`) {
		t.Fatalf("https/mailto links must survive: %q", got)
	}
	for _, bad := range []string{`href="http://insecure"`, `href="ftp://x"`, `href="/relative"`} {
		if strings.Contains(got, bad) {
			t.Fatalf("forbidden href survived: %q in %q", bad, got)
		}
	}
}
