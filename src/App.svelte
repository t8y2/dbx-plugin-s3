<script>
  import { onDestroy, onMount } from "svelte";
  import ObjectList from "./components/ObjectList.svelte";
  import PreviewPane from "./components/PreviewPane.svelte";
  import { Button } from "./lib/components/ui/button/index.js";
  import * as Dialog from "./lib/components/ui/dialog/index.js";
  import { ArrowUp, Download, FolderOpen, FolderPlus, Pencil, RefreshCw, Trash2, Upload } from "@lucide/svelte";

  const providerId = "io.github.t8y2.s3.files";
  const previewLimits = { image: 4 * 1024 * 1024, video: 4 * 1024 * 1024, audio: 4 * 1024 * 1024, text: 2 * 1024 * 1024, markdown: 2 * 1024 * 1024, word: 4 * 1024 * 1024, spreadsheet: 2 * 1024 * 1024 };
  const archiveExtensions = new Set(["7z", "bz2", "gz", "rar", "tar", "tgz", "zip"]);
  const textExtensions = new Set(["c", "conf", "cpp", "css", "go", "h", "html", "ini", "java", "js", "json", "jsx", "log", "py", "rs", "sh", "sql", "toml", "ts", "tsx", "txt", "vue", "xml", "yaml", "yml"]);
  const copy = {
    en: {
      title: "S3 object browser", path: "Path", refresh: "Refresh", up: "Up", open: "Open", empty: "This folder is empty.",
      loading: "Loading objects…", preview: "Preview", noSelection: "Select an object to preview it.", binary: "This object cannot be previewed.", previewTooLarge: "This object is too large to preview here.", downloadTooLarge: "Downloads are limited to 256 MiB.", downloadUnavailable: "Downloads require a newer DBX host.", uploadLargeUnavailable: "Large uploads require a newer DBX host.",
      truncated: "Preview is truncated.", error: "Error", connection: "Connection", type: "Type",
      markdown: "Markdown", word: "Word document", spreadsheet: "Spreadsheet", sheet: "Sheet", noSheets: "No worksheets found.", noConnection: "No connection", file: "File", folder: "Folder", newFolder: "New folder", upload: "Upload", download: "Download", rename: "Rename", delete: "Delete", confirm: "Confirm", cancel: "Cancel", folderName: "Folder name", newName: "New name", confirmDelete: "Delete {name}?", invalidName: "Enter a valid name.", uploadLimit: "Files must be 4 MiB or smaller.", operationFailed: "Operation failed",
    },
    zh: {
      title: "S3 对象浏览器", path: "路径", refresh: "刷新", up: "上级", open: "打开", empty: "此目录为空。",
      loading: "正在加载对象…", preview: "预览", noSelection: "选择一个对象以预览。", binary: "此对象无法预览。", previewTooLarge: "对象过大，已跳过预览。", downloadTooLarge: "下载大小不能超过 256 MiB。", downloadUnavailable: "当前 DBX 宿主不支持下载。", uploadLargeUnavailable: "当前 DBX 宿主不支持大文件上传。",
      truncated: "预览内容已截断。", error: "错误", connection: "连接", type: "类型",
      markdown: "Markdown", word: "Word 文档", spreadsheet: "电子表格", sheet: "工作表", noSheets: "未找到工作表。", noConnection: "未连接", file: "文件", folder: "文件夹", newFolder: "新建文件夹", upload: "上传", download: "下载", rename: "重命名", delete: "删除", confirm: "确定", cancel: "取消", folderName: "文件夹名称", newName: "新名称", confirmDelete: "确定删除 {name} 吗？", invalidName: "请输入有效名称。", uploadLimit: "文件不能超过 4 MiB。", operationFailed: "操作失败",
    },
  };

  let text = $state(copy.en);
  let context = $state({});
  let entries = $state([]);
  let currentUri = $state("s3:/");
  let nextCursor = $state("");
  let bucketMode = $state(false);
  let selected = $state(null);
  let preview = $state({ kind: "empty", value: "", type: "", truncated: false });
  let loading = $state(false);
  let operating = $state(false);
  let error = $state("");
  let leftWidth = $state(42);
  let uploadInput = $state(null);
  let dialog = $state(null);
  let dialogOpen = $state(false);
  let contextMenu = $state(null);
  let previewRequest = 0;
  let previewReader;
  let mammothPromise;
  let xlsxPromise;

  const connectionId = () => context?.connectionId || "";
  const isZh = () => (window.dbxPlugin?.locale || "en").toLowerCase().startsWith("zh");
  const decode = (value) => window.dbxPlugin.decodeBase64(value);
  const extension = (name) => name.split(".").pop()?.toLowerCase() || "";
  const mimeByExtension = {
    aac: "audio/aac", avif: "image/avif", bmp: "image/bmp", csv: "text/csv", docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
    flac: "audio/flac", gif: "image/gif", jpeg: "image/jpeg", jpg: "image/jpeg", m4a: "audio/mp4", m4v: "video/x-m4v", md: "text/markdown",
    mov: "video/quicktime", mp3: "audio/mpeg", mp4: "video/mp4", mpeg: "video/mpeg", oga: "audio/ogg", ogg: "audio/ogg", png: "image/png",
    weba: "audio/webm", webm: "video/webm", webp: "image/webp", wav: "audio/wav", xls: "application/vnd.ms-excel", xlsb: "application/vnd.ms-excel.sheet.binary.macroEnabled.12",
    xlsm: "application/vnd.ms-excel.sheet.macroEnabled.12", xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
  };
  const normalizedType = (entry, result) => {
    const declared = (result?.contentType || entry?.contentType || "application/octet-stream").split(";", 1)[0].toLowerCase();
    return declared === "application/octet-stream" ? (mimeByExtension[extension(entry?.name || "")] || declared) : declared;
  };
  const previewKind = (entry, type = normalizedType(entry)) => {
    const ext = extension(entry?.name || "");
    if (archiveExtensions.has(ext) || type === "application/pdf") return "binary";
    if (type.startsWith("image/") || ["avif", "bmp", "gif", "jpeg", "jpg", "png", "webp"].includes(ext)) return "image";
    if (type.startsWith("video/") || ["m4v", "mov", "mp4", "mpeg", "webm"].includes(ext)) return "video";
    if (type.startsWith("audio/") || ["aac", "flac", "m4a", "mp3", "ogg", "wav", "weba"].includes(ext)) return "audio";
    if (ext === "docx" || type.includes("wordprocessingml.document")) return "word";
    if (["csv", "xls", "xlsb", "xlsm", "xlsx"].includes(ext) || type === "text/csv" || type.includes("spreadsheet") || type.includes("excel")) return "spreadsheet";
    if (["md", "markdown"].includes(ext)) return "markdown";
    if (type.startsWith("text/") || ["application/json", "application/javascript", "application/xml"].includes(type) || textExtensions.has(ext)) return "text";
    return "binary";
  };
  const loadMammoth = () => mammothPromise ||= import("mammoth").then(({ default: module }) => module);
  const loadXlsx = () => xlsxPromise ||= import("xlsx");

  async function invoke(method, params, options = {}) {
    return window.dbxPlugin.invoke(method, { ...params, connectionId: connectionId(), providerId }, options);
  }

  async function readStream(reader) {
    const chunks = [];
    let total = 0;
    try {
      while (true) {
        const result = await reader.read();
        if (result.done) break;
        chunks.push(result.value);
        total += result.value.byteLength;
      }
    } finally {
      if (previewReader === reader) previewReader = undefined;
    }
    const bytes = new Uint8Array(total);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.byteLength;
    }
    return bytes;
  }

  async function readPreview(uri, maxBytes) {
    if (typeof window.dbxPlugin?.stream !== "function") {
      const result = await invoke("filesystem/read", { uri, maxBytes }, { timeoutMs: 120000 });
      return { bytes: decode(result.dataBase64), metadata: result };
    }
    const opened = await window.dbxPlugin.stream("filesystem/stream/open", { uri, maxBytes, connectionId: connectionId(), providerId }, { timeoutMs: 120000 });
    previewReader = opened.stream.getReader();
    return { bytes: await readStream(previewReader), metadata: opened.metadata || {} };
  }

  async function load(uri = currentUri, append = false) {
    if (!connectionId() || loading || (append && !nextCursor)) return;
    loading = true;
    error = "";
    try {
      const result = await invoke("filesystem/list", { uri, cursor: append ? nextCursor : undefined, limit: 200 });
      const incoming = result?.entries || [];
      if (append) {
        const existingUris = new Set(entries.map((entry) => entry.uri));
        entries = [...entries, ...incoming.filter((candidate) => !existingUris.has(candidate.uri))];
      } else {
        entries = incoming;
      }
      currentUri = uri;
      nextCursor = result?.nextCursor || "";
      if (!append) bucketMode = !!result?.bucketMode;
      if (!append) clearPreview();
    } catch (cause) {
      error = cause?.message || String(cause);
    } finally {
      loading = false;
    }
  }

  async function selectEntry(entry) {
    contextMenu = null;
    const requestId = ++previewRequest;
    releasePreviewUrl();
    await previewReader?.cancel();
    previewReader = undefined;
    selected = entry;
    if (entry.kind === "directory" || entry.kind === "bucket") {
      preview = { kind: "empty", value: "", type: text.folder, truncated: false };
      return;
    }
    const type = normalizedType(entry);
    const kind = previewKind(entry, type);
    if (kind === "binary") {
      preview = { kind, value: "", type, truncated: false };
      return;
    }
    const maxBytes = previewLimits[kind];
    if (Number.isFinite(entry.size) && entry.size > maxBytes) {
      preview = { kind: "binary", value: "", type, truncated: true, message: text.previewTooLarge };
      return;
    }
    preview = { kind: "loading", value: "", type: "", truncated: false };
    try {
      const loaded = await readPreview(entry.uri, maxBytes);
      const bytes = loaded.bytes;
      if (requestId !== previewRequest) return;
      const result = loaded.metadata;
      const resultType = normalizedType(entry, result);
      if (result.truncated && ["image", "video", "audio", "word", "spreadsheet"].includes(kind)) {
        preview = { kind: "binary", value: "", type: resultType, truncated: true };
      } else if (kind === "image") {
        preview = { kind, value: URL.createObjectURL(new Blob([bytes], { type: resultType })), type: resultType, truncated: !!result.truncated };
      } else if (kind === "video") {
        preview = { kind, value: URL.createObjectURL(new Blob([bytes], { type: resultType })), type: resultType, truncated: !!result.truncated };
      } else if (kind === "audio") {
        preview = { kind, value: URL.createObjectURL(new Blob([bytes], { type: resultType })), type: resultType, truncated: !!result.truncated };
      } else if (kind === "word") {
        const mammoth = await loadMammoth();
        const word = await mammoth.extractRawText({ arrayBuffer: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) });
        if (requestId !== previewRequest) return;
        preview = { kind: "text", value: word.value, type: text.word, truncated: !!result.truncated };
      } else if (kind === "spreadsheet") {
        const XLSX = await loadXlsx();
        const workbook = XLSX.read(bytes, { type: "array", cellDates: true });
        const sheets = workbook.SheetNames.map((name) => {
          const rows = XLSX.utils.sheet_to_json(workbook.Sheets[name], { header: 1, defval: "", raw: false });
          const visibleRows = rows.slice(0, 500).map((row) => row.slice(0, 50));
          const columnCount = visibleRows.reduce((count, row) => Math.max(count, row.length), 0);
          return { name, rows: visibleRows, truncated: rows.length > 500 || rows.some((row) => row.length > 50), columnCount };
        });
        if (requestId !== previewRequest) return;
        preview = { kind: "spreadsheet", value: "", type: text.spreadsheet, sheets, sheetIndex: 0, truncated: !!result.truncated || sheets.some((sheet) => sheet.truncated) };
      } else if (kind === "markdown" || kind === "text") {
        const value = new TextDecoder().decode(bytes);
        preview = { kind, value, type: kind === "markdown" ? text.markdown : resultType, truncated: !!result.truncated };
      }
    } catch (cause) {
      if (requestId === previewRequest) preview = { kind: "error", value: cause?.message || String(cause), type: "", truncated: false };
    }
  }

  function openEntry(entry) {
    contextMenu = null;
    if (entry.kind === "directory" || entry.kind === "bucket") void load(entry.uri);
  }

  function openContextMenu(event, entry) {
    const menuWidth = 150;
    const menuHeight = entry.kind === "bucket" ? 40 : entry.kind === "directory" ? 80 : 116;
    contextMenu = {
      entry,
      x: Math.max(8, Math.min(event.clientX, window.innerWidth - menuWidth - 8)),
      y: Math.max(8, Math.min(event.clientY, window.innerHeight - menuHeight - 8)),
    };
  }

  function childUri(parent, name, directory = false) {
    const base = parent.endsWith("/") ? parent : `${parent}/`;
    return `${base}${encodeURIComponent(name)}${directory ? "/" : ""}`;
  }

  function containingUri(uri) {
    const value = uri.replace(/\/$/, "");
    const slash = value.lastIndexOf("/");
    return slash <= value.indexOf("://") + 2 ? `${value}/` : `${value.slice(0, slash + 1)}`;
  }

  async function runOperation(operation) {
    if (operating) return;
    operating = true;
    error = "";
    try {
      await operation();
      await load(currentUri);
    } catch (cause) {
      error = cause?.message || `${text.operationFailed}: ${String(cause)}`;
    } finally {
      operating = false;
    }
  }

  async function createFolder() {
    dialog = { kind: "create-folder", value: "" };
    dialogOpen = true;
  }

  function beginUpload() {
    uploadInput?.click();
  }

  async function uploadFiles(event) {
    const input = event.currentTarget;
    const files = [...(input.files || [])];
    input.value = "";
    if (!files.length) return;
    await runOperation(async () => {
      for (const file of files) {
        if (file.size > 4 * 1024 * 1024) {
          if (typeof window.dbxPlugin?.sendBinary !== "function") throw new Error(text.uploadLargeUnavailable);
          const uploadId = globalThis.crypto?.randomUUID?.() || `upload-${Date.now()}`;
          const opened = await invoke("filesystem/upload/open", { uploadId, uri: childUri(currentUri, file.name), contentType: file.type || "application/octet-stream", create: true, overwrite: false }, { timeoutMs: 120000 });
          try {
            for (let offset = 0; offset < file.size; offset += 1024 * 1024) {
              await window.dbxPlugin.sendBinary(opened.channel, await file.slice(offset, offset + 1024 * 1024).arrayBuffer());
            }
            await invoke("filesystem/upload/finish", { uploadId }, { timeoutMs: 120000 });
          } catch (cause) {
            await invoke("filesystem/upload/abort", { uploadId }, { timeoutMs: 120000 }).catch(() => undefined);
            throw cause;
          }
          continue;
        }
        const bytes = new Uint8Array(await file.arrayBuffer());
        await invoke("filesystem/write", { uri: childUri(currentUri, file.name), dataBase64: window.dbxPlugin.encodeBase64(bytes), contentType: file.type || "application/octet-stream", create: true, overwrite: false }, { timeoutMs: 120000 });
      }
    });
  }

  async function renameEntry(entry) {
    contextMenu = null;
    dialog = { kind: "rename", entry, value: entry.name };
    dialogOpen = true;
  }

  async function deleteEntry(entry) {
    contextMenu = null;
    dialog = { kind: "delete", entry, value: "" };
    dialogOpen = true;
  }

  function handleDialogOpenChange(open) {
    dialogOpen = open;
    if (!open) dialog = null;
  }

  function cancelDialog() {
    dialogOpen = false;
    dialog = null;
  }

  function closeContextMenu() {
    contextMenu = null;
  }

  async function confirmDialog() {
    const active = dialog;
    if (!active) return;
    if (active.kind === "delete") {
      cancelDialog();
      await runOperation(async () => {
        await invoke("filesystem/delete", { uri: active.entry.uri, recursive: active.entry.kind === "directory" });
        clearPreview();
      });
      return;
    }
    const name = active.value.trim();
    if (!name || /[\\/]/.test(name)) {
      error = text.invalidName;
      return;
    }
    if (active.kind === "rename" && name === active.entry.name) {
      cancelDialog();
      return;
    }
    cancelDialog();
    await runOperation(async () => {
      if (active.kind === "create-folder") {
        await invoke("filesystem/createDirectory", { uri: childUri(currentUri, name, true) });
      } else {
        await invoke("filesystem/rename", { sourceUri: active.entry.uri, targetUri: childUri(containingUri(active.entry.uri), name, active.entry.kind === "directory"), overwrite: false });
      }
    });
  }

  function clearPreview() {
    previewRequest += 1;
    releasePreviewUrl();
    void previewReader?.cancel();
    previewReader = undefined;
    selected = null;
    preview = { kind: "empty", value: "", type: "", truncated: false };
  }

  function releasePreviewUrl() {
    if (["image", "video", "audio"].includes(preview.kind) && preview.value) URL.revokeObjectURL(preview.value);
  }

  function parentUri() {
    const value = currentUri.replace(/\/$/, "");
    if (value.startsWith("s3://") && !value.slice(5).includes("/")) return "s3:/";
    const slash = value.lastIndexOf("/");
    return slash <= value.indexOf(":") ? "" : `${value.slice(0, slash)}/`;
  }

  async function downloadEntry(entry) {
    contextMenu = null;
    if (entry.kind === "directory" || entry.kind === "bucket") return;
    operating = true;
    error = "";
    try {
      if (typeof window.dbxPlugin?.stream !== "function") throw new Error(text.downloadUnavailable);
      const opened = await window.dbxPlugin.stream("filesystem/stream/open", { uri: entry.uri, maxBytes: 256 * 1024 * 1024, connectionId: connectionId(), providerId }, { timeoutMs: 120000 });
      const reader = opened.stream.getReader();
      const chunks = [];
      try {
        while (true) {
          const result = await reader.read();
          if (result.done) break;
          chunks.push(result.value);
        }
      } finally {
        await reader.cancel().catch(() => undefined);
      }
      if (opened.metadata?.truncated) throw new Error(text.downloadTooLarge);
      const blob = new Blob(chunks, { type: normalizedType(entry, opened.metadata) });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = entry.name;
      anchor.click();
      setTimeout(() => URL.revokeObjectURL(url), 0);
    } catch (cause) {
      error = cause?.message || `${text.operationFailed}: ${String(cause)}`;
    } finally {
      operating = false;
    }
  }

  function startResize(event) {
    event.preventDefault();
    const update = (move) => {
      const width = document.body.clientWidth || 1;
      leftWidth = Math.min(70, Math.max(24, (move.clientX / width) * 100));
    };
    const stop = () => {
      window.removeEventListener("pointermove", update);
      window.removeEventListener("pointerup", stop);
      document.body.style.cursor = "";
      document.body.style.userSelect = "";
    };
    document.body.style.cursor = "col-resize";
    document.body.style.userSelect = "none";
    window.addEventListener("pointermove", update);
    window.addEventListener("pointerup", stop, { once: true });
  }

  onMount(() => {
    const unsubscribe = window.dbxPlugin.onContext((value) => { context = value || {}; load("s3:/"); });
    window.dbxPlugin.ready.then((value) => {
      context = value || {};
      text = isZh() ? copy.zh : copy.en;
      load("s3:/");
    });
    return unsubscribe;
  });

  onDestroy(releasePreviewUrl);
</script>

<svelte:window onclick={closeContextMenu} oncontextmenu={(event) => event.preventDefault()} onkeydown={(event) => event.key === "Escape" && closeContextMenu()} />

<svelte:head><title>S3</title></svelte:head>

<main>
  <div class="toolbar">
    <div class="path-group">
      <Button variant="outline" size="icon-sm" aria-label={text.up} title={text.up} disabled={!parentUri() || loading} onclick={() => load(parentUri())}><ArrowUp size={14} /></Button>
      <input aria-label={text.path} bind:value={currentUri} onkeydown={(event) => event.key === "Enter" && load(currentUri)} />
    </div>
    <div class="toolbar-actions">
      <Button variant="outline" size="icon-sm" aria-label={text.refresh} title={text.refresh} disabled={loading || operating} onclick={() => load(currentUri)}><RefreshCw size={14} /></Button>
      <Button variant="outline" size="sm" disabled={loading || operating || (bucketMode && currentUri === "s3:/")} onclick={createFolder}><FolderPlus size={14} />{text.newFolder}</Button>
      <Button size="sm" disabled={loading || operating || (bucketMode && currentUri === "s3:/")} onclick={beginUpload}><Upload size={14} />{text.upload}</Button>
    </div>
    <input bind:this={uploadInput} hidden type="file" multiple onchange={uploadFiles} />
  </div>
  {#if error}<div class="error">{text.error}: {error}</div>{/if}
  <section class="split" style={`grid-template-columns: minmax(240px, ${leftWidth}%) 2px minmax(280px, 1fr)`}>
    <ObjectList {entries} {selected} {loading} {nextCursor} {text} onSelect={selectEntry} onOpen={openEntry} onContextMenu={openContextMenu} onLoadMore={() => load(currentUri, true)} />
    <button class="splitter" aria-label="Resize panels" onpointerdown={startResize}></button>
    <PreviewPane {selected} {preview} {text} onRename={renameEntry} onDelete={deleteEntry} onDownload={downloadEntry} onSheetChange={(value) => (preview = value)} />
  </section>
  {#if contextMenu}
    <div class="context-menu" data-dbx-context-menu role="menu" tabindex="-1" style={`left: ${contextMenu.x}px; top: ${contextMenu.y}px;`} oncontextmenu={(event) => event.preventDefault()}>
      {#if contextMenu.entry.kind === "directory" || contextMenu.entry.kind === "bucket"}<button role="menuitem" onclick={() => openEntry(contextMenu.entry)}><FolderOpen size={14} />{text.open}</button>{/if}
      {#if contextMenu.entry.kind !== "directory" && contextMenu.entry.kind !== "bucket"}<button role="menuitem" onclick={() => downloadEntry(contextMenu.entry)}><Download size={14} />{text.download}</button>{/if}
      {#if contextMenu.entry.kind !== "bucket"}<button role="menuitem" onclick={() => renameEntry(contextMenu.entry)}><Pencil size={14} />{text.rename}</button>{/if}
      {#if contextMenu.entry.kind !== "bucket"}<button class="danger" role="menuitem" onclick={() => deleteEntry(contextMenu.entry)}><Trash2 size={14} />{text.delete}</button>{/if}
    </div>
  {/if}
  <Dialog.Root bind:open={dialogOpen} onOpenChange={handleDialogOpenChange}>
    {#if dialog}
      <Dialog.Content showCloseButton={false} class="dialog-content">
        <Dialog.Header>
          <Dialog.Title>{dialog.kind === "delete" ? text.delete : dialog.kind === "rename" ? text.rename : text.newFolder}</Dialog.Title>
          {#if dialog.kind === "delete"}<Dialog.Description>{text.confirmDelete.replace("{name}", dialog.entry.name)}</Dialog.Description>{/if}
        </Dialog.Header>
        {#if dialog.kind !== "delete"}
          <label class="dialog-field">{dialog.kind === "rename" ? text.newName : text.folderName}<input bind:value={dialog.value} onkeydown={(event) => event.key === "Enter" && confirmDialog()} /></label>
        {/if}
        <Dialog.Footer class="dialog-actions">
          <Button variant="outline" onclick={cancelDialog}>{text.cancel}</Button>
          <Button variant={dialog.kind === "delete" ? "destructive" : "default"} onclick={confirmDialog}>{dialog.kind === "delete" ? text.delete : text.confirm}</Button>
        </Dialog.Footer>
      </Dialog.Content>
    {/if}
  </Dialog.Root>
</main>

<style>
  :global(*) { box-sizing: border-box; }
  :global(body) { margin: 0; min-width: 720px; min-height: 100vh; color: CanvasText; background: Canvas; font-family: Inter, ui-sans-serif, system-ui, sans-serif; }
  main { min-height: 100vh; display: flex; flex-direction: column; padding: 22px 26px; background: Canvas; }
  .toolbar { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 0; }
  .path-group, .toolbar-actions { display: flex; align-items: center; gap: 4px; }
  .path-group { min-width: 0; flex: 1; padding: 2px; border: 1px solid color-mix(in srgb, CanvasText 10%, transparent); border-radius: 5px; background: color-mix(in srgb, CanvasText 2%, transparent); }
  .toolbar input { min-width: 0; flex: 1; height: 30px; border: 1px solid color-mix(in srgb, CanvasText 14%, transparent); border-radius: 4px; padding: 6px 9px; color: inherit; background: color-mix(in srgb, CanvasText 4%, transparent); font: 12px ui-monospace, monospace; outline: none; }
  .toolbar input:focus { border-color: var(--color-primary, #6d5dfc); box-shadow: 0 0 0 2px color-mix(in srgb, var(--color-primary, #6d5dfc) 18%, transparent); }
  .error { margin: 8px 0; padding: 10px; border: 1px solid #d44a4a66; border-radius: 8px; color: #d44a4a; font-size: 12px; }
  .split { display: grid; min-height: 0; flex: 1; overflow: hidden; border: 1px solid color-mix(in srgb, CanvasText 12%, transparent); border-radius: 6px; box-shadow: 0 1px 3px color-mix(in srgb, CanvasText 7%, transparent); }
  .splitter { width: 2px; height: 100%; padding: 0; border-radius: 0; background: color-mix(in srgb, CanvasText 11%, transparent); cursor: col-resize; }.splitter:hover { background: var(--color-primary, #6d5dfc); }
  :global(html), :global(body), :global(#app) { height: 100%; overflow: hidden; }
  :global(body) { min-width: 0; color: var(--color-foreground, CanvasText); background: var(--color-background, Canvas); }
  main { height: 100%; min-height: 0; gap: 8px; padding: 10px 12px; background: var(--color-background, Canvas); }
  .toolbar { min-height: 32px; padding: 0; border: 0; background: transparent; }
  .path-group { border-color: var(--color-border, color-mix(in srgb, CanvasText 10%, transparent)); background: var(--color-background, Canvas); }
  .toolbar input { border-color: var(--color-border, color-mix(in srgb, CanvasText 14%, transparent)); background: var(--color-background, Canvas); }
  .split { border-color: var(--color-border, color-mix(in srgb, CanvasText 12%, transparent)); border-radius: 6px; background: var(--color-background, Canvas); }
  .splitter { width: 2px; background: var(--color-border, color-mix(in srgb, CanvasText 11%, transparent)); }
  .splitter:hover { background: var(--color-primary, #6d5dfc); }
  :global(.dialog-content) { width: min(360px, calc(100% - 32px)); }
  .dialog-field { display: grid; gap: 6px; color: var(--color-foreground, CanvasText); font-size: 12px; }
  .dialog-field input { width: 100%; padding: 8px 10px; color: var(--color-foreground, CanvasText); border: 1px solid var(--color-border, color-mix(in srgb, CanvasText 18%, transparent)); border-radius: var(--radius-md, 6px); outline: none; background: var(--color-muted, color-mix(in srgb, CanvasText 5%, transparent)); font: inherit; }
  .dialog-field input:focus { border-color: var(--color-primary, #6d5dfc); }
  :global(.dialog-actions) { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
  .context-menu { position: fixed; z-index: 9999; min-width: 160px; width: max-content; max-width: calc(100vw - 16px); padding: 4px; overflow-y: auto; border: 1px solid color-mix(in srgb, var(--color-foreground, CanvasText) 10%, transparent); border-radius: 6px; background: var(--color-popover, var(--color-background, Canvas)); color: var(--color-popover-foreground, var(--color-foreground, CanvasText)); box-shadow: 0 12px 32px color-mix(in srgb, CanvasText 18%, transparent); }
  .context-menu button { display: flex; align-items: center; gap: 8px; width: 100%; min-height: 24px; padding: 4px 8px; color: inherit; border: 0; border-radius: 6px; background: transparent; text-align: left; font-size: 13px; line-height: 16px; cursor: default; }
  .context-menu button:hover, .context-menu button:focus-visible { color: var(--color-accent-foreground, var(--color-foreground, CanvasText)); background: var(--color-accent, var(--color-muted, color-mix(in srgb, CanvasText 7%, transparent))); outline: none; }
  .context-menu button.danger { color: var(--color-destructive, #dc2626); }
  .context-menu button.danger:hover, .context-menu button.danger:focus-visible { color: var(--color-destructive, #dc2626); background: color-mix(in srgb, var(--color-destructive, #dc2626) 10%, transparent); }
</style>
