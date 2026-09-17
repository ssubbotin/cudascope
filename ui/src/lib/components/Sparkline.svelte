<script lang="ts">
	interface Props {
		data: number[];
		height?: number;
		color?: string;
		/** Lower bound. Defaults to zero: every series on a card starts there. */
		min?: number;
		/**
		 * Upper bound. Given for a percentage, left out for anything else so
		 * the line follows its own data. Drawing tokens per second against a
		 * fixed 0..100 put every value above 100 outside the box, on top of
		 * the row above it.
		 */
		max?: number;
	}

	let { data, height = 40, color = 'var(--color-accent)', min = 0, max }: Props = $props();

	// The path is built in these units and the svg stretches to its container,
	// so every card's plot is as wide as its card.
	const VIEW_WIDTH = 200;

	let top = $derived.by(() => {
		if (max != null) return max;
		if (data.length === 0) return min + 1;
		const peak = Math.max(...data);
		// Headroom, so a peak does not sit on the frame. A flat series still
		// needs a range or every point lands on the same line.
		return peak > min ? min + (peak - min) * 1.1 : min + 1;
	});

	let path = $derived.by(() => {
		if (data.length < 2) return '';
		const range = top - min || 1;
		const stepX = VIEW_WIDTH / (data.length - 1);
		const points = data.map((v, i) => {
			const x = i * stepX;
			const clamped = Math.min(Math.max(v, min), top);
			const y = height - ((clamped - min) / range) * height;
			return `${x},${y}`;
		});
		return 'M' + points.join(' L');
	});

	let areaPath = $derived.by(() => {
		if (data.length < 2) return '';
		return path + ` L${VIEW_WIDTH},${height} L0,${height} Z`;
	});
</script>

<svg
	viewBox="0 0 {VIEW_WIDTH} {height}"
	{height}
	preserveAspectRatio="none"
	class="w-full block"
>
	{#if data.length >= 2}
		<path d={areaPath} fill={color} opacity="0.15" />
		<path
			d={path}
			fill="none"
			stroke={color}
			stroke-width="1.5"
			vector-effect="non-scaling-stroke"
		/>
	{/if}
</svg>
