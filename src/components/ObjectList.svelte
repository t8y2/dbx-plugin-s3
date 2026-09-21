<script>
  import FileIcon from "./FileIcon.svelte";
  import { formatObjectModified, formatObjectSize } from "../lib/object-metadata.js";

  let { entries = [], selected = null, checkedUris = [], loading = false, nextCursor = "", focusTarget = null, text, onSelect, onOpen, onContextMenu, onBackgroundContextMenu, onLoadMore, onToggleCheck, onToggleCheckAll } = $props();
  let listElement = $state(null);
  let loadMoreSentinel = $state(null);
  let sentinelVisible = false;

  const allChecked = $derived(entries.length > 0 && entries.every((entry) => checkedUris.includes(entry.uri)));
  const someChecked = $derived(!allChecked && entries.some((entry) => checkedUris.includes(entry.uri)));
  // Native checkboxes lose their checkmark when the click default is
  // suppressed while Svelte owns the checked state, so draw our own.
  const headerCheckState = $derived(allChecked ? "true" : someChecked ? "mixed" : "false");

  function triggerLoadMore() {
    if (!nextCursor || loading || sentinelVisible) return;
    sentinelVisible = true;
    onLoadMore?.();
  }

  function handleListScroll(event) {
    const element = event.currentTarget;
    if (nextCursor && !loading && !sentinelVisible && element.scrollTop + element.clientHeight >= element.scrollHeight - 120) triggerLoadMore();
  }

  function handleContextMenu(event, entry) {
    event.preventDefault();
    event.stopPropagation();
    onSelect?.(entry);
    onContextMenu?.(event, entry);
  }

  $effect(() => {
    if (!loadMoreSentinel) return;
    sentinelVisible = false;
    const observer = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) {
        sentinelVisible = false;
        return;
      }
      triggerLoadMore();
    }, { root: listElement, rootMargin: "0px 0px 120px 0px" });
    observer.observe(loadMoreSentinel);
    return () => observer.disconnect();
  });

  // Path navigation asks for a specific row to be brought into view; compute
  // the delta by hand because old WKWebView ignores scrollIntoView options.
  $effect(() => {
    if (!focusTarget?.uri || !listElement) return;
    const row = listElement.querySelector(`[data-uri="${focusTarget.uri.replace(/"/g, '\\"')}"]`);
    if (!row) return;
    const listRect = listElement.getBoundingClientRect();
    const rowRect = row.getBoundingClientRect();
    if (rowRect.top < listRect.top) listElement.scrollTop += rowRect.top - listRect.top - 8;
    else if (rowRect.bottom > listRect.bottom) listElement.scrollTop += rowRect.bottom - listRect.bottom + 8;
  });
</script>

<div class="list-panel" role="group" bind:this={listElement} onscroll={handleListScroll} oncontextmenu={(event) => { event.preventDefault(); if (!event.target.closest(".entry-row")) onBackgroundContextMenu?.(event); }}>
  {#if loading && !entries.length}<div class="empty loading-state"><span class="spinner" aria-hidden="true"></span>{text.loading}</div>
  {:else if !entries.length}<div class="empty">{text.empty}</div>
  {:else}
    <div class="list-header">
      <button type="button" class="list-check" role="checkbox" aria-checked={headerCheckState} aria-label={text.file} disabled={loading} onclick={() => onToggleCheckAll?.()}>
        {#if headerCheckState === "true"}<svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 6.3 5 8.8 9.5 3.6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>
        {:else if headerCheckState === "mixed"}<span class="check-mixed" aria-hidden="true"></span>{/if}
      </button>
      <div class="entry-columns"><span>{text.file}</span><span>{text.type}</span><span class="entry-size">{text.size}</span><span title={text.modifiedHint}>{text.modified}</span></div>
    </div>
    <div class="entries">
      {#each entries as entry (entry.uri)}
        {@const isChecked = checkedUris.includes(entry.uri)}
        <div class="entry-row" data-uri={entry.uri} class:checked={isChecked} class:selected={selected?.uri === entry.uri}>
          <button type="button" class="list-check" role="checkbox" aria-checked={isChecked ? "true" : "false"} aria-label={entry.name} onclick={() => onToggleCheck?.(entry)}>
            {#if isChecked}<svg viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 6.3 5 8.8 9.5 3.6" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" /></svg>{/if}
          </button>
          <button class="entry entry-columns" onclick={() => onSelect?.(entry)} ondblclick={() => onOpen?.(entry)} oncontextmenu={(event) => handleContextMenu(event, entry)}>
            <span class="entry-name" title={entry.name}><FileIcon {entry} /><span>{entry.name}</span></span>
            <span class="entry-meta">{entry.kind === "directory" || entry.kind === "bucket" ? text.folder : entry.contentType || ""}</span>
            <span class="entry-meta entry-size" title={entry.kind === "file" && Number.isFinite(entry.size) ? `${entry.size.toLocaleString(text.locale)} B` : ""}>{formatObjectSize(entry.kind === "file" ? entry.size : undefined, text.locale)}</span>
            <span class="entry-meta" title={entry.modifiedAt || ""}>{formatObjectModified(entry.modifiedAt, text.locale)}</span>
          </button>
        </div>
      {/each}
      {#if nextCursor}<div bind:this={loadMoreSentinel} class:loading={loading} class="load-more-status" role="status" aria-live="polite">{#if loading}<span class="spinner" aria-hidden="true"></span>{text.loading}{/if}</div>{/if}
    </div>
  {/if}
</div>

<style>
  .list-panel { min-width: 0; min-height: 0; overflow: auto; display: flex; flex-direction: column; }
  .list-header { display: flex; align-items: center; justify-content: space-between; gap: 8px; height: 38px; min-height: 38px; padding: 0 12px 0 10px; color: color-mix(in srgb, CanvasText 55%, transparent); border-bottom: 1px solid color-mix(in srgb, CanvasText 12%, transparent); font-size: 10px; font-weight: 600; letter-spacing: .04em; text-transform: uppercase; }
  .list-header, .entries { min-width: 510px; }
  .list-header { position: sticky; top: 0; z-index: 1; background: var(--color-background, Canvas); }
  .entry-columns { display: grid; grid-template-columns: minmax(120px, 1fr) 76px 72px 146px; align-items: center; gap: 10px; width: 100%; min-width: 0; }
  .entry-size { text-align: right; font-variant-numeric: tabular-nums; }
  .list-check { display: grid; flex: 0 0 auto; place-items: center; width: 14px; height: 14px; padding: 0; color: transparent; border: 1.5px solid color-mix(in srgb, CanvasText 35%, transparent); border-radius: 3.5px; background: transparent; cursor: pointer; transition: background-color 120ms ease, border-color 120ms ease; }
  .list-check:hover { border-color: var(--color-primary, #6d5dfc); }
  .list-check:focus-visible { outline: 2px solid color-mix(in srgb, var(--color-primary, #6d5dfc) 55%, transparent); outline-offset: 1px; }
  .list-check:disabled { cursor: default; opacity: .45; }
  .list-check svg { width: 11px; height: 11px; }
  .list-check[aria-checked="true"] { color: var(--color-primary-foreground, #fff); border-color: var(--color-primary, #6d5dfc); background: var(--color-primary, #6d5dfc); }
  .list-check[aria-checked="mixed"] { border-color: var(--color-primary, #6d5dfc); background: color-mix(in srgb, var(--color-primary, #6d5dfc) 28%, transparent); }
  .check-mixed { width: 7px; height: 1.5px; border-radius: 1px; background: var(--color-primary, #6d5dfc); }
  .entries { flex: 1 0 auto; }
  .entry-row { display: flex; align-items: center; gap: 8px; padding: 0 12px 0 10px; border-bottom: 1px solid color-mix(in srgb, CanvasText 7%, transparent); }
  .entry-row:hover { background: color-mix(in srgb, CanvasText 5%, transparent); }
  .entry-row.checked { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 7%, transparent); }
  .entry-row.selected { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 16%, transparent); }
  .entry-row.selected:hover { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 20%, transparent); }
  .entry { min-height: 32px; padding: 5px 0; color: inherit; border: 0; border-radius: 0; background: transparent; text-align: left; font: inherit; font-size: 12px; line-height: 1.2; cursor: pointer; transition: background-color 120ms ease; }
  .entry-name, .entry-meta { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .entry-name { display: flex; align-items: center; gap: 7px; }
  .entry-name > span { overflow: hidden; text-overflow: ellipsis; }
  .entry-meta { color: color-mix(in srgb, CanvasText 50%, transparent); font-size: 11px; }
  .empty { display: grid; min-height: 100%; place-items: center; padding: 24px; color: color-mix(in srgb, CanvasText 55%, transparent); font-size: 13px; text-align: center; }
  .loading-state { display: flex; flex-direction: row; align-items: center; justify-content: center; gap: 8px; }
  .load-more-status { display: flex; align-items: center; justify-content: center; height: 1px; color: color-mix(in srgb, CanvasText 55%, transparent); font-size: 11px; }
  .load-more-status.loading { height: 30px; min-height: 30px; padding: 6px; }
  .spinner { width: 14px; height: 14px; flex: 0 0 14px; border: 2px solid color-mix(in srgb, currentColor 24%, transparent); border-top-color: currentColor; border-radius: 50%; animation: spin .7s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .empty, .load-more-status { color: var(--color-muted-foreground, var(--color-foreground, CanvasText)); }
  .list-header, .entry-row { border-color: var(--color-border, color-mix(in srgb, CanvasText 12%, transparent)); }
  .list-header, .entry-meta { color: var(--color-muted-foreground, var(--color-foreground, CanvasText)); }
  .entry-row { min-height: 32px; }
</style>
