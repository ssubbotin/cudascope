<script lang="ts">
	import { onDestroy } from 'svelte';
	import { onMessage, connected } from '$lib/stores/websocket';
	import GPUCard from '$lib/components/GPUCard.svelte';
	import HostCard from '$lib/components/HostCard.svelte';
	import VLLMCard from '$lib/components/VLLMCard.svelte';
	import ProcessList from '$lib/components/ProcessList.svelte';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import NodeSelector from '$lib/components/NodeSelector.svelte';
	import { devices, latestGPU, latestHosts, processes, gpuHistory, nodes, selectedNode, gpuKey, fetchGPUHistory, fetchHostHistory, parseRangeSeconds, latestVLLM, vllmHistory, isLiveRange, appendPoint } from '$lib/stores/metrics';
	import type { GPUMetrics, HostMetrics } from '$lib/stores/metrics';

	const GPU_COLORS = ['#38bdf8', '#4ade80', '#fbbf24', '#f87171', '#a78bfa', '#fb923c', '#2dd4bf', '#e879f9'];

	let selectedRange = $state('5m');
	let autoRefresh = $state(true);
	let loading = $state(false);

	// Time range bounds for chart X axis
	let xMax = $state(Math.floor(Date.now() / 1000));
	let xMin = $derived(xMax - parseRangeSeconds(selectedRange));

	// Per-GPU history data keyed by gpu_id
	let allGPUHistory = $state<Map<number, GPUMetrics[]>>(new Map());
	let hostHistoryData = $state<HostMetrics[]>([]);

	// Filtered views based on selected node
	let filteredDevices = $derived(
		$selectedNode === 'all'
			? $devices
			: $devices.filter((d) => d.node_id === $selectedNode)
	);

	let filteredGPU = $derived(
		$selectedNode === 'all'
			? $latestGPU
			: $latestGPU.filter((g) => (g.node_id || 'local') === $selectedNode)
	);

	let filteredHosts = $derived.by(() => {
		if ($selectedNode === 'all') {
			return [...$latestHosts.values()];
		}
		const h = $latestHosts.get($selectedNode);
		return h ? [h] : [];
	});

	let filteredProcesses = $derived(
		$selectedNode === 'all'
			? $processes
			: $processes.filter((p) => (p.node_id || 'local') === $selectedNode)
	);

	let gpuMap = $derived(new Map(filteredGPU.map((g) => [gpuKey(g.node_id, g.gpu_id), g])));

	async function loadHistory(range: string, silent = false) {
		selectedRange = range;
		if (!silent) loading = true;
		xMax = Math.floor(Date.now() / 1000);

		const nodeFilter = $selectedNode === 'all' ? undefined : $selectedNode;

		const promises = filteredDevices.map(async (d) => {
			const data = await fetchGPUHistory(d.id, range, nodeFilter || d.node_id);
			return [d, data] as const;
		});

		const results = await Promise.all(promises);
		const map = new Map<number, GPUMetrics[]>();
		for (const [device, data] of results) {
			// Use a unique numeric key for chart indexing
			const key = filteredDevices.indexOf(device);
			map.set(key, data);
		}
		allGPUHistory = map;

		hostHistoryData = await fetchHostHistory(range, nodeFilter);
		loading = false;
		historyLoaded = true;
		setupRefresh();
	}

	// Charts of an hour or less follow the websocket. Refetching the whole
	// window every ten seconds sent the same thousands of points over and
	// over while the stream was already delivering each new one.
	let liveTail = $derived(isLiveRange(selectedRange) && ($nodes.length <= 1 || $selectedNode !== 'all'));

	function appendGPUPoints(gpus: GPUMetrics[], nodeId: string) {
		const window = parseRangeSeconds(selectedRange);
		const next = new Map(allGPUHistory);
		let changed = false;

		filteredDevices.forEach((device, index) => {
			if ((device.node_id || 'local') !== nodeId) return;
			const sample = gpus.find((g) => g.gpu_id === device.id);
			if (!sample) return;
			next.set(index, appendPoint(next.get(index) || [], { ...sample, node_id: nodeId }, window));
			changed = true;
		});

		if (changed) {
			allGPUHistory = next;
			xMax = Math.floor(Date.now() / 1000);
		}
	}

	const stopListening = onMessage((data: any) => {
		if (!liveTail) return;
		const nodeId = data.node_id || 'local';
		if ($selectedNode !== 'all' && nodeId !== $selectedNode) return;

		if (data.type === 'gpu_metrics' && data.gpus) {
			appendGPUPoints(data.gpus, nodeId);
		}
		if (data.type === 'host_metrics' && data.host) {
			hostHistoryData = appendPoint(hostHistoryData, data.host, parseRangeSeconds(selectedRange));
		}
	});

	// A dropped connection leaves a gap the stream cannot fill, so refetch
	// once it is back.
	// Declared here rather than reusing the mount flag below: this callback
	// fires the moment it subscribes, before that declaration has run.
	let wasConnected = true;
	let historyLoaded = false;
	const stopWatchingConnection = connected.subscribe((isConnected) => {
		if (isConnected && !wasConnected && historyLoaded) loadHistory(selectedRange, true);
		wasConnected = isConnected;
	});

	// Build multi-GPU overlay chart data: use timestamps from first GPU
	let chartTimestamps = $derived.by(() => {
		for (const [, data] of allGPUHistory) {
			if (data.length >= 2) return data.map((m) => m.ts);
		}
		return [];
	});

	let utilSeries = $derived(
		filteredDevices.map((d, i) => ({
			label: $nodes.length > 1 ? `${d.node_id}:GPU${d.id}` : `GPU ${d.id}`,
			color: GPU_COLORS[i % GPU_COLORS.length],
			data: (allGPUHistory.get(i) || []).map((m) => m.gpu_util)
		}))
	);

	let memSeries = $derived(
		filteredDevices.map((d, i) => ({
			label: $nodes.length > 1 ? `${d.node_id}:GPU${d.id}` : `GPU ${d.id}`,
			color: GPU_COLORS[i % GPU_COLORS.length],
			data: (allGPUHistory.get(i) || []).map((m) => m.mem_used)
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
		filteredDevices.reduce((max, d) => Math.max(max, d.mem_total), 0)
	);
	let hostMemTotal = $derived(
		hostHistoryData.length > 0 ? hostHistoryData[0].mem_total / (1024 * 1024 * 1024) : undefined
	);

	let refreshInterval: ReturnType<typeof setInterval>;
	let initialized = $state(false);

	function setupRefresh() {
		clearInterval(refreshInterval);
		if (!autoRefresh || liveTail) return;
		// Wider ranges are drawn from rollups, which change once a minute at
		// most.
		refreshInterval = setInterval(() => loadHistory(selectedRange, true), 60000);
	}

	// Wait for devices to be populated (fetchStatus in layout is async)
	// then trigger initial history load
	$effect(() => {
		if (filteredDevices.length > 0 && !initialized) {
			initialized = true;
			loadHistory(selectedRange);
			setupRefresh();
		}
	});

	onDestroy(() => {
		clearInterval(refreshInterval);
		stopListening();
		stopWatchingConnection();
	});

	function handleRefreshToggle(enabled: boolean) {
		autoRefresh = enabled;
		setupRefresh();
	}

	function handleNodeChange(nodeId: string) {
		$selectedNode = nodeId;
		loadHistory(selectedRange);
	}
</script>

<svelte:head>
	<title>CudaScope - Dashboard</title>
</svelte:head>

<div class="space-y-6">
	<!-- Node Selector (only shown for multi-node) -->
	{#if $nodes.length > 1}
		<div class="flex items-center justify-between flex-wrap gap-2">
			<NodeSelector nodes={$nodes} selected={$selectedNode} onchange={handleNodeChange} />
		</div>
	{/if}

	<!-- GPU + Host Cards -->
	<section>
		<div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
			{#each filteredDevices as device (gpuKey(device.node_id, device.id))}
				<GPUCard
					{device}
					metrics={gpuMap.get(gpuKey(device.node_id, device.id))}
					history={$gpuHistory.get(gpuKey(device.node_id, device.id)) || []}
					showNode={$nodes.length > 1}
				/>
			{/each}
			{#each filteredHosts as host (host.node_id)}
				<HostCard metrics={host} />
			{/each}
			{#if $latestVLLM}
				<VLLMCard metrics={$latestVLLM} history={$vllmHistory} />
			{/if}
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
				{xMin}
				{xMax}
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
				{xMin}
				{xMax}
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
				<TimeSeriesChart timestamps={hostTs} series={cpuSeries} yMin={0} yMax={100} yLabel="%" syncKey="dashboard" {xMin} {xMax} />
			</section>
			<section class="bg-bg-card border border-border rounded-xl p-5">
				<h4 class="text-xs font-medium text-text-muted mb-3">Host RAM (GiB)</h4>
				<TimeSeriesChart timestamps={hostTs} series={hostMemSeries} yMin={0} yMax={hostMemTotal} yLabel="GiB" syncKey="dashboard" {xMin} {xMax} />
			</section>
		</div>
	{/if}

	<!-- Processes -->
	<ProcessList processes={filteredProcesses} showNode={$nodes.length > 1} />
</div>
