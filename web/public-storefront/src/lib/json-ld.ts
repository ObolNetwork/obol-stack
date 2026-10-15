// Serialize a value as JSON that is safe to inline inside an HTML <script>
// element (e.g. <script type="application/ld+json">). JSON.stringify alone
// leaves "<", ">" and "&" untouched, so a string containing "</script>" or
// "<!--" would change how the HTML parser tokenizes the element. Escaping
// them as \uXXXX keeps the JSON value identical once parsed. U+2028/U+2029
// are escaped too so the output is also a valid JavaScript string literal.
const SCRIPT_UNSAFE_CHARS = /[<>&\u2028\u2029]/g;

const SCRIPT_ESCAPES: Record<string, string> = {
  "<": "\\u003c",
  ">": "\\u003e",
  "&": "\\u0026",
  "\u2028": "\\u2028",
  "\u2029": "\\u2029",
};

export function safeJsonForScript(value: unknown): string {
  const json = JSON.stringify(value) ?? "null";
  return json.replace(SCRIPT_UNSAFE_CHARS, (ch) => SCRIPT_ESCAPES[ch] ?? ch);
}
