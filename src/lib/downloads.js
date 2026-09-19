export const maxDownloadBytes = 256 * 1024 * 1024;

export async function saveDownload({ host, fileName, params, signal, onProgress = () => {} }) {
  if (signal?.aborted) return null;
  const random = crypto.getRandomValues(new Uint8Array(16));
  random[6] = (random[6] & 15) | 64;
  random[8] = (random[8] & 63) | 128;
  const hex = [...random].map((value) => value.toString(16).padStart(2, "0")).join("");
  const downloadId = `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
  const unsubscribe = host.onEvent((event) => {
    if (event.method === "host.download.progress" && event.params?.downloadId === downloadId && !signal?.aborted) onProgress(event.params);
  });
  const cancel = () => { void host.cancelDownload(downloadId).catch(() => undefined); };
  signal?.addEventListener("abort", cancel, { once: true });
  try {
    return await host.downloadFile({ downloadId, fileName, params });
  } finally {
    signal?.removeEventListener("abort", cancel);
    unsubscribe();
  }
}

export async function readDownload({ open, signal, maxBytes = maxDownloadBytes, tooLarge, onMetadata = () => {}, onProgress = () => {} }) {
  const abortError = () => new DOMException("Download cancelled", "AbortError");
  const checkCancelled = () => { if (signal?.aborted) throw abortError(); };
  checkCancelled();
  let reader;
  let rejectCancelled;
  const cancelled = new Promise((resolve, reject) => { rejectCancelled = reject; });
  const cancelReader = () => { void reader?.cancel().catch(() => undefined); };
  const onAbort = () => { cancelReader(); rejectCancelled(abortError()); };
  signal?.addEventListener("abort", onAbort, { once: true });
  const read = async () => {
    const opened = await open();
    if (signal?.aborted) {
      void opened.stream.cancel().catch(() => undefined);
      throw abortError();
    }
    reader = opened.stream.getReader();
    try {
      if (opened.metadata?.size > maxBytes || opened.metadata?.truncated) throw new Error(tooLarge);
      onMetadata(opened.metadata || {});
      const chunks = [];
      let total = 0;
      while (true) {
        checkCancelled();
        const result = await reader.read();
        checkCancelled();
        if (result.done) break;
        total += result.value.byteLength;
        if (total > maxBytes) throw new Error(tooLarge);
        chunks.push(result.value);
        onProgress(total);
      }
      if (opened.metadata?.truncated) throw new Error(tooLarge);
      const bytes = new Uint8Array(total);
      let offset = 0;
      for (const chunk of chunks) {
        bytes.set(chunk, offset);
        offset += chunk.byteLength;
      }
      return { bytes, metadata: opened.metadata || {} };
    } finally {
      cancelReader();
      reader.releaseLock();
    }
  };
  try {
    return await Promise.race([read(), cancelled]);
  } finally {
    signal?.removeEventListener("abort", onAbort);
  }
}
