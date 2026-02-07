<script lang="ts">
	import { page } from '$app/stores';
	import { onMount, onDestroy } from 'svelte';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import ProcessList from '$lib/components/ProcessList.svelte';
	import ProgressBar from '$lib/components/ProgressBar.svelte';
	import { devices, latestGPU, processes, fetchGPUHistory } from '$lib/stores/metrics';
	import type { GPUMetrics, GPUDevice } from '$lib/stores/metrics';
	import { formatMiB, formatWatts, formatTemp, utilColor, tempColor } from '$lib/utils/format';

	let gpuId = $derived(parseInt($page.params.id));
	let device = $derived($devices.find((d) => d.id === gpuId));
	let metrics = $derived($latestGPU.find((g) => g.gpu_id === gpuId));
	let gpuProcs = $derived($processes.filter((p) => p.gpu_id === gpuId));

	let selectedRange = $state('5m');
	let historyData = $state<GPUMetrics[]>([]);
	let loading = $state(false);

	async function loadHistory(range: string) {
		selectedRange = range;
		loading = true;
		historyData = await fetchGPUHistory(gpuId, range);
		loading = false;
	}

	let ts = $derived(historyData.map((m) => m.ts));

	let utilSeries = $derived([
		{ label: 'GPU', color: '#38bdf8', data: historyData.map((m) => m.gpu_util) },
		{ label: 'Memory', color: '#4ade80', data: historyData.map((m) => m.mem_util) }
	]);

	let memSeries = $derived([
		{ label: 'Used (MiB)', color: '#38bdf8', data: historyData.map((m) => m.mem_used) }
	]);

	let tempSeries = $derived([
		{ label: 'Temp (C)', color: '#f87171', data: historyData.map((m) => m.temperature) },
		{ label: 'Fan (%)', color: '#94a3b8', data: historyData.map((m) => m.fan_speed) }
	]);

	let powerSeries = $derived([
		{ label: 'Power (W)', color: '#fbbf24', data: historyData.map((m) => m.power_draw) }
	]);

	let clockSeries = $derived([
		{ label: 'Graphics (MHz)', color: '#38bdf8', data: historyData.map((m) => m.clock_gfx) },
		{ label: 'Memory (MHz)', color: '#a78bfa', data: historyData.map((m) => m.clock_mem) }
	]);

	let pcieSeries = $derived([
		{ label: 'TX (KB/s)', color: '#38bdf8', data: historyData.map((m) => m.pcie_tx) },
		{ label: 'RX (KB/s)', color: '#4ade80', data: historyData.map((m) => m.pcie_rx) }
	]);

	let refreshInterval: ReturnType<typeof setInterval>;

	onMount(() => {
		loadHistory(selectedRange);
		refreshInterval = setInterval(() => loadHistory(selectedRange), 10000);
	});

	onDestroy(() => {
		if (refreshInterval) clearInterval(refreshInterval);
	});
</script>

<svelte:head>
	<title>CudaScope - GPU {gpuId}</title>
</svelte:head>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex items-center justify-between">
		<div class="flex items-center gap-4">
			<a href="/" class="text-text-muted hover:text-accent transition-colors text-sm">&larr; Back</a>
			<div>
				<h2 class="text-lg font-semibold text-text-primary">GPU {gpuId}: {device?.name || '...'}</h2>
				<p class="text-xs text-text-muted">{device?.uuid || ''} &middot; Driver {device?.driver_ver || ''}</p>
			</div>
		</div>
		<TimeRangePicker selected={selectedRange} onchange={loadHistory} />
	</div>

	<!-- Live Stats -->
	{#if metrics}
		<div class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-7 gap-3">
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">GPU Util</div>
				<div class="text-xl font-mono font-semibold" style="color: {utilColor(metrics.gpu_util)}">{metrics.gpu_util.toFixed(0)}%</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">VRAM</div>
				<div class="text-xl font-mono font-semibold text-accent">{formatMiB(metrics.mem_used)}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Temperature</div>
				<div class="text-xl font-mono font-semibold" style="color: {tempColor(metrics.temperature)}">{formatTemp(metrics.temperature)}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Fan</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">{metrics.fan_speed}%</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Power</div>
				<div class="text-xl font-mono font-semibold text-yellow">{formatWatts(metrics.power_draw)}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Clock GFX</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">{metrics.clock_gfx} MHz</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">PState</div>
				<div class="text-xl font-mono font-semibold text-green">P{metrics.pstate}</div>
			</div>
		</div>
	{/if}

	<!-- Charts Grid -->
	{#if !loading && ts.length >= 2}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">Utilization</h3>
				<TimeSeriesChart timestamps={ts} series={utilSeries} yMin={0} yMax={100} yLabel="%" />
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">Memory Usage</h3>
				<TimeSeriesChart timestamps={ts} series={memSeries} yMin={0} yMax={device?.mem_total} yLabel="MiB" />
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">Temperature / Fan</h3>
				<TimeSeriesChart timestamps={ts} series={tempSeries} yMin={0} />
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">Power Draw</h3>
				<TimeSeriesChart timestamps={ts} series={powerSeries} yMin={0} yLabel="W" />
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">Clock Speeds</h3>
				<TimeSeriesChart timestamps={ts} series={clockSeries} yMin={0} yLabel="MHz" />
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-sm font-medium text-text-primary mb-3">PCIe Throughput</h3>
				<TimeSeriesChart timestamps={ts} series={pcieSeries} yMin={0} yLabel="KB/s" />
			</div>
		</div>
	{:else if loading}
		<div class="text-center py-12 text-text-muted">Loading history...</div>
	{:else}
		<div class="text-center py-12 text-text-muted">Collecting metrics...</div>
	{/if}

	<!-- Processes -->
	<ProcessList processes={gpuProcs} />
</div>
