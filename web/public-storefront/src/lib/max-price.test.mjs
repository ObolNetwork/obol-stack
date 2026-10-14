// Run with: npm test (node --test; Node >= 22.18 strips TypeScript types).
import assert from "node:assert/strict";
import { test } from "node:test";

import { maxPriceFlag } from "./max-price.ts";

test("adds --max-price only above the 1-token default ceiling", () => {
  assert.equal(maxPriceFlag("0.001", "perRequest"), "");
  assert.equal(maxPriceFlag("1", "perRequest"), "");
  assert.equal(maxPriceFlag("1.000", "perRequest"), "");
  assert.equal(maxPriceFlag("1.000001", "perRequest"), " --max-price 1.000001");
  assert.equal(maxPriceFlag("10", "perRequest"), " --max-price 10");
  assert.equal(maxPriceFlag("10"), " --max-price 10");
});

test("ignores non per-request and malformed prices", () => {
  assert.equal(maxPriceFlag("5", "perMTok"), "");
  assert.equal(maxPriceFlag(undefined, "perRequest"), "");
  assert.equal(maxPriceFlag("abc", "perRequest"), "");
  assert.equal(maxPriceFlag("1e3", "perRequest"), "");
});
