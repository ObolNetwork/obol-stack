// RichText renders controller-published descriptionHtml. The HTML is
// produced and sanitized exclusively by internal/storefront/richtext.go
// (goldmark with raw HTML disabled + bluemonday allow-list) — that single
// Go-side sanitizer is the trust boundary, which is what makes
// dangerouslySetInnerHTML acceptable here. Never feed this component HTML
// from any other source. Catalogs from pre-richtext controllers only carry
// the plain-text description — render that as text.
export function RichText({
  html,
  fallback,
  className = "",
  dataObol,
}: {
  html?: string;
  fallback: string;
  className?: string;
  /** Stable hook name emitted as data-obol=... for operator stylesheets. */
  dataObol?: string;
}) {
  if (html) {
    return (
      <div
        className={`richtext ${className}`}
        data-obol={dataObol}
        dangerouslySetInnerHTML={{ __html: html }}
      />
    );
  }
  return (
    <p className={`whitespace-pre-line ${className}`} data-obol={dataObol}>
      {fallback}
    </p>
  );
}
