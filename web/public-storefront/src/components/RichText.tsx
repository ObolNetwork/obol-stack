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
}: {
  html?: string;
  fallback: string;
  className?: string;
}) {
  if (html) {
    return (
      <div
        className={`richtext ${className}`}
        dangerouslySetInnerHTML={{ __html: html }}
      />
    );
  }
  return <p className={`whitespace-pre-line ${className}`}>{fallback}</p>;
}
