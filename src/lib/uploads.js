export const inlineUploadBytes = 256 * 1024;
const chunkBytes = 1024 * 1024;

export function uploadTarget(parent, file) {
  const path = file.webkitRelativePath || file.name;
  const segments = path.split("/");
  if (segments.some((segment) => !segment || segment === "." || segment === ".." || /[\\\0]/.test(segment))) {
    throw new Error(`Invalid upload path: ${path}`);
  }
  return `${parent.endsWith("/") ? parent : `${parent}/`}${segments.map(encodeURIComponent).join("/")}`;
}

function checkCancelled(signal) {
  if (signal?.aborted) throw new DOMException("Upload cancelled", "AbortError");
}

export async function uploadFile(file, { uri, invoke, sendBinary, encodeBase64, onProgress = () => {}, overwrite = false, signal }) {
  checkCancelled(signal);
  const parameters = { uri, contentType: file.type || "application/octet-stream", create: true, overwrite, allowNewVersion: true };
  if (file.size <= inlineUploadBytes) {
    const bytes = new Uint8Array(await file.arrayBuffer());
    checkCancelled(signal);
    const result = await invoke("filesystem/write", { ...parameters, dataBase64: encodeBase64(bytes) }, { timeoutMs: 120000 });
    onProgress(file.size, false);
    return result;
  }
  const uploadId = globalThis.crypto?.randomUUID?.() || `upload-${Date.now()}`;
  const opened = await invoke("filesystem/upload/open", { ...parameters, uploadId, size: file.size }, { timeoutMs: 120000 });
  const abort = () => invoke("filesystem/upload/abort", { uploadId }, { timeoutMs: 120000 }).catch(() => undefined);
  const onAbort = () => { void abort(); };
  signal?.addEventListener("abort", onAbort, { once: true });
  const waitForStatus = async (predicate, seal = false) => {
    do {
      checkCancelled(signal);
      const status = await invoke("filesystem/upload/status", { uploadId, seal });
      onProgress(status.processed, seal && !status.complete);
      if (predicate(status)) return;
      await new Promise((resolve) => setTimeout(resolve, 100));
    } while (true);
  };
  try {
    checkCancelled(signal);
    const windowBytes = Math.min(8 * chunkBytes, Math.max(chunkBytes, Number(opened.windowBytes) || chunkBytes));
    let pendingBytes = 0;
    for (let offset = 0; offset < file.size; offset += chunkBytes) {
      checkCancelled(signal);
      const end = Math.min(file.size, offset + chunkBytes);
      const chunk = await file.slice(offset, end).arrayBuffer();
      checkCancelled(signal);
      await sendBinary(opened.channel, chunk);
      pendingBytes += end - offset;
      if (pendingBytes >= windowBytes && end < file.size) {
        await waitForStatus((status) => status.processed >= end);
        pendingBytes = 0;
      }
    }
    await waitForStatus((status) => status.complete, true);
    checkCancelled(signal);
    const result = await invoke("filesystem/upload/finish", { uploadId }, { timeoutMs: 120000 });
    onProgress(file.size, false);
    return result;
  } catch (cause) {
    await abort();
    checkCancelled(signal);
    throw cause;
  } finally {
    signal?.removeEventListener("abort", onAbort);
  }
}
