import assert from "node:assert/strict";
import { test } from "node:test";
import { formatObjectModified, formatObjectSize } from "./object-metadata.js";

test("object sizes distinguish empty files, missing metadata and large objects", () => {
  assert.equal(formatObjectSize(0), "0 B");
  assert.equal(formatObjectSize(1023), "1,023 B");
  assert.equal(formatObjectSize(1024), "1 KiB");
  assert.equal(formatObjectSize(1536), "1.5 KiB");
  assert.equal(formatObjectSize(3 * 1024 ** 3), "3 GiB");
  assert.equal(formatObjectSize(1024 ** 5), "1 PiB");
  for (const value of [undefined, null, NaN, Infinity, -1, "1024"]) {
    assert.equal(formatObjectSize(value), "—");
  }
});

test("modification times use the requested locale and local timezone", () => {
  const timestamp = "2026-09-18T01:54:26Z";
  for (const locale of ["en", "zh-CN"]) {
    const expected = new Date(timestamp).toLocaleString(locale, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
    assert.equal(formatObjectModified(timestamp, locale), expected);
  }
  for (const value of [undefined, null, "", "invalid"]) {
    assert.equal(formatObjectModified(value), "—");
  }
});
