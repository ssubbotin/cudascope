<script lang="ts">
	interface Props {
		data: number[];
		width?: number;
		height?: number;
		color?: string;
		min?: number;
		max?: number;
	}

	let { data, width = 200, height = 40, color = 'var(--color-accent)', min = 0, max = 100 }: Props = $props();

	let path = $derived.by(() => {
		if (data.length < 2) return '';
		const range = max - min || 1;
		const stepX = width / (data.length - 1);
		const points = data.map((v, i) => {
			const x = i * stepX;
			const y = height - ((v - min) / range) * height;
			return `${x},${y}`;
		});
		return 'M' + points.join(' L');
	});

	let areaPath = $derived.by(() => {
		if (data.length < 2) return '';
		return path + ` L${width},${height} L0,${height} Z`;
	});
</script>

<svg {width} {height} class="overflow-visible">
	{#if data.length >= 2}
		<path d={areaPath} fill={color} opacity="0.15" />
		<path d={path} fill="none" stroke={color} stroke-width="1.5" />
	{/if}
</svg>
