import assert from "node:assert/strict";
import { test } from "node:test";
import { maxDownloadBytes, readDownload, saveDownload } from "./downloads.js";

test("native downloads ask the host to save before reading, route progress and cancel by ID", async () => {
  const controller = new AbortController();
  let listener;
  let resolveSave;
  let downloadId;
  let cancelled;
  let unsubscribed = false;
  const progress = [];
  const host = {
    onEvent(callback) { listener = callback; return () => { unsubscribed = true; }; },
    downloadFile(request) {
      downloadId = request.downloadId;
      assert.match(downloadId, /^[a-f0-9-]{36}$/);
      assert.equal(request.fileName, "large.bin");
      assert.equal(request.params.uri, "s3://bucket/large.bin");
      return new Promise((resolve) => { resolveSave = resolve; });
    },
    async cancelDownload(id) { cancelled = id; resolveSave(null); },
  };
  const result = saveDownload({ host, fileName: "large.bin", params: { uri: "s3://bucket/large.bin" }, signal: controller.signal, onProgress: (value) => progress.push(value.sent) });
  listener({ method: "host.download.progress", params: { downloadId: "other", sent: 10 } });
  listener({ method: "host.download.progress", params: { downloadId, sent: 300 * 1024 * 1024 } });
  controller.abort();
  assert.equal(await result, null);
  assert.equal(cancelled, downloadId);
  assert.equal(unsubscribed, true);
  assert.deepEqual(progress, [300 * 1024 * 1024]);
});

const tick = () => new Promise((resolve) => setImmediate(resolve));

test("downloads preserve bytes, metadata and progress", async () => {
  const progress = [];
  const metadata = { size: 3, contentType: "text/plain" };
  const result = await readDownload({
    open: async () => ({ metadata, stream: new ReadableStream({ start(controller) {
      controller.enqueue(new Uint8Array([1, 2]));
      controller.enqueue(new Uint8Array([3]));
      controller.close();
    } }) }),
    onMetadata: (value) => assert.equal(value, metadata), onProgress: (sent) => progress.push(sent),
  });
  assert.deepEqual(result.bytes, new Uint8Array([1, 2, 3]));
  assert.equal(result.metadata, metadata);
  assert.deepEqual(progress, [2, 3]);
});

test("cancellation before opening never starts a download", async () => {
  const controller = new AbortController();
  controller.abort();
  await assert.rejects(readDownload({ signal: controller.signal, open: () => assert.fail("unexpected stream") }), { name: "AbortError" });
});

test("cancellation interrupts a pending read and closes the stream", async () => {
  const controller = new AbortController();
  let cancelled = 0;
  const stream = new ReadableStream({ cancel() { cancelled += 1; } });
  const result = readDownload({ signal: controller.signal, open: async () => ({ stream, metadata: {} }) });
  await tick();
  controller.abort();
  await assert.rejects(result, { name: "AbortError" });
  await tick();
  assert.equal(cancelled, 1);
  assert.equal(stream.locked, false);
});

test("cancellation during opening returns immediately and closes a late stream", async () => {
  const controller = new AbortController();
  let resolveOpen;
  let cancelled = 0;
  const opening = new Promise((resolve) => { resolveOpen = resolve; });
  const result = readDownload({ signal: controller.signal, open: () => opening });
  controller.abort();
  await assert.rejects(result, { name: "AbortError" });
  resolveOpen({ stream: new ReadableStream({ cancel() { cancelled += 1; } }), metadata: {} });
  await tick();
  assert.equal(cancelled, 1);
});

test("oversized metadata is rejected before reading chunks", async () => {
  let cancelled = 0;
  await assert.rejects(readDownload({ tooLarge: "size limit", open: async () => ({
    metadata: { size: maxDownloadBytes + 1 },
    stream: new ReadableStream({ pull() { assert.fail("must not read oversized object"); }, cancel() { cancelled += 1; } }, { highWaterMark: 0 }),
  }) }), /size limit/);
  assert.equal(cancelled, 1);
});

test("downloads enforce the byte limit even when the object size is missing or stale", async () => {
  let cancelled = 0;
  await assert.rejects(readDownload({ maxBytes: 3, tooLarge: "size limit", open: async () => ({
    metadata: { size: 2 }, stream: new ReadableStream({ start(controller) {
      controller.enqueue(new Uint8Array(4));
    }, cancel() { cancelled += 1; } }),
  }) }), /size limit/);
  assert.equal(cancelled, 1);
});

test("an end-of-stream truncation flag rejects incomplete file and ZIP downloads", async () => {
  const metadata = {};
  await assert.rejects(readDownload({ tooLarge: "truncated", open: async () => ({
    metadata, stream: new ReadableStream({ pull(controller) {
      controller.enqueue(new Uint8Array([1]));
      metadata.truncated = true;
      controller.close();
    } }),
  }) }), /truncated/);
});

test("stream errors and open failures propagate without saving partial bytes", async () => {
  await assert.rejects(readDownload({ open: async () => { throw new Error("connection failed"); } }), /connection failed/);
  const stream = new ReadableStream({ start(controller) { controller.error(new Error("read failed")); } });
  await assert.rejects(readDownload({ open: async () => ({ stream, metadata: {} }) }), /read failed/);
  assert.equal(stream.locked, false);
});

test("an exact-limit download succeeds and aborting afterwards has no effect", async () => {
  const controller = new AbortController();
  const result = await readDownload({ maxBytes: 3, signal: controller.signal, open: async () => ({
    metadata: { size: 3 }, stream: new ReadableStream({ start(streamController) {
      streamController.enqueue(new Uint8Array(3));
      streamController.close();
    } }),
  }) });
  controller.abort();
  assert.equal(result.bytes.length, 3);
});
