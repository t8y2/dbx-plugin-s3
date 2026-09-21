// Path-bar navigation helpers shared by the Svelte app: turning whatever the
// user pasted into a canonical plugin URI, and comparing pasted paths against
// the percent-encoded entry URIs the backend returns.

export const TREE_ROOT = "s3:/";

// "s3://bucket/a b", "/bucket/a b", "bucket/a b/" and "  s3:/bucket/a b " all
// resolve to s3://bucket/a b. On a bucket-scoped connection the pasted path is
// a key path, and a leading segment matching the pinned bucket is tolerated
// (users copy full URIs even when the connection already fixes the bucket).
export function normalizePathInput(raw, { bucketScoped = false, scopedBucket = "" } = {}) {
  const value = String(raw || "").trim().replace(/^s3:[/]*/i, "").replace(/^\/+/, "").replace(/\/+$/, "");
  let parts = value.split("/").filter(Boolean);
  if (bucketScoped && scopedBucket && parts[0] === scopedBucket) parts = parts.slice(1);
  if (!parts.length) return TREE_ROOT;
  return bucketScoped ? `s3:/${parts.join("/")}` : `s3://${parts.join("/")}`;
}

// The folder that contains a file URI ("s3://b/a/f.txt" -> "s3://b/a/",
// "s3://b/f.txt" -> "s3://b/"); bucket-only inputs fall back to themselves.
export function parentOfUri(uri) {
  const value = uri.replace(/\/$/, "");
  const slash = value.lastIndexOf("/");
  return slash <= value.indexOf("://") + 2 ? `${value}/` : `${value.slice(0, slash + 1)}`;
}

// Entry URIs arrive percent-encoded while pasted paths are raw user text;
// compare both through the same decoding so non-ASCII keys still match.
export function displayUri(uri) {
  try {
    return decodeURIComponent(uri);
  } catch {
    return uri;
  }
}

export function sameEntryUri(left, right) {
  return displayUri(left) === displayUri(right);
}

// The last path segment as a display name.
export function baseNameOfUri(uri) {
  return displayUri(uri.slice(uri.lastIndexOf("/") + 1));
}
