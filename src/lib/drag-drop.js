// Turns a drop event's DataTransfer into a flat File list, preserving folder
// structure through webkitRelativePath exactly like the directory-picker
// input does, so uploadTarget() can treat both paths identically. Entries
// must be read from the DataTransfer synchronously — the browser detaches
// them once the drop handler yields — so the entry walk below kicks off
// before the first await in the caller's turn.

function readFileEntry(entry) {
  return new Promise((resolve, reject) => entry.file(resolve, reject));
}

function readEntriesBatch(reader) {
  return new Promise((resolve) => reader.readEntries(resolve, () => resolve([])));
}

function withRelativePath(file, relativePath) {
  if (!relativePath) return file;
  const shaped = new File([file], file.name, { type: file.type, lastModified: file.lastModified });
  Object.defineProperty(shaped, "webkitRelativePath", { value: relativePath });
  return shaped;
}

export async function collectDroppedFiles(dataTransfer) {
  const items = [...(dataTransfer?.items || [])];
  const roots = items.map((item) => (typeof item.webkitGetAsEntry === "function" ? item.webkitGetAsEntry() : null)).filter(Boolean);
  if (!roots.length) return [...(dataTransfer?.files || [])];
  const files = [];
  const walk = async (entry, parentPath) => {
    if (entry.isFile) {
      try {
        const file = await readFileEntry(entry);
        files.push(withRelativePath(file, parentPath ? `${parentPath}/${entry.name}` : ""));
      } catch {
        // Unreadable entry (permissions, vanished file): skip it and keep
        // uploading the rest rather than failing the whole drop.
      }
      return;
    }
    if (!entry.isDirectory) return;
    const path = parentPath ? `${parentPath}/${entry.name}` : entry.name;
    const reader = entry.createReader();
    let batch;
    do {
      // readEntries returns at most 100 entries per call until it yields an
      // empty batch, so large folders must be drained in a loop.
      batch = await readEntriesBatch(reader);
      for (const child of batch) await walk(child, path);
    } while (batch.length);
  };
  for (const root of roots) await walk(root, "");
  return files;
}
