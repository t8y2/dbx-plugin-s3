<script>
  let { entry } = $props();

  const extension = (name) => name.split(".").pop()?.toLowerCase() || "";
  const iconPaths = {
    folder: '<path d="M3 6.5A1.5 1.5 0 0 1 4.5 5h4l1.5 2h5.5A1.5 1.5 0 0 1 17 8.5v7A1.5 1.5 0 0 1 15.5 17h-11A1.5 1.5 0 0 1 3 15.5z"/>',
    image: '<rect x="3" y="4" width="14" height="12" rx="2"/><circle cx="7.5" cy="8" r="1.25"/><path d="m4.5 14 3.5-3 2.5 2 2-2 3 3"/>',
    video: '<rect x="3" y="5" width="11" height="10" rx="2"/><path d="m14 9 4-2v6l-4-2z"/>',
    audio: '<path d="M6 15.5V5l9-2v10.5"/><circle cx="4.5" cy="15.5" r="2.5"/><circle cx="13.5" cy="13.5" r="2.5"/>',
    spreadsheet: '<rect x="3" y="3" width="14" height="14" rx="2"/><path d="M3 8h14M8 3v14M13 8v9"/>',
    document: '<path d="M6 3h6l4 4v10H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M12 3v5h5M7 11h6M7 14h6"/>',
    markdown: '<path d="M3 5.5A1.5 1.5 0 0 1 4.5 4h11A1.5 1.5 0 0 1 17 5.5v7a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 3 12.5z"/><path d="m5.5 11 2-2 2 2 2.5-3 2.5 3"/>',
    archive: '<path d="M4 5h12v11H4zM3 5h14M7 8h2M7 11h2M7 14h2"/>',
    code: '<path d="m7 6-4 3 4 3M13 6l4 3-4 3M11 4l-2 10"/>',
    file: '<path d="M6 3h6l4 4v10H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M12 3v5h5"/>',
  };
  function entryKind(value) {
    if (value.kind === "directory") return "folder";
    const type = (value.contentType || "").split(";", 1)[0].toLowerCase();
    const ext = extension(value.name);
    if (type.startsWith("image/") || ["avif", "bmp", "gif", "jpeg", "jpg", "png", "webp"].includes(ext)) return "image";
    if (type.startsWith("video/") || ["m4v", "mov", "mp4", "mpeg", "webm"].includes(ext)) return "video";
    if (type.startsWith("audio/") || ["aac", "flac", "m4a", "mp3", "ogg", "wav", "weba"].includes(ext)) return "audio";
    if (type.includes("spreadsheet") || type.includes("excel") || ["csv", "xls", "xlsb", "xlsm", "xlsx"].includes(ext)) return "spreadsheet";
    if (type.includes("word") || type === "application/pdf" || ["doc", "docx", "pdf"].includes(ext)) return "document";
    if (["md", "markdown"].includes(ext)) return "markdown";
    if (["7z", "bz2", "gz", "rar", "tar", "tgz", "zip"].includes(ext)) return "archive";
    if (["c", "cpp", "css", "go", "html", "java", "js", "json", "jsx", "py", "rs", "sql", "ts", "tsx", "vue", "xml", "yaml", "yml"].includes(ext)) return "code";
    return "file";
  }

  let kind = $derived(entryKind(entry));
</script>

<span class={`entry-icon ${kind}`} aria-hidden="true">
  <svg viewBox="0 0 20 20">{@html iconPaths[kind]}</svg>
</span>

<style>
  .entry-icon { display: inline-grid; width: 16px; height: 16px; flex: 0 0 16px; place-items: center; color: #8b93a1; }
  .entry-icon.folder { color: #e7ab45; }.entry-icon.image { color: #3b82f6; }.entry-icon.video { color: #a855f7; }.entry-icon.audio { color: #f97316; }.entry-icon.spreadsheet { color: #16a34a; }.entry-icon.document { color: #ef4444; }.entry-icon.markdown { color: #64748b; }.entry-icon.archive { color: #ca8a04; }.entry-icon.code { color: #0891b2; }.entry-icon.file { color: #8b93a1; }
  svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.5; }
</style>
