<script lang="ts">
	import { Dialog as DialogPrimitive } from "bits-ui";
	import { cn } from "src/lib/utils.js";

	let {
		ref = $bindable(null),
		class: className,
		...restProps
	}: DialogPrimitive.OverlayProps = $props();
</script>

<!-- The `inset` shorthand is unavailable on older WebKit (Safari < 14.5),
     where it collapses the overlay to a 0x0 box; spell the edges out. -->
<DialogPrimitive.Overlay
	bind:ref
	data-slot="dialog-overlay"
	class={cn("data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 bg-black/40 duration-100 supports-backdrop-filter:backdrop-blur-xs fixed top-0 right-0 bottom-0 left-0 isolate z-50", className)}
	{...restProps}
/>
