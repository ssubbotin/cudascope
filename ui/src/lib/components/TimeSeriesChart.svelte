<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import uPlot from 'uplot';
	import 'uplot/dist/uPlot.min.css';
	import { resolvedTheme } from '$lib/stores/theme';

	interface Series {
		label: string;
		color: string;
		/**
		 * null is a gap: the window measured nothing. uPlot leaves the line
		 * open there, and spanGaps below joins the two readings around it
		 * rather than dropping the line to the axis.
		 */
		data: (number | null)[];
	}

	interface Props {
		timestamps: number[];
		series: Series[];
		height?: number;
		yMin?: number;
		yMax?: number;
		yLabel?: string;
		syncKey?: string;
		xMin?: number;
		xMax?: number;
	}

	let { timestamps, series, height = 200, yMin = 0, yMax, yLabel = '', syncKey, xMin, xMax }: Props = $props();

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
		const width = container.clientWidth;
		const tc = themeColors();

		const opts: uPlot.Options = {
			width,
			height,
			cursor: {
				drag: { x: true, y: false, setScale: true },
				sync: syncKey ? { key: syncKey, setSeries: true } : undefined,
			},
			scales: {
				x: {
					time: true,
					// A function rather than a fixed pair: the window is a prop,
					// and a chart that is never rebuilt would otherwise keep the
					// range it was born with. The host charts are exactly that
					// case, so switching to a wider range left them showing the
					// first window they ever drew.
					range: (u: uPlot, dataMin: number, dataMax: number) =>
						xMin != null && xMax != null
							? [xMin, xMax]
							: uPlot.rangeNum(dataMin, dataMax, 0.1, true),
				},
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
					// Join the readings across a window that measured nothing.
					// Without this the line is a scatter of short segments on
					// any series that only updates when work arrives.
					spanGaps: true,
				}))
			]
		};

		return opts;
	}

	function buildData(): uPlot.AlignedData {
		// A typed array cannot hold a gap: null becomes zero in it, which is
		// the floor of the chart and reads as a measurement. A series with
		// gaps stays a plain array.
		return [
			new Float64Array(timestamps),
			...series.map((s) =>
				s.data.some((v) => v == null) ? (s.data as (number | null)[]) : new Float64Array(s.data as number[])
			)
		] as uPlot.AlignedData;
	}

	function createChart() {
		// A container of no width means the layout has not happened yet: the
		// tab is in the background, or the panel is still hidden. Building the
		// chart anyway used to fall back to 600 pixels and keep that width for
		// ever, which drew the series into the left part of a wide panel and
		// left the rest of it blank. The observer below builds it once the
		// width arrives.
		if (!container || container.clientWidth === 0 || timestamps.length < 2) return;
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
		if (!container) return;
		const width = container.clientWidth;
		if (width === 0) return;
		// Born without a width, or not born at all while the data was short.
		if (!chart) {
			createChart();
			return;
		}
		if (Math.round(chart.width) !== width) chart.setSize({ width, height });
	}

	// The window is not the only thing that changes a chart's width: a grid
	// reflows, a panel is revealed, a scrollbar comes and goes, and none of
	// those raise a window resize. Watching the container covers the window
	// case as well, so it is the only listener.
	let observer: ResizeObserver | null = null;

	onMount(() => {
		createChart();
		observer = new ResizeObserver(handleResize);
		observer.observe(container);
	});

	onDestroy(() => {
		observer?.disconnect();
		observer = null;
		destroyChart();
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

	// React to the window moving. Without this a chart only follows the
	// selected range when something else happens to rebuild it.
	$effect(() => {
		const min = xMin;
		const max = xMax;
		if (chart && min != null && max != null) {
			chart.setScale('x', { min, max });
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
