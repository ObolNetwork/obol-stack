// Run with: npm test (node --test; Node >= 22.18 strips TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";

import { safeJsonForScript } from "./json-ld.ts";

test("escapes characters that could end or alter a script element", () => {
  const out = safeJsonForScript({ name: "a</script><b>&c" });
  assert.ok(!out.includes("<"));
  assert.ok(!out.includes(">"));
  assert.ok(!out.includes("&"));
  assert.equal(
    out,
    '{"name":"a\\u003c/script\\u003e\\u003cb\\u003e\\u0026c"}',
  );
});

test("escapes U+2028 and U+2029", () => {
  const out = safeJsonForScript({ s: "x\u2028y\u2029z" });
  assert.equal(out, '{"s":"x\\u2028y\\u2029z"}');
});

test("round-trips to the original value", () => {
  const value = {
    name: "Shop <one> & </script> \u2028\u2029",
    offers: [{ description: "<!-- note -->", price: "0.01" }],
  };
  assert.deepEqual(JSON.parse(safeJsonForScript(value)), value);
});

test("leaves plain JSON unchanged", () => {
  const value = { a: 1, b: "plain text", c: [true, null] };
  assert.equal(safeJsonForScript(value), JSON.stringify(value));
});
