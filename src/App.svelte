<script>
  import { onMount } from "svelte";
  import mammoth from "mammoth";
  import * as XLSX from "xlsx";
  import ObjectList from "./components/ObjectList.svelte";
  import PreviewPane from "./components/PreviewPane.svelte";

  const providerId = "io.github.t8y2.s3.files";
  const copy = {
    en: {
      title: "S3 object browser", path: "Path", refresh: "Refresh", up: "Up", empty: "This folder is empty.",
      loading: "Loading objects…", preview: "Preview", noSelection: "Select an object to preview it.", binary: "This object cannot be previewed.",
      truncated: "Preview is truncated.", error: "Error", connection: "Connection", type: "Type",
      markdown: "Markdown", word: "Word document", spreadsheet: "Spreadsheet", sheet: "Sheet", noSheets: "No worksheets found.", noConnection: "No connection", file: "File", folder: "Folder",
    },
    zh: {
      title: "S3 对象浏览器", path: "路径", refresh: "刷新", up: "上级", empty: "此目录为空。",
      loading: "正在加载对象…", preview: "预览", noSelection: "选择一个对象以预览。", binary: "此对象无法预览。",
      truncated: "预览内容已截断。", error: "错误", connection: "连接", type: "类型",
      markdown: "Markdown", word: "Word 文档", spreadsheet: "电子表格", sheet: "工作表", noSheets: "未找到工作表。", noConnection: "未连接", file: "文件", folder: "文件夹",
    },
  };

  let text = $state(copy.en);
  let context = $state({});
  let entries = $state([]);
  let currentUri = $state("s3:/");
  let nextCursor = $state("");
  let selected = $state(null);
  let preview = $state({ kind: "empty", value: "", type: "", truncated: false });
  let loading = $state(false);
  let error = $state("");
  let leftWidth = $state(42);

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

  async function invoke(method, params, options = {}) {
    return window.dbxPlugin.invoke(method, { ...params, connectionId: connectionId(), providerId }, options);
  }

  async function load(uri = currentUri, append = false) {
    if (!connectionId() || loading || (append && !nextCursor)) return;
    loading = true;
    error = "";
    try {
      const result = await invoke("filesystem/list", { uri, cursor: append ? nextCursor : undefined, limit: 200 });
      const incoming = result?.entries || [];
      entries = append ? [...entries, ...incoming.filter((candidate) => !entries.some((entry) => entry.uri === candidate.uri))] : incoming;
      currentUri = uri;
      nextCursor = result?.nextCursor || "";
      if (!append) clearPreview();
    } catch (cause) {
      error = cause?.message || String(cause);
    } finally {
      loading = false;
    }
  }

  async function selectEntry(entry) {
    selected = entry;
    if (entry.kind === "directory") {
      await load(entry.uri);
      return;
    }
    preview = { kind: "loading", value: "", type: "", truncated: false };
    try {
      const result = await invoke("filesystem/read", { uri: entry.uri, maxBytes: 32 * 1024 * 1024 }, { timeoutMs: 120000 });
      const type = normalizedType(entry, result);
      const bytes = decode(result.dataBase64);
      const ext = extension(entry.name);
      const isImage = type.startsWith("image/") || ["avif", "bmp", "gif", "jpeg", "jpg", "png", "webp"].includes(ext);
      const isVideo = type.startsWith("video/") || ["m4v", "mov", "mp4", "mpeg", "webm"].includes(ext);
      const isAudio = type.startsWith("audio/") || ["aac", "flac", "m4a", "mp3", "ogg", "wav", "weba"].includes(ext);
      const isWord = ext === "docx" || type.includes("wordprocessingml.document");
      const isSpreadsheet = ["csv", "xls", "xlsb", "xlsm", "xlsx"].includes(ext) || type === "text/csv" || type.includes("spreadsheet") || type.includes("excel");
      const isMarkdown = ["md", "markdown"].includes(ext);
      if (result.truncated && (isImage || isVideo || isAudio || isWord || isSpreadsheet)) {
        preview = { kind: "binary", value: "", type, truncated: true };
      } else if (isImage || type.startsWith("image/")) {
        preview = { kind: "image", value: URL.createObjectURL(new Blob([bytes], { type })), type, truncated: !!result.truncated };
      } else if (isVideo || type.startsWith("video/")) {
        preview = { kind: "video", value: URL.createObjectURL(new Blob([bytes], { type })), type, truncated: !!result.truncated };
      } else if (isAudio || type.startsWith("audio/")) {
        preview = { kind: "audio", value: URL.createObjectURL(new Blob([bytes], { type })), type, truncated: !!result.truncated };
      } else if (isWord) {
        const word = await mammoth.extractRawText({ arrayBuffer: bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength) });
        preview = { kind: "text", value: word.value, type: text.word, truncated: !!result.truncated };
      } else if (isSpreadsheet) {
        const workbook = XLSX.read(bytes, { type: "array", cellDates: true });
        const sheets = workbook.SheetNames.map((name) => {
          const rows = XLSX.utils.sheet_to_json(workbook.Sheets[name], { header: 1, defval: "", raw: false });
          const visibleRows = rows.slice(0, 500).map((row) => row.slice(0, 50));
          const columnCount = visibleRows.reduce((count, row) => Math.max(count, row.length), 0);
          return { name, rows: visibleRows, truncated: rows.length > 500 || rows.some((row) => row.length > 50), columnCount };
        });
        preview = { kind: "spreadsheet", value: "", type: text.spreadsheet, sheets, sheetIndex: 0, truncated: !!result.truncated || sheets.some((sheet) => sheet.truncated) };
      } else {
        const value = new TextDecoder().decode(bytes);
        preview = { kind: isMarkdown ? "markdown" : "text", value, type: isMarkdown ? text.markdown : type, truncated: !!result.truncated };
      }
    } catch (cause) {
      preview = { kind: "error", value: cause?.message || String(cause), type: "", truncated: false };
    }
  }

  function clearPreview() {
    if (["image", "video", "audio"].includes(preview.kind) && preview.value) URL.revokeObjectURL(preview.value);
    selected = null;
    preview = { kind: "empty", value: "", type: "", truncated: false };
  }

  function parentUri() {
    const value = currentUri.replace(/\/$/, "");
    const slash = value.lastIndexOf("/");
    return slash <= value.indexOf(":") ? "" : `${value.slice(0, slash)}/`;
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
</script>

<svelte:head><title>S3</title></svelte:head>

<main>
  <div class="toolbar">
    <button class="secondary" disabled={!parentUri() || loading} onclick={() => load(parentUri())}>{text.up}</button>
    <input aria-label={text.path} bind:value={currentUri} onkeydown={(event) => event.key === "Enter" && load(currentUri)} />
    <button class="secondary" disabled={loading} onclick={() => load(currentUri)}>{text.refresh}</button>
  </div>
  {#if error}<div class="error">{text.error}: {error}</div>{/if}
  <section class="split" style={`grid-template-columns: minmax(240px, ${leftWidth}%) 4px minmax(280px, 1fr)`}>
    <ObjectList {entries} {selected} {loading} {nextCursor} {text} onSelect={selectEntry} onLoadMore={() => load(currentUri, true)} />
    <button class="splitter" aria-label="Resize panels" onpointerdown={startResize}></button>
    <PreviewPane {selected} {preview} {text} onSheetChange={(value) => (preview = value)} />
  </section>
</main>

<style>
  :global(*) { box-sizing: border-box; }
  :global(body) { margin: 0; min-width: 720px; min-height: 100vh; color: CanvasText; background: Canvas; font-family: Inter, ui-sans-serif, system-ui, sans-serif; }
  main { min-height: 100vh; display: flex; flex-direction: column; padding: 22px 26px; background: radial-gradient(circle at 0 0, rgba(109,93,252,.16), transparent 36%), Canvas; }
  .toolbar { display: flex; align-items: center; }
  .toolbar { gap: 8px; padding: 10px 0; }.toolbar input { min-width: 0; flex: 1; border: 1px solid color-mix(in srgb, CanvasText 18%, transparent); border-radius: 8px; padding: 8px 10px; color: inherit; background: color-mix(in srgb, CanvasText 5%, transparent); font: 12px ui-monospace, monospace; }
  button { border: 0; border-radius: 8px; padding: 8px 11px; color: white; background: #6d5dfc; font: inherit; font-size: 12px; cursor: pointer; } button.secondary { color: inherit; border: 1px solid color-mix(in srgb, CanvasText 16%, transparent); background: transparent; } button:disabled { cursor: default; opacity: .45; }
  .error { margin: 8px 0; padding: 10px; border: 1px solid #d44a4a66; border-radius: 8px; color: #d44a4a; font-size: 12px; }
  .split { display: grid; min-height: 0; flex: 1; overflow: hidden; border: 1px solid color-mix(in srgb, CanvasText 14%, transparent); border-radius: 12px; }
  .splitter { width: 4px; height: 100%; padding: 0; border-radius: 0; background: color-mix(in srgb, CanvasText 13%, transparent); cursor: col-resize; }.splitter:hover { background: #6d5dfc; }
  :global(html), :global(body), :global(#app) { height: 100%; overflow: hidden; }
  :global(body) { min-width: 0; color: var(--color-foreground, CanvasText); background: var(--color-background, Canvas); }
  main { height: 100%; min-height: 0; gap: 8px; padding: 12px 14px; background: var(--color-background, Canvas); }
  .toolbar { min-height: 32px; padding: 0; }
  .toolbar input { border-color: var(--color-border, color-mix(in srgb, CanvasText 18%, transparent)); background: var(--color-muted, color-mix(in srgb, CanvasText 5%, transparent)); }
  button { border-radius: var(--radius-md, 6px); color: var(--color-primary-foreground, white); background: var(--color-primary, #6d5dfc); }
  button.secondary { color: var(--color-foreground, CanvasText); border-color: var(--color-border, color-mix(in srgb, CanvasText 16%, transparent)); background: transparent; }
  .split { border-color: var(--color-border, color-mix(in srgb, CanvasText 14%, transparent)); border-radius: var(--radius-lg, 8px); background: var(--color-background, Canvas); }
  .splitter { width: 4px; background: var(--color-border, color-mix(in srgb, CanvasText 13%, transparent)); }
  .splitter:hover { background: var(--color-primary, #6d5dfc); }
</style>
