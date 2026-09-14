<script>
  import { marked } from "marked";
  import { Pencil, Trash2 } from "@lucide/svelte";
  import { Button } from "../lib/components/ui/button/index.js";

  let { selected = null, preview, text, onSheetChange, onRename, onDelete } = $props();
  const allowedMarkdownTags = new Set(["A", "BLOCKQUOTE", "BR", "CODE", "EM", "H1", "H2", "H3", "H4", "H5", "H6", "HR", "LI", "OL", "P", "PRE", "STRONG", "TABLE", "TBODY", "TD", "TH", "THEAD", "TR", "UL"]);
  const displayCell = (value) => value === null || value === undefined ? "" : String(value);

  function safeMarkdown(value) {
    const documentFragment = new DOMParser().parseFromString(marked.parse(value), "text/html");
    for (const element of [...documentFragment.body.querySelectorAll("*")]) {
      if (!allowedMarkdownTags.has(element.tagName)) {
        element.replaceWith(documentFragment.createTextNode(element.textContent || ""));
        continue;
      }
      for (const attribute of [...element.attributes]) {
        if (element.tagName === "A" && attribute.name === "href" && /^(?:https?:|mailto:|#|\/(?!\/)|\.\.?\/)/i.test(attribute.value)) continue;
        element.removeAttribute(attribute.name);
      }
    }
    return documentFragment.body.innerHTML;
  }
</script>

<div class="preview-panel">
  {#if !selected}<div class="empty">{text.noSelection}</div>
  {:else}
    <div class="preview-title">
      <div class="preview-heading"><strong>{selected.name}</strong><small>{preview.type}{preview.truncated ? ` · ${text.truncated}` : ""}</small></div>
      <div class="preview-actions">
        <Button variant="ghost" size="sm" class="preview-action" onclick={() => onRename?.(selected)}><Pencil size={13} />{text.rename}</Button>
        <Button variant="destructive" size="sm" class="preview-action" onclick={() => onDelete?.(selected)}><Trash2 size={13} />{text.delete}</Button>
      </div>
    </div>
    <div class="preview-content">
      {#if preview.kind === "loading"}<div class="empty loading-state"><span class="spinner" aria-hidden="true"></span>{text.loading}</div>
      {:else if preview.kind === "error"}<div class="error">{text.error}: {preview.value}</div>
      {:else if preview.kind === "empty"}<div class="empty">{text.folder}</div>
      {:else if preview.kind === "image"}<img src={preview.value} alt={selected.name} />
      {:else if preview.kind === "video"}<video src={preview.value} controls><track kind="captions" /></video>
      {:else if preview.kind === "audio"}<audio src={preview.value} controls></audio>
      {:else if preview.kind === "markdown"}<article class="markdown">{@html safeMarkdown(preview.value)}</article>
      {:else if preview.kind === "spreadsheet"}
        {#if preview.sheets.length}
          <div class="sheet-tabs" role="tablist" aria-label={text.sheet}>
            {#each preview.sheets as sheet, sheetIndex}
              <button class:active={preview.sheetIndex === sheetIndex} class="sheet-tab" role="tab" aria-selected={preview.sheetIndex === sheetIndex} onclick={() => onSheetChange?.({ ...preview, sheetIndex })}>{sheet.name}</button>
            {/each}
          </div>
          {#each preview.sheets as sheet, sheetIndex}
            {#if preview.sheetIndex === sheetIndex}
              <div class="sheet-scroll">
                <table class="sheet-table">
                  <tbody>
                    {#each sheet.rows as row, rowIndex}
                      <tr>
                        {#each row as cell, columnIndex}
                          {#if rowIndex === 0}<th>{displayCell(cell)}</th>{:else}<td>{displayCell(cell)}</td>{/if}
                        {/each}
                      </tr>
                    {/each}
                  </tbody>
                </table>
              </div>
            {/if}
          {/each}
        {:else}<div class="empty">{text.noSheets}</div>{/if}
      {:else if preview.kind === "text"}<pre>{preview.value}</pre>
      {:else}<div class="empty">{preview.message || text.binary}</div>{/if}
    </div>
  {/if}
</div>

<style>
  .preview-panel { display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; }
  .error { margin: 8px 0; padding: 10px; border: 1px solid #d44a4a66; border-radius: 8px; color: #d44a4a; font-size: 12px; }
  .preview-title { display: flex; align-items: center; justify-content: space-between; gap: 12px; height: 38px; min-height: 38px; padding: 0 10px 0 14px; border-bottom: 1px solid color-mix(in srgb, CanvasText 12%, transparent); background: color-mix(in srgb, CanvasText 2%, transparent); }
  .preview-heading { display: flex; min-width: 0; align-items: center; gap: 10px; }
  .preview-title strong { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 13px; font-weight: 600; }
  .preview-title small { color: color-mix(in srgb, CanvasText 55%, transparent); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .preview-actions { display: flex; flex: 0 0 auto; gap: 4px; }
  .preview-content { min-height: 0; flex: 1; overflow: auto; padding: 16px; }
  .preview-content img { display: block; max-width: 100%; max-height: 100%; margin: auto; object-fit: contain; }
  .preview-content video { display: block; width: 100%; max-height: 75%; margin: auto; }
  .preview-content audio { width: 100%; margin-top: 24px; }
  .preview-content pre { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: 12px/1.6 ui-monospace, monospace; }
  .markdown { line-height: 1.6; }.markdown :global(pre) { overflow: auto; padding: 10px; border-radius: 8px; background: color-mix(in srgb, CanvasText 8%, transparent); }
  .sheet-tabs { display: flex; gap: 4px; overflow-x: auto; margin-bottom: 10px; }.sheet-tab { flex: 0 0 auto; padding: 6px 9px; color: inherit; border: 1px solid color-mix(in srgb, CanvasText 14%, transparent); background: color-mix(in srgb, CanvasText 4%, transparent); font: inherit; font-size: 12px; cursor: pointer; }.sheet-tab.active { color: white; border-color: #6d5dfc; background: #6d5dfc; }
  .sheet-scroll { overflow: auto; border: 1px solid color-mix(in srgb, CanvasText 12%, transparent); border-radius: 8px; }.sheet-table { min-width: 100%; border-collapse: collapse; font-size: 12px; white-space: nowrap; }.sheet-table th, .sheet-table td { padding: 6px 9px; border-right: 1px solid color-mix(in srgb, CanvasText 10%, transparent); border-bottom: 1px solid color-mix(in srgb, CanvasText 10%, transparent); text-align: left; }.sheet-table th { position: sticky; top: 0; color: inherit; background: color-mix(in srgb, CanvasText 8%, Canvas); }
  .empty { display: grid; min-height: 100%; place-items: center; padding: 24px; color: color-mix(in srgb, CanvasText 55%, transparent); font-size: 13px; text-align: center; }.loading-state { display: flex; flex-direction: row; align-items: center; justify-content: center; gap: 8px; }.spinner { width: 14px; height: 14px; flex: 0 0 14px; border: 2px solid color-mix(in srgb, currentColor 24%, transparent); border-top-color: currentColor; border-radius: 50%; animation: spin .7s linear infinite; } @keyframes spin { to { transform: rotate(360deg); } }
  .empty { color: var(--color-muted-foreground, var(--color-foreground, CanvasText)); }.preview-title, .sheet-scroll { border-color: var(--color-border, color-mix(in srgb, CanvasText 12%, transparent)); }.preview-title small { color: var(--color-muted-foreground, var(--color-foreground, CanvasText)); }.sheet-tab { border-color: var(--color-border, color-mix(in srgb, CanvasText 14%, transparent)); background: color-mix(in srgb, var(--color-foreground, CanvasText) 4%, transparent); }.sheet-tab.active { border-color: var(--color-primary, #3370ff); background: var(--color-primary, #3370ff); }.sheet-table th, .sheet-table td { border-color: var(--color-border, color-mix(in srgb, CanvasText 10%, transparent)); }.sheet-table th { background: var(--color-muted, color-mix(in srgb, CanvasText 8%, Canvas)); }
</style>
