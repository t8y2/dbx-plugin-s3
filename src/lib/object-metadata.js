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
