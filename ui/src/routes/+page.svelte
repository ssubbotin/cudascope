<script lang="ts">
	import { onMount, onDestroy } from 'svelte';
	import GPUCard from '$lib/components/GPUCard.svelte';
	import HostCard from '$lib/components/HostCard.svelte';
	import ProcessList from '$lib/components/ProcessList.svelte';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import { devices, latestGPU, latestHost, processes, gpuHistory, fetchGPUHistory, fetchHostHistory } from '$lib/stores/metrics';
	import type { GPUMetrics, HostMetrics } from '$lib/stores/metrics';

	const GPU_COLORS = ['#38bdf8', '#4ade80', '#fbbf24', '#f87171', '#a78bfa', '#fb923c', '#2dd4bf', '#e879f9'];

	let selectedRange = $state('5m');
	let autoRefresh = $state(true);
	let loading = $state(false);

	// Per-GPU history data keyed by gpu_id
	let allGPUHistory = $state<Map<number, GPUMetrics[]>>(new Map());
	let hostHistoryData = $state<HostMetrics[]>([]);

	let gpuMap = $derived(new Map($latestGPU.map((g) => [g.gpu_id, g])));

	async function loadHistory(range: string) {
		selectedRange = range;
		loading = true;

		const promises = $devices.map(async (d) => {
			const data = await fetchGPUHistory(d.id, range);
			return [d.id, data] as const;
		});

		const results = await Promise.all(promises);
		const map = new Map<number, GPUMetrics[]>();
		for (const [id, data] of results) map.set(id, data);
		allGPUHistory = map;

		hostHistoryData = await fetchHostHistory(range);
		loading = false;
	}

	// Build multi-GPU overlay chart data: use timestamps from first GPU
	let chartTimestamps = $derived.by(() => {
		for (const [, data] of allGPUHistory) {
			if (data.length >= 2) return data.map((m) => m.ts);
		}
		return [];
	});

	let utilSeries = $derived(
		$devices.map((d, i) => ({
			label: `GPU ${d.id}`,
			color: GPU_COLORS[i % GPU_COLORS.length],
			data: (allGPUHistory.get(d.id) || []).map((m) => m.gpu_util)
		}))
	);

	let memSeries = $derived(
		$devices.map((d, i) => ({
			label: `GPU ${d.id}`,
			color: GPU_COLORS[i % GPU_COLORS.length],
			data: (allGPUHistory.get(d.id) || []).map((m) => m.mem_used)
		}))
	);

	let hostTs = $derived(hostHistoryData.map((m) => m.ts));
	let cpuSeries = $derived([
		{ label: 'CPU', color: '#38bdf8', data: hostHistoryData.map((m) => m.cpu_percent) }
	]);
	let hostMemSeries = $derived([
		{ label: 'RAM', color: '#4ade80', data: hostHistoryData.map((m) => m.mem_used / (1024 * 1024 * 1024)) }
	]);

	let maxMem = $derived(
		$devices.reduce((max, d) => Math.max(max, d.mem_total), 0)
	);
	let hostMemTotal = $derived(
		hostHistoryData.length > 0 ? hostHistoryData[0].mem_total / (1024 * 1024 * 1024) : undefined
	);

	let refreshInterval: ReturnType<typeof setInterval>;

	function setupRefresh() {
		clearInterval(refreshInterval);
		if (autoRefresh) {
			refreshInterval = setInterval(() => loadHistory(selectedRange), 10000);
		}
	}

	onMount(() => {
		loadHistory(selectedRange);
		setupRefresh();
	});

	onDestroy(() => {
		clearInterval(refreshInterval);
	});

	function handleRefreshToggle(enabled: boolean) {
		autoRefresh = enabled;
		setupRefresh();
	}
</script>

<svelte:head>
	<title>CudaScope - Dashboard</title>
</svelte:head>

<div class="space-y-6">
	<!-- GPU + Host Cards -->
	<section>
		<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
			{#each $devices as device (device.id)}
				<GPUCard
					{device}
					metrics={gpuMap.get(device.id)}
					history={$gpuHistory.get(device.id) || []}
				/>
			{/each}
			<HostCard metrics={$latestHost} />
		</div>
	</section>

	<!-- Time Range Controls -->
	<div class="flex items-center justify-between flex-wrap gap-2">
		<h3 class="text-sm font-medium text-text-primary">History</h3>
		<TimeRangePicker
			selected={selectedRange}
			onchange={loadHistory}
			{autoRefresh}
			onRefreshToggle={handleRefreshToggle}
			onManualRefresh={() => loadHistory(selectedRange)}
		/>
	</div>

	<!-- GPU Utilization Overlay -->
	<section class="bg-bg-card border border-border rounded-xl p-5">
		<h4 class="text-xs font-medium text-text-muted mb-3">GPU Utilization (%)</h4>
		{#if !loading && chartTimestamps.length >= 2}
			<TimeSeriesChart
				timestamps={chartTimestamps}
				series={utilSeries}
				yMin={0}
				yMax={100}
				yLabel="%"
				syncKey="dashboard"
			/>
		{:else if loading}
			<div class="flex items-center justify-center h-[200px] text-text-muted text-sm">Loading...</div>
		{:else}
			<div class="flex items-center justify-center h-[200px] text-text-muted text-sm">Collecting metrics...</div>
		{/if}
	</section>

	<!-- GPU Memory Overlay -->
	<section class="bg-bg-card border border-border rounded-xl p-5">
		<h4 class="text-xs font-medium text-text-muted mb-3">GPU Memory (MiB)</h4>
		{#if !loading && chartTimestamps.length >= 2}
			<TimeSeriesChart
				timestamps={chartTimestamps}
				series={memSeries}
				yMin={0}
				yMax={maxMem || undefined}
				yLabel="MiB"
				syncKey="dashboard"
			/>
		{:else}
			<div class="h-[200px]"></div>
		{/if}
	</section>

	<!-- Host CPU + Memory -->
	{#if hostTs.length >= 2}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<section class="bg-bg-card border border-border rounded-xl p-5">
				<h4 class="text-xs font-medium text-text-muted mb-3">Host CPU (%)</h4>
				<TimeSeriesChart timestamps={hostTs} series={cpuSeries} yMin={0} yMax={100} yLabel="%" syncKey="dashboard" />
			</section>
			<section class="bg-bg-card border border-border rounded-xl p-5">
				<h4 class="text-xs font-medium text-text-muted mb-3">Host RAM (GiB)</h4>
				<TimeSeriesChart timestamps={hostTs} series={hostMemSeries} yMin={0} yMax={hostMemTotal} yLabel="GiB" syncKey="dashboard" />
			</section>
		</div>
	{/if}

	<!-- Processes -->
	<ProcessList processes={$processes} />
</div>
