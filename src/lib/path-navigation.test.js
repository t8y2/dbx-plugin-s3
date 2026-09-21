import assert from "node:assert/strict";
import { test } from "node:test";

import { TREE_ROOT, baseNameOfUri, normalizePathInput, parentOfUri, sameEntryUri } from "./path-navigation.js";

test("normalizePathInput accepts scheme, slash, and whitespace variants", () => {
  assert.equal(normalizePathInput("s3://bucket/a/b"), "s3://bucket/a/b");
  assert.equal(normalizePathInput("s3:/bucket/a/b"), "s3://bucket/a/b");
  assert.equal(normalizePathInput("/bucket/a/b/"), "s3://bucket/a/b");
  assert.equal(normalizePathInput("  bucket/a/b  "), "s3://bucket/a/b");
  assert.equal(normalizePathInput("bucket//a///b"), "s3://bucket/a/b");
  assert.equal(normalizePathInput("bucket"), "s3://bucket");
});

test("normalizePathInput collapses empty input to the tree root", () => {
  assert.equal(normalizePathInput(""), TREE_ROOT);
  assert.equal(normalizePathInput("   "), TREE_ROOT);
  assert.equal(normalizePathInput("s3:/"), TREE_ROOT);
  assert.equal(normalizePathInput("///"), TREE_ROOT);
});

test("normalizePathInput treats pasted paths as key paths on bucket-scoped connections", () => {
  const scoped = { bucketScoped: true, scopedBucket: "pinned" };
  assert.equal(normalizePathInput("a/b/c", scoped), "s3:/a/b/c");
  // A full URI pasted into a pinned connection still resolves: the bucket
  // segment matches and is stripped instead of being read as a key.
  assert.equal(normalizePathInput("s3://pinned/a/b", scoped), "s3:/a/b");
  assert.equal(normalizePathInput("s3://other/a", scoped), "s3:/other/a");
  assert.equal(normalizePathInput("", scoped), TREE_ROOT);
});

test("parentOfUri returns the containing folder with a trailing slash", () => {
  assert.equal(parentOfUri("s3://bucket/a/b/file.txt"), "s3://bucket/a/b/");
  assert.equal(parentOfUri("s3://bucket/file.txt"), "s3://bucket/");
  assert.equal(parentOfUri("s3:/a/file.txt"), "s3:/a/");
  // Folder inputs keep parent semantics (matches the rename flow's needs).
  assert.equal(parentOfUri("s3://bucket/a/"), "s3://bucket/");
  assert.equal(parentOfUri("s3://bucket/"), "s3://bucket/");
});

test("sameEntryUri matches encoded backend URIs against raw pasted paths", () => {
  assert.equal(sameEntryUri("s3://bucket/%E5%AF%B9%E8%B4%A6%E5%8D%95/9%E6%9C%88.csv", "s3://bucket/对账单/9月.csv"), true);
  assert.equal(sameEntryUri("s3://bucket/report%202025.xlsx", "s3://bucket/report 2025.xlsx"), true);
  assert.equal(sameEntryUri("s3://bucket/a.txt", "s3://bucket/b.txt"), false);
  // Both sides decode exactly once, consistently: a pasted path containing a
  // literal "%2F" sequence reads as an encoded slash and will not match a key
  // that literally contains "%2F" — an unavoidable pasted-path ambiguity.
  assert.equal(sameEntryUri("s3://bucket/literal%252Fkey", "s3://bucket/literal%252Fkey"), true);
});

test("baseNameOfUri decodes the final segment for display", () => {
  assert.equal(baseNameOfUri("s3://bucket/%E5%AF%B9%E8%B4%A6%E5%8D%95/9%E6%9C%88.csv"), "9月.csv");
  assert.equal(baseNameOfUri("s3://bucket/plain.txt"), "plain.txt");
});
