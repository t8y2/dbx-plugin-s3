import test from "node:test";
import assert from "node:assert/strict";
import { formatObjectSize, sortEntriesDirectoryFirst, prettyJsonText } from "./object-metadata.js";

test("formatObjectSize formats byte counts", () => {
  assert.equal(formatObjectSize(0), "0 B");
  assert.equal(formatObjectSize(2048), "2 KiB");
  assert.equal(formatObjectSize(Number.NaN), "—");
});

test("sortEntriesDirectoryFirst keeps folders above files", () => {
  const sorted = sortEntriesDirectoryFirst([
    { name: "zebra.txt", kind: "file" },
    { name: "apple", kind: "directory" },
    { name: "banana.txt", kind: "file" },
    { name: "bucket-a", kind: "bucket" },
    { name: "Apple2", kind: "directory" },
  ]);
  assert.deepEqual(sorted.map((entry) => entry.name), ["apple", "Apple2", "bucket-a", "banana.txt", "zebra.txt"]);
});

test("sortEntriesDirectoryFirst does not mutate the input", () => {
  const input = [{ name: "b.txt", kind: "file" }, { name: "a", kind: "directory" }];
  const sorted = sortEntriesDirectoryFirst(input);
  assert.equal(input[0].name, "b.txt");
  assert.equal(sorted[0].name, "a");
});

test("prettyJsonText indents compact JSON and keeps invalid input", () => {
  assert.equal(prettyJsonText('{"a":[1,2],"b":"é"}'), '{\n  "a": [\n    1,\n    2\n  ],\n  "b": "é"\n}');
  assert.equal(prettyJsonText('{"truncated'), '{"truncated');
});
