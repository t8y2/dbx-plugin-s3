<script>
  import FileIcon from "./FileIcon.svelte";

  let { entries = [], selected = null, checkedUris = [], loading = false, nextCursor = "", text, onSelect, onOpen, onContextMenu, onLoadMore, onToggleCheck, onToggleCheckAll } = $props();
  let listElement = $state(null);
  let loadMoreSentinel = $state(null);
  let sentinelVisible = false;

  const allChecked = $derived(entries.length > 0 && entries.every((entry) => checkedUris.includes(entry.uri)));

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
</script>

<div class="list-panel" role="group" oncontextmenu={(event) => event.preventDefault()}>
  {#if loading && !entries.length}<div class="empty loading-state"><span class="spinner" aria-hidden="true"></span>{text.loading}</div>
  {:else if !entries.length}<div class="empty">{text.empty}</div>
  {:else}
    <div class="list-header">
      <input class="list-check" type="checkbox" aria-label={text.file} checked={allChecked} disabled={loading} onclick={(event) => { event.preventDefault(); onToggleCheckAll?.(); }} />
      <span class="header-file">{text.file}</span><span>{text.type}</span>
    </div>
    <div class="entries" bind:this={listElement} onscroll={handleListScroll}>
      {#each entries as entry (entry.uri)}
        <div class="entry-row" class:checked={checkedUris.includes(entry.uri)}>
          <input class="list-check" type="checkbox" aria-label={entry.name} checked={checkedUris.includes(entry.uri)} onclick={(event) => { event.preventDefault(); onToggleCheck?.(entry); }} />
          <button class:selected={selected?.uri === entry.uri} class="entry" onclick={() => onSelect?.(entry)} ondblclick={() => onOpen?.(entry)} oncontextmenu={(event) => handleContextMenu(event, entry)}>
            <span class="entry-name"><FileIcon {entry} />{entry.name}</span>
            <span class="entry-meta">{entry.kind === "directory" || entry.kind === "bucket" ? text.folder : entry.contentType || ""}</span>
          </button>
        </div>
      {/each}
      {#if nextCursor}<div bind:this={loadMoreSentinel} class:loading={loading} class="load-more-status" role="status" aria-live="polite">{#if loading}<span class="spinner" aria-hidden="true"></span>{text.loading}{/if}</div>{/if}
    </div>
  {/if}
</div>

<style>
  .list-panel { min-width: 0; min-height: 0; overflow: hidden; display: flex; flex-direction: column; }
  .list-header { display: flex; align-items: center; justify-content: space-between; gap: 8px; height: 38px; min-height: 38px; padding: 0 12px 0 10px; color: color-mix(in srgb, CanvasText 55%, transparent); border-bottom: 1px solid color-mix(in srgb, CanvasText 12%, transparent); font-size: 10px; font-weight: 600; letter-spacing: .04em; text-transform: uppercase; }
  .header-file { flex: 1; }
  .list-check { flex: 0 0 auto; width: 13px; height: 13px; margin: 0; accent-color: var(--color-primary, #6d5dfc); cursor: pointer; }
  .entries { min-height: 0; flex: 1; overflow: auto; }
  .entry-row { display: flex; align-items: center; gap: 8px; padding: 0 12px 0 10px; border-bottom: 1px solid color-mix(in srgb, CanvasText 7%, transparent); }
  .entry-row:hover { background: color-mix(in srgb, CanvasText 5%, transparent); }
  .entry-row.checked { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 7%, transparent); }
  .entry { display: flex; align-items: center; width: 100%; min-height: 32px; justify-content: space-between; gap: 10px; padding: 5px 0; color: inherit; border: 0; border-radius: 0; background: transparent; text-align: left; font: inherit; font-size: 12px; line-height: 1.2; cursor: pointer; transition: background-color 120ms ease; }
  .entry.selected { box-shadow: inset 2px 0 var(--color-primary, #6d5dfc); }
  .entry-name, .entry-meta { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .entry-name { display: flex; align-items: center; gap: 7px; }
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
