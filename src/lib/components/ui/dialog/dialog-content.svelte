<script lang="ts">
	import { Dialog as DialogPrimitive } from "bits-ui";
	import XIcon from '@lucide/svelte/icons/x';
	import { Button } from "src/lib/components/ui/button/index.js";
	import { cn, type WithoutChildrenOrChild } from "src/lib/utils.js";
	import * as Dialog from "./index.js";
	import DialogPortal from "./dialog-portal.svelte";
	import type { Snippet } from "svelte";
	import type { ComponentProps } from "svelte";

	let {
		ref = $bindable(null),
		class: className,
		portalProps,
		children,
		showCloseButton = true,
		...restProps
	}: WithoutChildrenOrChild<DialogPrimitive.ContentProps> & {
		portalProps?: WithoutChildrenOrChild<ComponentProps<typeof DialogPortal>>;
		children: Snippet;
		showCloseButton?: boolean;
	} = $props();
</script>

<DialogPortal {...portalProps}>
	<Dialog.Overlay />
	<!-- Centering uses a fixed flex shell plus a normally-flowed content box.
	     The previous scheme pinned all four edges and relied on height:
	     fit-content to keep the box content-sized — engines that do not know
	     that value (older WebKit) drop it, stretch the box to the viewport and
	     then distribute the extra height across every grid row. Inside the
	     shell the content gets its natural auto height on every engine. -->
	<div class="fixed top-0 right-0 bottom-0 left-0 z-50 flex items-center justify-center pointer-events-none p-4">
		<DialogPrimitive.Content
			bind:ref
			data-slot="dialog-content"
			class={cn(
				"bg-popover text-popover-foreground data-open:animate-in data-closed:animate-out data-closed:fade-out-0 data-open:fade-in-0 data-closed:zoom-out-95 data-open:zoom-in-95 ring-foreground/10 pointer-events-auto relative grid max-h-full gap-4 overflow-y-auto rounded-xl p-4 text-sm ring-1 duration-100 w-full max-w-none sm:max-w-none outline-none",
				className
			)}
			{...restProps}
		>
		{@render children?.()}
		{#if showCloseButton}
			<DialogPrimitive.Close data-slot="dialog-close">
				{#snippet child({ props })}
					<Button variant="ghost" class="absolute top-2 right-2" size="icon-sm" {...props}>
						<XIcon  />
						<span class="sr-only">Close</span>
					</Button>
				{/snippet}
			</DialogPrimitive.Close>
		{/if}
	</DialogPrimitive.Content>
	</div>
</DialogPortal>
