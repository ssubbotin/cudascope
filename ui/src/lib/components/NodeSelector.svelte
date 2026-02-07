<script lang="ts">
	import type { Node } from '$lib/stores/metrics';

	interface Props {
		nodes: Node[];
		selected: string;
		onchange: (nodeId: string) => void;
	}

	let { nodes, selected, onchange }: Props = $props();
</script>

{#if nodes.length > 1}
	<div class="flex items-center gap-2">
		<svg class="w-3.5 h-3.5 text-text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
			<rect x="2" y="2" width="20" height="8" rx="2"/>
			<rect x="2" y="14" width="20" height="8" rx="2"/>
			<circle cx="6" cy="6" r="1" fill="currentColor"/>
			<circle cx="6" cy="18" r="1" fill="currentColor"/>
		</svg>
		<div class="flex items-center gap-0.5 bg-bg-primary rounded-lg p-0.5 overflow-x-auto no-scrollbar">
			<button
				class="px-2 sm:px-3 py-1 text-xs font-medium rounded-md transition-all duration-150
					{selected === 'all'
						? 'bg-accent text-bg-primary'
						: 'text-text-muted hover:text-text-secondary hover:bg-bg-secondary'}"
				onclick={() => onchange('all')}
			>
				All
			</button>
			{#each nodes as node}
				<button
					class="px-2 sm:px-3 py-1 text-xs font-medium rounded-md transition-all duration-150 flex items-center gap-1.5
						{selected === node.node_id
							? 'bg-accent text-bg-primary'
							: 'text-text-muted hover:text-text-secondary hover:bg-bg-secondary'}"
					onclick={() => onchange(node.node_id)}
				>
					<span class="w-1.5 h-1.5 rounded-full flex-shrink-0 {node.online ? 'bg-green' : 'bg-red'}"></span>
					{node.hostname}
					<span class="text-[10px] opacity-60">{node.gpu_count}G</span>
				</button>
			{/each}
		</div>
	</div>
{/if}
