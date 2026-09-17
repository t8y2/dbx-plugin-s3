import assert from "node:assert/strict";
import { test } from "node:test";
import { inlineUploadBytes, uploadFile, uploadTarget } from "./uploads.js";

test("folder targets preserve hierarchy, Unicode, reserved characters and the root folder", () => {
  assert.equal(uploadTarget("s3://bucket/base/", { name: "a #%.txt", webkitRelativePath: "资料/子目录/a #%.txt" }), "s3://bucket/base/%E8%B5%84%E6%96%99/%E5%AD%90%E7%9B%AE%E5%BD%95/a%20%23%25.txt");
  assert.equal(uploadTarget("s3:/base", { name: "plain.txt" }), "s3:/base/plain.txt");
  for (const path of ["../x", "root/../x", "/root/x", "root//x", "root/./x", "root/\\x", "root/\0x"]) {
    assert.throws(() => uploadTarget("s3://bucket/", { webkitRelativePath: path }), /Invalid upload path/);
  }
});

test("small and empty files request a new version without bypassing overwrite protection", async () => {
  for (const size of [0, inlineUploadBytes]) {
    const file = new File([new Uint8Array(size)], "small.txt");
    let called = false;
    await uploadFile(file, { uri: "s3://bucket/small.txt", encodeBase64: (bytes) => Buffer.from(bytes).toString("base64"), invoke: async (method, params) => {
      assert.equal(method, "filesystem/write");
      assert.equal(params.overwrite, false);
      assert.equal(params.allowNewVersion, true);
      assert.equal(Buffer.from(params.dataBase64, "base64").length, size);
      called = true;
      return { success: true };
    } });
    assert.ok(called);
  }
});

test("streaming sends size, waits for bounded-window acknowledgements and storage completion", async () => {
  const file = new File([new Uint8Array(19 * 1024 * 1024 + 3)], "large.bin");
  let received = 0;
  let acknowledged = 0;
  let sealed = false;
  let polls = 0;
  const invoke = async (method, params) => {
    if (method.endsWith("/open")) {
      assert.equal(params.size, file.size);
      assert.equal(params.overwrite, false);
      return { channel: "test", windowBytes: 8 * 1024 * 1024 };
    }
    if (method.endsWith("/status")) {
      acknowledged = received;
      if (params.seal) { assert.equal(received, file.size); sealed = true; polls++; }
      return { processed: received, complete: sealed && polls > 1 };
    }
    assert.equal(method, "filesystem/upload/finish");
    assert.ok(sealed && polls > 1);
    return { success: true, versionId: "new-version" };
  };
  const result = await uploadFile(file, { uri: "s3://bucket/large.bin", invoke, sendBinary: async (channel, bytes) => {
    assert.equal(channel, "test");
    assert.ok(bytes.byteLength <= 1024 * 1024);
    received += bytes.byteLength;
    assert.ok(received - acknowledged <= 8 * 1024 * 1024);
  } });
  assert.equal(result.versionId, "new-version");
});

test("stream failures abort and never report successful completion", async () => {
  let aborted = false;
  await assert.rejects(uploadFile(new File([new Uint8Array(inlineUploadBytes + 1)], "file.bin"), {
    uri: "s3://bucket/file.bin", sendBinary: async () => { throw new Error("disconnected"); },
    invoke: async (method) => {
      if (method.endsWith("/open")) return { channel: "test" };
      assert.equal(method, "filesystem/upload/abort");
      aborted = true;
    },
  }), /disconnected/);
  assert.ok(aborted);
});

test("streaming supports hosts without crypto.randomUUID", async (context) => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis.crypto, "randomUUID");
  Object.defineProperty(globalThis.crypto, "randomUUID", { value: undefined, configurable: true });
  context.after(() => {
    if (descriptor) Object.defineProperty(globalThis.crypto, "randomUUID", descriptor);
    else delete globalThis.crypto.randomUUID;
  });
  const file = new File([new Uint8Array(inlineUploadBytes + 1)], "legacy.bin");
  await uploadFile(file, {
    uri: "s3://bucket/legacy.bin", sendBinary: async () => {},
    invoke: async (method, params) => {
      if (method.endsWith("/open")) {
        assert.match(params.uploadId, /^upload-\d+$/);
        return { channel: params.uploadId };
      }
      if (method.endsWith("/status")) return { processed: file.size, complete: true };
      assert.equal(method, "filesystem/upload/finish");
      return { success: true };
    },
  });
});

test("cancellation while waiting for storage aborts the active upload", async () => {
  const controller = new AbortController();
  let aborted = false;
  await assert.rejects(uploadFile(new File([new Uint8Array(inlineUploadBytes + 1)], "file.bin"), {
    uri: "s3://bucket/file.bin", signal: controller.signal, sendBinary: async () => {},
    invoke: async (method) => {
      if (method.endsWith("/open")) return { channel: "test" };
      if (method.endsWith("/status")) { controller.abort(); return { processed: 0, complete: false }; }
      assert.equal(method, "filesystem/upload/abort");
      aborted = true;
    },
  }), { name: "AbortError" });
  assert.ok(aborted);
});
