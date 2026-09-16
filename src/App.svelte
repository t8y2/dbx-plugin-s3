<script>
  import { onDestroy, onMount } from "svelte";
  import ObjectList from "./components/ObjectList.svelte";
  import PreviewPane from "./components/PreviewPane.svelte";
  import FolderTree from "./components/FolderTree.svelte";
  import { Button } from "./lib/components/ui/button/index.js";
  import * as Dialog from "./lib/components/ui/dialog/index.js";
  import { ArrowUp, ChevronRight, Download, FileArchive, FolderOpen, FolderPlus, Link2, ListTree, Pencil, RefreshCw, Trash2, Upload } from "@lucide/svelte";

  const providerId = "io.github.t8y2.s3.files";
  const previewLimits = { image: 4 * 1024 * 1024, video: 4 * 1024 * 1024, audio: 4 * 1024 * 1024, text: 2 * 1024 * 1024, markdown: 2 * 1024 * 1024, word: 4 * 1024 * 1024, spreadsheet: 2 * 1024 * 1024 };
  // Base64 inflates JSON-RPC requests past the host bridge limit at a few MiB,
  // so anything larger than this goes through the binary upload channel.
  const inlineUploadBytes = 256 * 1024;
  const archiveExtensions = new Set(["7z", "bz2", "gz", "rar", "tar", "tgz", "zip"]);
  const textExtensions = new Set(["c", "conf", "cpp", "css", "go", "h", "html", "ini", "java", "js", "json", "jsx", "log", "py", "rs", "sh", "sql", "toml", "ts", "tsx", "txt", "vue", "xml", "yaml", "yml"]);
  const copy = {
    en: {
      title: "S3 object browser", path: "Path", refresh: "Refresh", up: "Up", open: "Open", empty: "This folder is empty.",
      loading: "Loading objects…", preview: "Preview", noSelection: "Select an object to preview it.", binary: "This object cannot be previewed.", previewTooLarge: "This object is too large to preview here.", downloadTooLarge: "Downloads are limited to 256 MiB.", downloadUnavailable: "Downloads require a newer DBX host.", uploadLargeUnavailable: "Large uploads require a newer DBX host.", archiveTooLarge: "ZIP downloads are limited to 220 MiB of source data.", uploading: "Uploading", downloading: "Downloading", downloadZip: "Download as ZIP", downloadZipCount: "ZIP {count} items",
      truncated: "Preview is truncated.", error: "Error", connection: "Connection", type: "Type",
      markdown: "Markdown", word: "Word document", spreadsheet: "Spreadsheet", sheet: "Sheet", noSheets: "No worksheets found.", noConnection: "No connection", file: "File", folder: "Folder", newFolder: "New folder", upload: "Upload", download: "Download", rename: "Rename", delete: "Delete", deleteCount: "Delete {count} items", confirm: "Confirm", cancel: "Cancel", folderName: "Folder name", newName: "New name", confirmDelete: "Delete {name}?", confirmDeleteCount: "Delete {count} items? This cannot be undone.", cannotDeleteBucket: "Buckets cannot be deleted from here.", invalidName: "Enter a valid name.", uploadLimit: "Files must be 4 MiB or smaller.", operationFailed: "Operation failed",
      share: "Share", shareExpires: "Link validity", shareExpiresHour: "1 hour", shareExpiresDay: "24 hours", shareExpiresWeek: "7 days", copy: "Copy link", copied: "Copied", copyBlocked: "Auto-copy was blocked — the link is selected, press ⌘C / Ctrl+C to copy.", shareFailed: "Could not create the share link.",
      folderTree: "Folder tree", expandFolder: "Expand folder", collapseFolder: "Collapse folder", loadMore: "Load more", noFolders: "No folders.", editPath: "Edit path", rootLabel: "S3",
    },
    zh: {
      title: "S3 对象浏览器", path: "路径", refresh: "刷新", up: "上级", open: "打开", empty: "此目录为空。",
      loading: "正在加载对象…", preview: "预览", noSelection: "选择一个对象以预览。", binary: "此对象无法预览。", previewTooLarge: "对象过大，已跳过预览。", downloadTooLarge: "下载大小不能超过 256 MiB。", downloadUnavailable: "当前 DBX 宿主不支持下载。", uploadLargeUnavailable: "当前 DBX 宿主不支持大文件上传。", archiveTooLarge: "ZIP 打包的源数据不能超过 220 MiB。", uploading: "正在上传", downloading: "正在下载", downloadZip: "下载为 ZIP", downloadZipCount: "打包 {count} 项",
      truncated: "预览内容已截断。", error: "错误", connection: "连接", type: "类型",
      markdown: "Markdown", word: "Word 文档", spreadsheet: "电子表格", sheet: "工作表", noSheets: "未找到工作表。", noConnection: "未连接", file: "文件", folder: "文件夹", newFolder: "新建文件夹", upload: "上传", download: "下载", rename: "重命名", delete: "删除", deleteCount: "删除 {count} 项", confirm: "确定", cancel: "取消", folderName: "文件夹名称", newName: "新名称", confirmDelete: "确定删除 {name} 吗？", confirmDeleteCount: "确定删除 {count} 项吗？删除后无法恢复。", cannotDeleteBucket: "不支持在此删除存储桶。", invalidName: "请输入有效名称。", uploadLimit: "文件不能超过 4 MiB。", operationFailed: "操作失败",
      share: "分享", shareExpires: "链接有效期", shareExpiresHour: "1 小时", shareExpiresDay: "24 小时", shareExpiresWeek: "7 天", copy: "复制链接", copied: "已复制", copyBlocked: "自动复制被拦截,已全选链接,请按 ⌘C / Ctrl+C 复制。", shareFailed: "生成分享链接失败。",
      folderTree: "目录树", expandFolder: "展开文件夹", collapseFolder: "折叠文件夹", loadMore: "加载更多", noFolders: "暂无文件夹。", editPath: "编辑路径", rootLabel: "S3",
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
  let checkedUris = $state([]);
  let transfer = $state(null);
  let leftWidth = $state(42);
  let treeNodes = $state({});
  let treeVisible = $state(true);
  let treeWidth = $state(192);
  let pathEditing = $state(false);
  let uploadInput = $state(null);
  let dialog = $state(null);
  let dialogOpen = $state(false);
  let contextMenu = $state(null);
  let shareCopied = $state(false);
  let shareCopyBlocked = $state(false);
  let shareUrlInput = $state(null);
  let previewRequest = 0;
  let previewReader;
  let mammothPromise;
  let xlsxPromise;
  let shareRequest = 0;

  const shareExpiryOptions = $derived([
    { value: 3600, label: text.shareExpiresHour },
    { value: 86400, label: text.shareExpiresDay },
    { value: 604800, label: text.shareExpiresWeek },
  ]);

  const connectionId = () => context?.connectionId || "";
  const isZh = () => (window.dbxPlugin?.locale || "en").toLowerCase().startsWith("zh");
  const decode = (value) => window.dbxPlugin.decodeBase64(value);
  const TREE_ROOT = "s3:/";

  function treeAncestors(uri) {
    const parts = uri.replace(/^s3:\/*/, "").split("/").filter(Boolean);
    const ancestors = [TREE_ROOT];
    let accumulated = "s3://";
    for (const part of parts) {
      accumulated += `${part}/`;
      ancestors.push(accumulated);
    }
    return ancestors;
  }

  function treeNode(uri) {
    treeNodes[uri] ||= { open: false, loaded: false, loading: false, children: [], nextCursor: "" };
    return treeNodes[uri];
  }

  // Tree children come from a directories-only listing: the sidecar skips
  // files server-side, so one request returns a full page of folders no
  // matter how many files sit between them.
  async function loadTreeChildren(uri, startCursor = "") {
    const node = treeNode(uri);
    if (node.loading) return;
    node.loading = true;
    if (!startCursor) {
      node.children = [];
      node.nextCursor = "";
    }
    try {
      const result = await invoke("filesystem/list", { uri, cursor: startCursor || undefined, limit: 1000, directoriesOnly: true });
      if (treeNodes[uri] !== node) return;
      const folders = (result?.entries || []).filter((entry) => entry.kind === "directory" || entry.kind === "bucket");
      node.children = startCursor ? [...(node.children || []), ...folders] : folders;
      node.nextCursor = result?.nextCursor || "";
      node.loaded = true;
    } catch {
      // The object list surfaces listing errors; the tree is a secondary view
      // and degrades to the folders gathered so far.
      node.loaded = true;
    } finally {
      node.loading = false;
    }
  }

  function toggleTreeNode(uri) {
    const node = treeNode(uri);
    node.open = !node.open;
    // Re-expansions reuse the cached children; operations refresh open nodes.
    if (node.open && !node.loaded && !node.loading) void loadTreeChildren(uri);
  }

  function continueTrees() {
    // Scroll-to-bottom continuation: pull the next folder batch for every
    // expanded node that still has a cursor.
    for (const [uri, node] of Object.entries(treeNodes)) {
      if (node.open && node.nextCursor && !node.loading) void loadTreeChildren(uri, node.nextCursor);
    }
  }

  function selectTreeNode(uri) {
    void load(uri);
  }

  async function expandTreePath(uri) {
    const ancestors = treeAncestors(uri);
    // Open every level above the target folder first so the spinner shows
    // while slow listings load, never the target itself.
    for (const ancestor of uri === TREE_ROOT ? ancestors : ancestors.slice(0, -1)) {
      const node = treeNode(ancestor);
      node.open = true;
      if (!node.loaded && !node.loading) await loadTreeChildren(ancestor);
    }
  }

  async function refreshOpenTree() {
    for (const [uri, node] of Object.entries(treeNodes)) {
      if (node.open && !node.loading) void loadTreeChildren(uri);
    }
  }

  const breadcrumbs = $derived.by(() => {
    const parts = currentUri.replace(/^s3:\/*/, "").split("/").filter(Boolean);
    const crumbs = [{ uri: TREE_ROOT, name: text.rootLabel }];
    let accumulated = "s3://";
    for (const part of parts) {
      accumulated += `${part}/`;
      crumbs.push({ uri: accumulated, name: decodeURIComponent(part) });
    }
    return crumbs;
  });
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

  async function readStream(reader, onProgress) {
    const chunks = [];
    let total = 0;
    try {
      while (true) {
        const result = await reader.read();
        if (result.done) break;
        chunks.push(result.value);
        total += result.value.byteLength;
        onProgress?.(total);
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
      if (!append) {
        bucketMode = !!result?.bucketMode;
        checkedUris = [];
        clearPreview();
        void expandTreePath(uri);
      }
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
    const menuHeight = entry.kind === "directory" || entry.kind === "bucket" ? 80 : 152;
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

  async function runOperation(operation, refreshUri = currentUri) {
    if (operating) return;
    operating = true;
    error = "";
    try {
      await operation();
      // The user may have navigated elsewhere mid-operation (uploads run for
      // minutes); only reload the folder the operation targeted if it is still
      // on screen, never yank them back to it.
      if (currentUri === refreshUri) await load(currentUri);
    } catch (cause) {
      error = cause?.message || `${text.operationFailed}: ${String(cause)}`;
    } finally {
      operating = false;
      transfer = null;
      void refreshOpenTree();
      // Closed nodes keep their cached children; drop the cache of the folder
      // this operation touched so a later expansion refetches it.
      const touched = treeNode(refreshUri);
      if (touched && !touched.open) touched.loaded = false;
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
    // Pin the target folder up front: a multi-file upload runs for a while
    // and later files must land next to the first, not in whatever folder
    // the user is browsing by then.
    const uploadUri = currentUri;
    const canStream = typeof window.dbxPlugin?.sendBinary === "function";
    await runOperation(async () => {
      const totalBytes = files.reduce((sum, file) => sum + file.size, 0);
      transfer = { kind: "upload", name: "", sent: 0, total: totalBytes, fileIndex: 0, fileCount: files.length };
      let completedBytes = 0;
      for (const file of files) {
        transfer.fileIndex += 1;
        transfer.name = file.name;
        transfer.sent = completedBytes;
        if (file.size > 4 * 1024 * 1024 && !canStream) throw new Error(text.uploadLargeUnavailable);
        if (canStream && file.size > inlineUploadBytes) {
          const uploadId = globalThis.crypto?.randomUUID?.() || `upload-${Date.now()}`;
          const opened = await invoke("filesystem/upload/open", { uploadId, uri: childUri(uploadUri, file.name), contentType: file.type || "application/octet-stream", create: true, overwrite: false }, { timeoutMs: 120000 });
          try {
            for (let offset = 0; offset < file.size; offset += 1024 * 1024) {
              await window.dbxPlugin.sendBinary(opened.channel, await file.slice(offset, offset + 1024 * 1024).arrayBuffer());
              transfer.sent = Math.min(totalBytes, completedBytes + Math.min(offset + 1024 * 1024, file.size));
            }
            await invoke("filesystem/upload/finish", { uploadId }, { timeoutMs: 120000 });
          } catch (cause) {
            await invoke("filesystem/upload/abort", { uploadId }, { timeoutMs: 120000 }).catch(() => undefined);
            throw cause;
          }
        } else {
          const bytes = new Uint8Array(await file.arrayBuffer());
          await invoke("filesystem/write", { uri: childUri(uploadUri, file.name), dataBase64: window.dbxPlugin.encodeBase64(bytes), contentType: file.type || "application/octet-stream", create: true, overwrite: false }, { timeoutMs: 120000 });
        }
        completedBytes += file.size;
        transfer.sent = completedBytes;
      }
    }, uploadUri);
  }

  function toggleCheck(entry) {
    checkedUris = checkedUris.includes(entry.uri) ? checkedUris.filter((uri) => uri !== entry.uri) : [...checkedUris, entry.uri];
  }

  function toggleCheckAll() {
    const uris = entries.map((entry) => entry.uri);
    const allChecked = uris.length > 0 && uris.every((uri) => checkedUris.includes(uri));
    checkedUris = allChecked ? checkedUris.filter((uri) => !uris.includes(uri)) : [...new Set([...checkedUris, ...uris])];
  }

  function archiveFileName(uris) {
    if (uris.length === 1) {
      const segments = uris[0].replace(/\/+$/, "").split("/").filter(Boolean);
      const name = decodeURIComponent(segments[segments.length - 1] || "s3");
      return `${name}.zip`;
    }
    return `s3-files-${new Date().toISOString().slice(0, 19).replace(/[-:T]/g, "")}.zip`;
  }

  function transferPercent() {
    if (!transfer?.total) return 0;
    return Math.min(100, Math.floor((transfer.sent / transfer.total) * 100));
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

  function deleteChecked() {
    const deletable = entries.filter((entry) => checkedUris.includes(entry.uri) && entry.kind !== "bucket");
    if (!deletable.length) {
      error = text.cannotDeleteBucket;
      return;
    }
    dialog = { kind: "delete-batch", count: deletable.length, value: "" };
    dialogOpen = true;
  }

  function shareEntry(entry) {
    contextMenu = null;
    if (entry.kind === "directory" || entry.kind === "bucket") return;
    shareCopied = false;
    shareCopyBlocked = false;
    dialog = { kind: "share", entry, expires: 86400, url: "", loading: true, shareError: "" };
    dialogOpen = true;
    void fetchShareUrl(entry, 86400);
  }

  async function fetchShareUrl(entry, expires) {
    const requestId = ++shareRequest;
    dialog.loading = true;
    dialog.url = "";
    dialog.shareError = "";
    shareCopyBlocked = false;
    try {
      const result = await invoke("filesystem/presign", { uri: entry.uri, expires });
      if (requestId !== shareRequest || dialog?.kind !== "share") return;
      dialog.loading = false;
      dialog.url = result?.url || "";
    } catch (cause) {
      if (requestId !== shareRequest || dialog?.kind !== "share") return;
      dialog.loading = false;
      dialog.shareError = cause?.message || text.shareFailed;
    }
  }

  async function copyShareUrl() {
    const value = dialog?.url;
    if (!value) return;
    let ok = false;
    if (typeof window.dbxPlugin?.copy === "function") {
      // The host bridge writes the system clipboard directly and is the only
      // path that works inside the sandboxed workbench iframe on every host.
      try {
        await window.dbxPlugin.copy(value);
        ok = true;
      } catch {
        ok = false;
      }
    }
    if (!ok) {
      try {
        await navigator.clipboard.writeText(value);
        ok = true;
      } catch {
        ok = false;
      }
    }
    if (!ok) {
      // Sandboxed workbench iframes (DBX and the dev host) deny the async
      // clipboard API and offscreen-copy tricks, but copying the selection of
      // a focused, visible input still reaches the system clipboard.
      shareUrlInput?.focus();
      shareUrlInput?.select();
      try {
        ok = document.execCommand("copy");
      } catch {
        ok = false;
      }
    }
    shareCopied = ok;
    shareCopyBlocked = !ok;
    if (ok) setTimeout(() => (shareCopied = false), 2000);
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
    if (!active || active.kind === "share") return;
    if (active.kind === "delete") {
      cancelDialog();
      await runOperation(async () => {
        await invoke("filesystem/delete", { uri: active.entry.uri, recursive: active.entry.kind === "directory" });
        checkedUris = checkedUris.filter((uri) => uri !== active.entry.uri);
        clearPreview();
      });
      return;
    }
    if (active.kind === "delete-batch") {
      const targets = entries.filter((entry) => checkedUris.includes(entry.uri) && entry.kind !== "bucket");
      cancelDialog();
      await runOperation(async () => {
        for (const entry of targets) {
          await invoke("filesystem/delete", { uri: entry.uri, recursive: entry.kind === "directory" });
        }
        const deletedUris = new Set(targets.map((entry) => entry.uri));
        checkedUris = checkedUris.filter((uri) => !deletedUris.has(uri));
        if (selected && deletedUris.has(selected.uri)) clearPreview();
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

  async function saveBytes(fileName, contentType, bytes) {
    if (typeof window.dbxPlugin.saveFile === "function") {
      // The sandboxed iframe cannot trigger downloads (WKWebView cancels blob
      // navigations), so hand the bytes to the host's native save dialog.
      // A null result means the user dismissed the dialog; stay quiet.
      await window.dbxPlugin.saveFile({ fileName, contentType }, bytes);
      return;
    }
    const url = URL.createObjectURL(new Blob([bytes], { type: contentType }));
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = fileName;
    anchor.click();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  async function downloadEntry(entry) {
    contextMenu = null;
    if (entry.kind === "directory" || entry.kind === "bucket") return;
    operating = true;
    error = "";
    transfer = { kind: "download", name: entry.name, sent: 0, total: Number.isFinite(entry.size) ? entry.size : 0 };
    try {
      if (typeof window.dbxPlugin?.stream !== "function") throw new Error(text.downloadUnavailable);
      const opened = await window.dbxPlugin.stream("filesystem/stream/open", { uri: entry.uri, maxBytes: 256 * 1024 * 1024, connectionId: connectionId(), providerId }, { timeoutMs: 120000 });
      transfer.total = opened.metadata?.size || transfer.total;
      const bytes = await readStream(opened.stream.getReader(), (sent) => { transfer.sent = sent; });
      if (opened.metadata?.truncated) throw new Error(text.downloadTooLarge);
      await saveBytes(entry.name, normalizedType(entry, opened.metadata), bytes);
    } catch (cause) {
      error = cause?.message || `${text.operationFailed}: ${String(cause)}`;
    } finally {
      operating = false;
      transfer = null;
    }
  }

  async function downloadArchive(uris) {
    contextMenu = null;
    if (!uris.length) return;
    const fileName = archiveFileName(uris);
    operating = true;
    error = "";
    transfer = { kind: "archive", name: fileName, sent: 0, total: 0 };
    try {
      if (typeof window.dbxPlugin?.stream !== "function") throw new Error(text.downloadUnavailable);
      const opened = await window.dbxPlugin.stream("filesystem/archive/open", { uris, connectionId: connectionId(), providerId }, { timeoutMs: 120000 });
      transfer.total = opened.metadata?.size || 0;
      const bytes = await readStream(opened.stream.getReader(), (sent) => { transfer.sent = sent; });
      if (opened.metadata?.truncated) throw new Error(text.archiveTooLarge);
      await saveBytes(fileName, "application/zip", bytes);
      checkedUris = [];
    } catch (cause) {
      error = cause?.message || `${text.operationFailed}: ${String(cause)}`;
    } finally {
      operating = false;
      transfer = null;
    }
  }

  function startTreeResize(event) {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = treeWidth;
    const update = (move) => {
      treeWidth = Math.round(Math.min(420, Math.max(140, startWidth + (move.clientX - startX))));
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

  function startResize(event) {
    event.preventDefault();
    // The list column shares only the space the tree has not claimed, so the
    // drag delta must map onto that remainder — anchoring to the body width
    // makes the splitter teleport when the tree panel is visible.
    const startX = event.clientX;
    const startPercent = leftWidth;
    const update = (move) => {
      const free = Math.max(1, (document.body.clientWidth || 1) - (treeVisible ? treeWidth + 6 : 0) - 6);
      leftWidth = Math.min(70, Math.max(24, startPercent + ((move.clientX - startX) / free) * 100));
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
    // Dark theming arrives as host CSS variables, not a color-scheme switch;
    // mirror the appearance so system colors (Canvas/CanvasText, form
    // controls, scrollbars) follow the theme too.
    const applyColorScheme = (theme) => {
      document.documentElement.style.colorScheme = theme?.appearance === "dark" ? "dark" : "light";
    };
    applyColorScheme(window.dbxPlugin?.theme);
    document.addEventListener("dbx-plugin-env", (event) => applyColorScheme(event.detail?.theme));
    return unsubscribe;
  });

  onDestroy(releasePreviewUrl);
</script>

<svelte:window onclick={closeContextMenu} oncontextmenu={(event) => event.preventDefault()} onkeydown={(event) => event.key === "Escape" && closeContextMenu()} />

<svelte:head><title>S3</title></svelte:head>

<main>
  <div class="toolbar">
    <div class="path-group">
      <Button variant="outline" size="icon-sm" aria-pressed={treeVisible} aria-label={text.folderTree} title={text.folderTree} onclick={() => (treeVisible = !treeVisible)}><ListTree size={14} /></Button>
      <Button variant="outline" size="icon-sm" aria-label={text.up} title={text.up} disabled={!parentUri() || loading} onclick={() => load(parentUri())}><ArrowUp size={14} /></Button>
      {#if pathEditing}
        <input aria-label={text.path} bind:value={currentUri} onkeydown={(event) => { if (event.key === "Enter") { pathEditing = false; load(currentUri); } }} />
      {:else}
        <nav class="breadcrumbs" aria-label={text.path}>
          {#each breadcrumbs as crumb, index (crumb.uri)}
            {#if index > 0}<span class="crumb-sep" aria-hidden="true"><ChevronRight size={12} /></span>{/if}
            <button type="button" class="crumb" class:current={index === breadcrumbs.length - 1} onclick={() => { pathEditing = false; load(crumb.uri); }}>{crumb.name}</button>
          {/each}
        </nav>
      {/if}
      <Button variant="ghost" size="icon-sm" class="path-edit" aria-label={text.editPath} title={text.editPath} onclick={() => (pathEditing = !pathEditing)}><Pencil size={13} /></Button>
    </div>
    <div class="toolbar-actions">
      <Button variant="outline" size="icon-sm" aria-label={text.refresh} title={text.refresh} disabled={loading || operating} onclick={() => load(currentUri)}><RefreshCw size={14} /></Button>
      <Button variant="outline" size="sm" disabled={loading || operating || (bucketMode && currentUri === "s3:/")} onclick={createFolder}><FolderPlus size={14} />{text.newFolder}</Button>
      <Button size="sm" disabled={loading || operating || (bucketMode && currentUri === "s3:/")} onclick={beginUpload}><Upload size={14} />{text.upload}</Button>
      {#if checkedUris.length}<Button variant="outline" size="sm" disabled={loading || operating} title={text.downloadZipCount.replace("{count}", checkedUris.length)} onclick={() => downloadArchive(checkedUris)}><FileArchive size={14} />{text.downloadZipCount.replace("{count}", checkedUris.length)}</Button>{/if}
      {#if checkedUris.length}<Button variant="destructive" size="sm" disabled={loading || operating} onclick={deleteChecked}><Trash2 size={14} />{text.deleteCount.replace("{count}", checkedUris.length)}</Button>{/if}
    </div>
    <input bind:this={uploadInput} hidden type="file" multiple onchange={uploadFiles} />
  </div>
  {#if transfer}<div class="transfer" role="status" aria-live="polite"><span class="transfer-label">{(transfer.kind === "upload" ? text.uploading : text.downloading) + " " + transfer.name}{transfer.fileCount > 1 ? ` (${transfer.fileIndex}/${transfer.fileCount})` : ""}</span><div class="transfer-bar"><div class="transfer-fill" style={`width: ${transferPercent()}%`}></div></div><span class="transfer-percent">{transferPercent()}%</span></div>{/if}
  {#if error}<div class="error">{text.error}: {error}</div>{/if}
  <section class="split" style={`grid-template-columns: ${treeVisible ? `${treeWidth}px 6px minmax(220px, ${leftWidth}fr) 6px minmax(280px, ${100 - leftWidth}fr)` : `minmax(240px, ${leftWidth}%) 6px minmax(280px, 1fr)`}`}>
    {#if treeVisible}
      <div class="tree-panel"><FolderTree nodes={treeNodes} currentUri={currentUri} {text} rootLabel={text.rootLabel} onToggle={toggleTreeNode} onSelect={selectTreeNode} onLoadMore={continueTrees} /></div>
      <button class="tree-splitter" aria-label="Resize tree" onpointerdown={startTreeResize}></button>
    {/if}
    <ObjectList {entries} {selected} {checkedUris} {loading} {nextCursor} {text} onSelect={selectEntry} onOpen={openEntry} onContextMenu={openContextMenu} onLoadMore={() => load(currentUri, true)} onToggleCheck={toggleCheck} onToggleCheckAll={toggleCheckAll} />
    <button class="splitter" aria-label="Resize panels" onpointerdown={startResize}></button>
    <PreviewPane {selected} {preview} {text} onRename={renameEntry} onDelete={deleteEntry} onDownload={downloadEntry} onShare={shareEntry} onSheetChange={(value) => (preview = value)} />
  </section>
  {#if contextMenu}
    <div class="context-menu" data-dbx-context-menu role="menu" tabindex="-1" style={`left: ${contextMenu.x}px; top: ${contextMenu.y}px;`} oncontextmenu={(event) => event.preventDefault()}>
      {#if contextMenu.entry.kind === "directory" || contextMenu.entry.kind === "bucket"}<button role="menuitem" onclick={() => openEntry(contextMenu.entry)}><FolderOpen size={14} />{text.open}</button><button role="menuitem" onclick={() => downloadArchive([contextMenu.entry.uri])}><FileArchive size={14} />{text.downloadZip}</button>{/if}
      {#if contextMenu.entry.kind !== "directory" && contextMenu.entry.kind !== "bucket"}<button role="menuitem" onclick={() => downloadEntry(contextMenu.entry)}><Download size={14} />{text.download}</button><button role="menuitem" onclick={() => shareEntry(contextMenu.entry)}><Link2 size={14} />{text.share}</button>{/if}
      {#if contextMenu.entry.kind !== "bucket"}<button role="menuitem" onclick={() => renameEntry(contextMenu.entry)}><Pencil size={14} />{text.rename}</button>{/if}
      {#if contextMenu.entry.kind !== "bucket"}<button class="danger" role="menuitem" onclick={() => deleteEntry(contextMenu.entry)}><Trash2 size={14} />{text.delete}</button>{/if}
    </div>
  {/if}
  <Dialog.Root bind:open={dialogOpen} onOpenChange={handleDialogOpenChange}>
    {#if dialog}
      <Dialog.Content showCloseButton={false} class="dialog-content">
        <Dialog.Header>
          <Dialog.Title>{dialog.kind === "delete" || dialog.kind === "delete-batch" ? text.delete : dialog.kind === "rename" ? text.rename : dialog.kind === "share" ? text.share : text.newFolder}</Dialog.Title>
          {#if dialog.kind === "delete"}<Dialog.Description>{text.confirmDelete.replace("{name}", dialog.entry.name)}</Dialog.Description>
          {:else if dialog.kind === "delete-batch"}<Dialog.Description>{text.confirmDeleteCount.replace("{count}", dialog.count)}</Dialog.Description>
          {:else if dialog.kind === "share"}<Dialog.Description>{dialog.entry.name}</Dialog.Description>{/if}
        </Dialog.Header>
        {#if dialog.kind === "share"}
          <label class="dialog-field">{text.shareExpires}
            <select value={dialog.expires} onchange={(event) => fetchShareUrl(dialog.entry, Number(event.currentTarget.value))}>
              {#each shareExpiryOptions as option (option.value)}<option value={option.value}>{option.label}</option>{/each}
            </select>
          </label>
          <div class="share-url">
            <input bind:this={shareUrlInput} readonly spellcheck="false" aria-label={text.share} value={dialog.loading ? text.loading : dialog.url} onclick={(event) => event.currentTarget.select()} onkeydown={(event) => event.key === "Enter" && copyShareUrl()} />
            {#if dialog.shareError}<div class="share-error">{dialog.shareError}</div>{/if}
            {#if shareCopyBlocked && dialog.url}<div class="share-hint">{text.copyBlocked}</div>{/if}
          </div>
        {:else if dialog.kind !== "delete" && dialog.kind !== "delete-batch"}
          <label class="dialog-field">{dialog.kind === "rename" ? text.newName : text.folderName}<input bind:value={dialog.value} onkeydown={(event) => event.key === "Enter" && confirmDialog()} /></label>
        {/if}
        <Dialog.Footer class="dialog-actions">
          <Button variant="outline" onclick={cancelDialog}>{text.cancel}</Button>
          {#if dialog.kind === "share"}
            <Button disabled={!dialog.url} onclick={copyShareUrl}>{shareCopied ? text.copied : text.copy}</Button>
          {:else}
            <Button variant={dialog.kind === "delete" || dialog.kind === "delete-batch" ? "destructive" : "default"} onclick={confirmDialog}>{dialog.kind === "delete" || dialog.kind === "delete-batch" ? text.delete : text.confirm}</Button>
          {/if}
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
  .breadcrumbs { display: flex; flex: 1; align-items: center; min-width: 0; overflow-x: auto; scrollbar-width: none; white-space: nowrap; }
  .breadcrumbs::-webkit-scrollbar { display: none; }
  .crumb { flex: 0 0 auto; max-width: 220px; padding: 4px 7px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); border: 0; border-radius: 5px; background: transparent; font: 12px ui-monospace, monospace; cursor: pointer; }
  .crumb:hover { color: var(--color-foreground, CanvasText); background: color-mix(in srgb, CanvasText 6%, transparent); }
  .crumb.current { color: var(--color-foreground, CanvasText); font-weight: 600; background: color-mix(in srgb, CanvasText 5%, transparent); }
  .crumb-sep { display: grid; flex: 0 0 auto; place-items: center; color: color-mix(in srgb, CanvasText 30%, transparent); }
  .path-edit { flex: 0 0 auto; }
  .tree-panel { min-height: 0; min-width: 0; overflow: hidden; background: var(--color-background, Canvas); }
  .tree-splitter { position: relative; width: 6px; height: 100%; padding: 0; border: 0; border-radius: 0; background: transparent; cursor: col-resize; }
  .tree-splitter::after { content: ""; position: absolute; top: 0; bottom: 0; left: calc(50% - 0.5px); width: 1px; background: var(--color-border, color-mix(in srgb, CanvasText 11%, transparent)); }
  .tree-splitter:hover { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 22%, transparent); }
  .tree-splitter:hover::after { background: var(--color-primary, #6d5dfc); }
  .error { margin: 8px 0; padding: 10px; border: 1px solid #d44a4a66; border-radius: 8px; color: #d44a4a; font-size: 12px; }
  .transfer { display: flex; align-items: center; gap: 10px; margin: 8px 0; padding: 8px 12px; border: 1px solid var(--color-border, color-mix(in srgb, CanvasText 12%, transparent)); border-radius: 8px; font-size: 12px; }
  .transfer-label { min-width: 0; max-width: 46%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .transfer-bar { flex: 1; height: 6px; overflow: hidden; border-radius: 3px; background: color-mix(in srgb, CanvasText 10%, transparent); }
  .transfer-fill { height: 100%; border-radius: 3px; background: var(--color-primary, #6d5dfc); transition: width 150ms ease; }
  .transfer-percent { flex: 0 0 auto; min-width: 34px; text-align: right; color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); font-variant-numeric: tabular-nums; }
  .split { display: grid; min-height: 0; flex: 1; overflow: hidden; border: 1px solid color-mix(in srgb, CanvasText 12%, transparent); border-radius: 6px; box-shadow: 0 1px 3px color-mix(in srgb, CanvasText 7%, transparent); }
  .splitter { position: relative; width: 6px; height: 100%; padding: 0; border-radius: 0; background: transparent; cursor: col-resize; }
  .splitter::after { content: ""; position: absolute; top: 0; bottom: 0; left: calc(50% - 0.5px); width: 1px; background: var(--color-border, color-mix(in srgb, CanvasText 11%, transparent)); }
  .splitter:hover { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 22%, transparent); }
  .splitter:hover::after { background: var(--color-primary, #6d5dfc); }
  :global(html), :global(body), :global(#app) { height: 100%; overflow: hidden; }
  :global(body) { min-width: 0; color: var(--color-foreground, CanvasText); background: var(--color-background, Canvas); }
  main { height: 100%; min-height: 0; gap: 8px; padding: 10px 12px; background: var(--color-background, Canvas); }
  .toolbar { min-height: 32px; padding: 0; border: 0; background: transparent; }
  .path-group { border-color: var(--color-border, color-mix(in srgb, CanvasText 10%, transparent)); background: var(--color-background, Canvas); }
  .toolbar input { border-color: var(--color-border, color-mix(in srgb, CanvasText 14%, transparent)); background: var(--color-background, Canvas); }
  .split { border-color: var(--color-border, color-mix(in srgb, CanvasText 12%, transparent)); border-radius: 6px; background: var(--color-background, Canvas); }
  :global(.dialog-content) { width: min(360px, calc(100% - 32px)); }
  .dialog-field { display: grid; gap: 6px; color: var(--color-foreground, CanvasText); font-size: 12px; }
  .dialog-field input, .dialog-field select, .share-url input { width: 100%; padding: 8px 10px; color: var(--color-foreground, CanvasText); border: 1px solid var(--color-border, color-mix(in srgb, CanvasText 18%, transparent)); border-radius: var(--radius-md, 6px); outline: none; background: var(--color-muted, color-mix(in srgb, CanvasText 5%, transparent)); font: inherit; }
  .dialog-field select { appearance: auto; cursor: pointer; }
  .dialog-field input:focus, .dialog-field select:focus, .share-url input:focus { border-color: var(--color-primary, #6d5dfc); }
  .share-url { display: grid; gap: 6px; margin-top: 12px; }
  .share-url input { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; cursor: text; font: 11px/1.4 ui-monospace, monospace; }
  .share-error { color: var(--color-destructive, #dc2626); font-size: 12px; }
  .share-hint { color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); font-size: 12px; }
  :global(.dialog-actions) { display: flex; justify-content: flex-end; gap: 8px; margin-top: 18px; }
  .context-menu { position: fixed; z-index: 9999; min-width: 160px; width: max-content; max-width: calc(100vw - 16px); padding: 4px; overflow-y: auto; border: 1px solid color-mix(in srgb, var(--color-foreground, CanvasText) 10%, transparent); border-radius: 6px; background: var(--color-popover, var(--color-background, Canvas)); color: var(--color-popover-foreground, var(--color-foreground, CanvasText)); box-shadow: 0 12px 32px color-mix(in srgb, CanvasText 18%, transparent); }
  .context-menu button { display: flex; align-items: center; gap: 8px; width: 100%; min-height: 24px; padding: 4px 8px; color: inherit; border: 0; border-radius: 6px; background: transparent; text-align: left; font-size: 13px; line-height: 16px; cursor: default; }
  .context-menu button:hover, .context-menu button:focus-visible { color: var(--color-accent-foreground, var(--color-foreground, CanvasText)); background: var(--color-accent, var(--color-muted, color-mix(in srgb, CanvasText 7%, transparent))); outline: none; }
  .context-menu button.danger { color: var(--color-destructive, #dc2626); }
  .context-menu button.danger:hover, .context-menu button.danger:focus-visible { color: var(--color-destructive, #dc2626); background: color-mix(in srgb, var(--color-destructive, #dc2626) 10%, transparent); }
</style>
