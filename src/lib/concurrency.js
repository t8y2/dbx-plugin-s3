// Runs an async worker over every item with at most `limit` workers active
// at once, preserving result order. Workers are expected to record their own
// failures and return normally — a rejection aborts the remaining lanes, so
// callers that want per-item failures must catch inside the worker.

export async function mapWithConcurrency(items, limit, worker) {
  const list = Array.isArray(items) ? [...items] : [];
  const results = new Array(list.length);
  let next = 0;
  const lanes = Array.from({ length: Math.max(1, Math.min(limit, list.length)) }, async () => {
    while (next < list.length) {
      const index = next++;
      results[index] = await worker(list[index], index);
    }
  });
  await Promise.all(lanes);
  return results;
}
