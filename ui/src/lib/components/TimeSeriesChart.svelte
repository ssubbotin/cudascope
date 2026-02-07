<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';
	import { resolvedTheme } from '$lib/stores/theme';

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
		syncKey?: string;
	}

	let { timestamps, series, height = 200, yMin = 0, yMax, yLabel = '', syncKey }: Props = $props();

	let container: HTMLDivElement;
	let chart: uPlot | null = null;
	let currentTheme: 'dark' | 'light' = 'dark';

	// Shared sync instances for crosshair synchronization
	const syncInstances = new Map<string, uPlot.SyncPubSub>();
	function getSync(key: string): uPlot.SyncPubSub {
		if (!syncInstances.has(key)) {
			syncInstances.set(key, uPlot.sync(key));
		}
		return syncInstances.get(key)!;
	}

	function themeColors() {
		const light = currentTheme === 'light';
		return {
			axis: light ? '#94a3b8' : '#64748b',
			grid: light ? '#f1f5f9' : '#1e293b',
			tick: light ? '#e2e8f0' : '#334155',
		};
	}

	function buildOpts(): uPlot.Options {
		const width = container?.clientWidth || 600;
		const tc = themeColors();

		const opts: uPlot.Options = {
			width,
			height,
			cursor: {
				drag: { x: true, y: false, setScale: true },
				sync: syncKey ? { key: syncKey, setSeries: true } : undefined,
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
					stroke: tc.axis,
					grid: { stroke: tc.grid, width: 1 },
					ticks: { stroke: tc.tick, width: 1 },
					font: '10px system-ui',
				},
				{
					stroke: tc.axis,
					grid: { stroke: tc.grid, width: 1 },
					ticks: { stroke: tc.tick, width: 1 },
					font: '10px system-ui',
					label: yLabel,
					labelFont: '10px system-ui',
					labelSize: 12,
					size: 50,
				}
			],
			series: [
				{},
				...series.map((s) => ({
					label: s.label,
					stroke: s.color,
					width: 1.5,
					fill: s.color + '20',
					points: { show: false },
				}))
			]
		};

		return opts;
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
		timestamps;
		series;

		if (chart && timestamps.length >= 2) {
			chart.setData(buildData());
		} else if (!chart && timestamps.length >= 2 && container) {
			createChart();
		}
	});

	// React to theme changes — recreate chart
	resolvedTheme.subscribe((theme) => {
		if (theme !== currentTheme) {
			currentTheme = theme;
			if (chart) createChart();
		}
	});
</script>

<div bind:this={container} class="w-full"></div>
