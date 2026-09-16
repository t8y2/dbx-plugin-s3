<script>
  import FileIcon from "./FileIcon.svelte";
  import { ChevronRight } from "@lucide/svelte";

  let { nodes = {}, currentUri = "", text, rootLabel = "S3", onToggle, onSelect, onLoadMore } = $props();

  const ROOT_URI = "s3:/";
  let listElement = $state(null);
  let sentinel = $state(null);

  const rows = $derived.by(() => {
    const visible = [];
    visible.push({ kind: "root", uri: ROOT_URI, name: rootLabel, depth: 0 });
    const walk = (uri, depth) => {
      const node = nodes[uri];
      if (!node || !node.open) return;
      for (const entry of node.children || []) {
        visible.push({ kind: entry.kind, uri: entry.uri, name: entry.name, depth: depth + 1 });
        walk(entry.uri, depth + 1);
      }
    };
    walk(ROOT_URI, 0);
    return visible;
  });

  const rootNode = $derived(nodes[ROOT_URI]);
  const isEmpty = $derived(rootNode?.loaded && !rootNode?.loading && !(rootNode?.children || []).length);
  // Continuation batches (not first loads) surface a spinner at the bottom.
  const continuing = $derived.by(() => {
    let active = false;
    const walk = (uri) => {
      const node = nodes[uri];
      if (!node || !node.open) return;
      if (node.loading && ((node.children || []).length || node.nextCursor)) active = true;
      for (const entry of node.children || []) walk(entry.uri);
    };
    walk(ROOT_URI);
    return active;
  });
  const hasMore = $derived.by(() => {
    let more = false;
    const walk = (uri) => {
      const node = nodes[uri];
      if (!node || !node.open) return;
      if (node.nextCursor && !node.loading) more = true;
      for (const entry of node.children || []) walk(entry.uri);
    };
    walk(ROOT_URI);
    return more;
  });

  $effect(() => {
    if (!sentinel || !listElement) return;
    // Scrolling the tree to the bottom pulls the next batch of folders, the
    // same lazy pattern the object list uses; one batch per bottom-reach,
    // re-armed once the bottom leaves the viewport again.
    let armed = true;
    const observer = new IntersectionObserver(([entry]) => {
      if (!entry.isIntersecting) {
        armed = true;
        return;
      }
      if (armed && hasMore) {
        armed = false;
        onLoadMore?.();
      }
    }, { root: listElement, rootMargin: "0px 0px 200px 0px" });
    observer.observe(sentinel);
    return () => observer.disconnect();
  });
</script>

<div class="folder-tree" bind:this={listElement} role="tree" aria-label={text.folderTree}>
  {#each rows as row (row.uri + row.depth)}
    <div class="tree-row" class:selected={currentUri === row.uri} class:ancestor={row.kind === "root" || currentUri.startsWith(row.uri === ROOT_URI ? "s3:" : row.uri)} role="treeitem" aria-selected={currentUri === row.uri} aria-expanded={row.kind === "root" ? rootNode?.open : nodes[row.uri]?.open} style={`padding-left: ${row.depth * 12 + 6}px`}>
      <button type="button" class="tree-toggle" aria-label={nodes[row.uri]?.open ? text.collapseFolder : text.expandFolder} onclick={() => onToggle?.(row.uri)}>
        {#if nodes[row.uri]?.loading && nodes[row.uri]?.open}<span class="spinner" aria-hidden="true"></span>
        {:else}<ChevronRight size={12} class={nodes[row.uri]?.open ? "open" : ""} />{/if}
      </button>
      <button type="button" class="tree-label" onclick={() => onSelect?.(row.uri)} ondblclick={() => onToggle?.(row.uri)} title={row.name}>
        {#if row.kind === "root" || row.kind === "bucket"}<span class="tree-icon database" aria-hidden="true"><svg viewBox="0 0 20 20"><ellipse cx="10" cy="5.5" rx="6.5" ry="2.5" /><path d="M3.5 5.5v9c0 1.4 2.9 2.5 6.5 2.5s6.5-1.1 6.5-2.5v-9" /><path d="M3.5 10c0 1.4 2.9 2.5 6.5 2.5s6.5-1.1 6.5-2.5" /></svg></span>
        {:else}<FileIcon entry={{ kind: "directory", name: row.name }} />{/if}
        <span class="tree-name">{row.name}</span>
      </button>
    </div>
  {/each}
  {#if isEmpty}<div class="tree-empty">{text.noFolders}</div>{/if}
  {#if continuing}<div class="tree-busy" role="status"><span class="spinner" aria-hidden="true"></span></div>{/if}
  <div bind:this={sentinel} class="tree-sentinel" aria-hidden="true"></div>
</div>

<style>
  .folder-tree { display: flex; flex-direction: column; min-height: 0; height: 100%; overflow: auto; padding: 6px 4px; font-size: 12px; background: var(--color-background, Canvas); }
  .tree-row { display: flex; align-items: center; gap: 2px; min-height: 26px; border-radius: 5px; color: inherit; }
  .tree-row:not(.more):hover { background: color-mix(in srgb, var(--color-foreground, CanvasText) 6%, transparent); }
  .tree-row.selected { background: color-mix(in srgb, var(--color-primary, #6d5dfc) 16%, transparent); }
  .tree-row.ancestor:not(:hover):not(.selected) .tree-name { color: var(--color-foreground, CanvasText); }
  .tree-toggle { display: grid; flex: 0 0 auto; place-items: center; width: 18px; height: 20px; padding: 0; border: 0; background: transparent; color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); cursor: pointer; }
  .tree-toggle:hover { color: var(--color-foreground, CanvasText); }
  .tree-toggle :global(svg) { transition: transform 120ms ease; }
  .tree-toggle :global(svg.open) { transform: rotate(90deg); }
  .tree-label { display: flex; flex: 1; align-items: center; gap: 6px; min-width: 0; padding: 3px 6px 3px 0; border: 0; background: transparent; color: var(--color-foreground, CanvasText); text-align: left; font: inherit; cursor: pointer; }
  .tree-row.selected .tree-label { color: var(--color-foreground, CanvasText); font-weight: 600; }
  .tree-name { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tree-icon { display: inline-grid; width: 16px; height: 16px; flex: 0 0 16px; place-items: center; color: #3b82f6; }
  .tree-icon svg { width: 15px; height: 15px; fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 1.5; }
  .tree-empty { padding: 10px 12px; color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); font-size: 12px; }
  .tree-busy { display: flex; align-items: center; justify-content: center; padding: 8px 0; color: var(--color-muted-foreground, color-mix(in srgb, CanvasText 55%, transparent)); }
  .tree-sentinel { height: 1px; min-height: 1px; flex: 0 0 auto; }
  .spinner { width: 10px; height: 10px; flex: 0 0 10px; border: 1.5px solid color-mix(in srgb, currentColor 25%, transparent); border-top-color: currentColor; border-radius: 50%; animation: spin .7s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
</style>
