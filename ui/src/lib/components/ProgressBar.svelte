<script lang="ts">
	interface Props {
		value: number;
		max?: number;
		color?: string;
		height?: string;
	}

	let { value, max = 100, color = 'var(--color-accent)', height = '6px' }: Props = $props();

	let pct = $derived(Math.min(100, Math.max(0, (value / max) * 100)));
</script>

<div class="w-full rounded-full bg-bg-primary overflow-hidden" style="height: {height}">
	<div
		class="h-full rounded-full transition-all duration-300 ease-out"
		style="width: {pct}%; background-color: {color};"
	></div>
</div>
