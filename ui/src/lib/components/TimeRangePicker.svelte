<script lang="ts">
	interface Props {
		selected: string;
		onchange: (range: string) => void;
		autoRefresh?: boolean;
		onRefreshToggle?: (enabled: boolean) => void;
		onManualRefresh?: () => void;
	}

	let { selected, onchange, autoRefresh = true, onRefreshToggle, onManualRefresh }: Props = $props();

	const ranges = [
		{ label: '5m', value: '5m' },
		{ label: '15m', value: '15m' },
		{ label: '1h', value: '1h' },
		{ label: '6h', value: '6h' },
		{ label: '24h', value: '24h' },
		{ label: '7d', value: '168h' },
		{ label: '30d', value: '720h' },
	];
</script>

<div class="flex items-center gap-2">
	<!-- Range buttons (scrollable on small screens) -->
	<div class="flex items-center gap-0.5 bg-bg-primary rounded-lg p-0.5 overflow-x-auto no-scrollbar">
		{#each ranges as range}
			<button
				class="px-2 sm:px-3 py-1 text-xs font-medium rounded-md transition-all duration-150
					{selected === range.value
						? 'bg-accent text-bg-primary'
						: 'text-text-muted hover:text-text-secondary hover:bg-bg-secondary'}"
				onclick={() => onchange(range.value)}
			>
				{range.label}
			</button>
		{/each}
	</div>

	<!-- Refresh controls -->
	<div class="flex items-center gap-1">
		{#if onManualRefresh}
			<button
				onclick={onManualRefresh}
				class="p-1.5 rounded-lg text-text-muted hover:text-accent hover:bg-bg-primary transition-colors"
				title="Refresh now"
			>
				<svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
					<polyline points="23 4 23 10 17 10"/>
					<path d="M20.49 15a9 9 0 11-2.12-9.36L23 10"/>
				</svg>
			</button>
		{/if}

		{#if onRefreshToggle}
			<button
				onclick={() => onRefreshToggle?.(!autoRefresh)}
				class="p-1.5 rounded-lg transition-colors {autoRefresh
					? 'text-green hover:bg-bg-primary'
					: 'text-text-muted hover:text-text-secondary hover:bg-bg-primary'}"
				title={autoRefresh ? 'Auto-refresh: ON' : 'Auto-refresh: OFF'}
			>
				<svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
					{#if autoRefresh}
						<polygon points="5 3 19 12 5 21 5 3"/>
					{:else}
						<rect x="6" y="4" width="4" height="16"/>
						<rect x="14" y="4" width="4" height="16"/>
					{/if}
				</svg>
			</button>
		{/if}
	</div>
</div>
