<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';

	interface Series {
		label: string;
		color: string;
		data: number[];
	}

	interface Props {
		timestamps: number[];
		series: Series[];
		height?: number;
		yMin?: number;
		yMax?: number;
		yLabel?: string;
	}

	let { timestamps, series, height = 200, yMin = 0, yMax, yLabel = '' }: Props = $props();

	let container: HTMLDivElement;
	let chart: uPlot | null = null;

	function buildOpts(): uPlot.Options {
		const width = container?.clientWidth || 600;
		return {
			width,
			height,
			cursor: {
				drag: { x: true, y: false, setScale: true }
			},
			scales: {
				x: { time: true },
				y: {
					auto: yMax === undefined,
					range: yMax !== undefined ? [yMin, yMax] : undefined
				}
			},
			axes: [
				{
					stroke: '#64748b',
					grid: { stroke: '#1e293b', width: 1 },
					ticks: { stroke: '#334155', width: 1 },
					font: '10px system-ui',
				},
				{
					stroke: '#64748b',
					grid: { stroke: '#1e293b', width: 1 },
					ticks: { stroke: '#334155', width: 1 },
					font: '10px system-ui',
					label: yLabel,
					labelFont: '10px system-ui',
					size: 50,
				}
			],
			series: [
				{}, // x-axis (timestamps)
				...series.map((s) => ({
					label: s.label,
					stroke: s.color,
					width: 1.5,
					fill: s.color + '20',
				}))
			]
		};
	}

	function buildData(): uPlot.AlignedData {
		return [
			new Float64Array(timestamps),
			...series.map((s) => new Float64Array(s.data))
		];
	}

	function createChart() {
		if (!container || timestamps.length < 2) return;
		destroyChart();
		chart = new uPlot(buildOpts(), buildData(), container);
	}

	function destroyChart() {
		if (chart) {
			chart.destroy();
			chart = null;
		}
	}

	function handleResize() {
		if (chart && container) {
			chart.setSize({ width: container.clientWidth, height });
		}
	}

	onMount(() => {
		createChart();
		window.addEventListener('resize', handleResize);
	});

	onDestroy(() => {
		destroyChart();
		if (typeof window !== 'undefined') {
			window.removeEventListener('resize', handleResize);
		}
	});

	// React to data changes
	$effect(() => {
		// Touch reactive deps
		timestamps;
		series;

		if (chart && timestamps.length >= 2) {
			chart.setData(buildData());
		} else if (!chart && timestamps.length >= 2 && container) {
			createChart();
		}
	});
</script>

<div bind:this={container} class="w-full"></div>
