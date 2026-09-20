export function formatObjectSize(size, locale = "en") {
  if (!Number.isFinite(size) || size < 0) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"];
  const unit = size > 0 ? Math.max(0, Math.min(Math.floor(Math.log(size) / Math.log(1024)), units.length - 1)) : 0;
  const value = (size / 1024 ** unit).toLocaleString(locale, { maximumFractionDigits: unit ? 1 : 0 });
  return `${value} ${units[unit]}`;
}

export function formatObjectModified(value, locale = "en") {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return date.toLocaleString(locale, { year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hour12: false });
}

// Explorer/Finder convention: directories and buckets group before files no
// matter their names, so listings keep folders on top across appended pages.
export function sortEntriesDirectoryFirst(entries) {
  const weight = (entry) => (entry.kind === "directory" || entry.kind === "bucket" ? 1 : 0);
  return [...entries].sort((left, right) => {
    const difference = weight(right) - weight(left);
    if (difference !== 0) return difference;
    return left.name.localeCompare(right.name, undefined, { numeric: true, sensitivity: "base" });
  });
}

// Pretty-print JSON previews; a parse failure (truncated or streamed text)
// keeps the raw bytes visible instead of blanking the preview.
export function prettyJsonText(value) {
  try {
    return JSON.stringify(JSON.parse(value), null, 2);
  } catch {
    return value;
  }
}
