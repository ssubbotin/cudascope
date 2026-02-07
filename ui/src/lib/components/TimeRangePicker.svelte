<script lang="ts">
	interface Props {
		selected: string;
		onchange: (range: string) => void;
	}

	let { selected, onchange }: Props = $props();

	const ranges = [
		{ label: '5m', value: '5m' },
		{ label: '15m', value: '15m' },
		{ label: '1h', value: '1h' },
		{ label: '6h', value: '6h' },
		{ label: '24h', value: '24h' },
	];
</script>

<div class="flex items-center gap-1 bg-bg-primary rounded-lg p-0.5">
	{#each ranges as range}
		<button
			class="px-3 py-1 text-xs font-medium rounded-md transition-all duration-150
				{selected === range.value
					? 'bg-accent text-bg-primary'
					: 'text-text-muted hover:text-text-secondary hover:bg-bg-secondary'}"
			onclick={() => onchange(range.value)}
		>
			{range.label}
		</button>
	{/each}
</div>
