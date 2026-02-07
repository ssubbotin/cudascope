<script lang="ts">
	import GPUCard from '$lib/components/GPUCard.svelte';
	import HostCard from '$lib/components/HostCard.svelte';
	import ProcessList from '$lib/components/ProcessList.svelte';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import { devices, latestGPU, latestHost, processes, gpuHistory, fetchGPUHistory } from '$lib/stores/metrics';
	import type { GPUMetrics } from '$lib/stores/metrics';

	let selectedRange = $state('5m');
	let historyData = $state<GPUMetrics[]>([]);
	let loading = $state(false);

	// Build GPU metrics lookup by gpu_id
	let gpuMap = $derived(new Map($latestGPU.map((g) => [g.gpu_id, g])));

	// Load historical data when range changes
	async function loadHistory(range: string) {
		selectedRange = range;
		loading = true;
		// Load for first GPU (or all GPUs)
		if ($devices.length > 0) {
			historyData = await fetchGPUHistory($devices[0].id, range);
		}
		loading = false;
	}

	// Build chart data from history
	let chartTimestamps = $derived(historyData.map((m) => m.ts));
	let chartUtilSeries = $derived([
		{ label: 'GPU Util', color: '#38bdf8', data: historyData.map((m) => m.gpu_util) },
		{ label: 'Mem Util', color: '#4ade80', data: historyData.map((m) => m.mem_util) }
	]);

	// Auto-refresh history every 10s
	let refreshInterval: ReturnType<typeof setInterval>;
	import { onMount, onDestroy } from 'svelte';

	onMount(() => {
		loadHistory(selectedRange);
		refreshInterval = setInterval(() => loadHistory(selectedRange), 10000);
	});

	onDestroy(() => {
		if (refreshInterval) clearInterval(refreshInterval);
	});
</script>

<svelte:head>
	<title>CudaScope - Dashboard</title>
</svelte:head>

<div class="space-y-6">
	<!-- GPU Cards -->
	<section>
		<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
			{#each $devices as device (device.id)}
				<GPUCard
					{device}
					metrics={gpuMap.get(device.id)}
					history={$gpuHistory.get(device.id) || []}
				/>
			{/each}

			<!-- Host Card -->
			<HostCard metrics={$latestHost} />
		</div>
	</section>

	<!-- History Chart -->
	<section class="bg-bg-card border border-border rounded-xl p-5">
		<div class="flex items-center justify-between mb-4">
			<h3 class="text-sm font-medium text-text-primary">GPU Utilization History</h3>
			<TimeRangePicker selected={selectedRange} onchange={loadHistory} />
		</div>

		{#if loading}
			<div class="flex items-center justify-center h-[200px] text-text-muted text-sm">
				Loading...
			</div>
		{:else if chartTimestamps.length >= 2}
			<TimeSeriesChart
				timestamps={chartTimestamps}
				series={chartUtilSeries}
				yMin={0}
				yMax={100}
				yLabel="%"
			/>
		{:else}
			<div class="flex items-center justify-center h-[200px] text-text-muted text-sm">
				No data yet. Collecting metrics...
			</div>
		{/if}
	</section>

	<!-- Process List -->
	<ProcessList processes={$processes} />
</div>
