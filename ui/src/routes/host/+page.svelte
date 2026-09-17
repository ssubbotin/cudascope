<script lang="ts">
	import { page } from '$app/stores';
	import { onDestroy } from 'svelte';
	import { onMessage, connected } from '$lib/stores/websocket';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import ProgressBar from '$lib/components/ProgressBar.svelte';
	import {
		latestHosts,
		nodes,
		fetchHostHistory,
		parseRangeSeconds,
		isLiveRange,
		appendPoint
	} from '$lib/stores/metrics';
	import type { HostMetrics } from '$lib/stores/metrics';
	import { formatBytes, formatNetRate, utilColor } from '$lib/utils/format';

	let nodeId = $derived($page.url.searchParams.get('node') || 'local');
	let metrics = $derived($latestHosts.get(nodeId) ?? null);
	let hostname = $derived($nodes.find((n) => n.node_id === nodeId)?.hostname ?? nodeId);

	let selectedRange = $state('5m');
	let autoRefresh = $state(true);
	let historyData = $state<HostMetrics[]>([]);
	let loading = $state(false);

	let xMax = $state(Math.floor(Date.now() / 1000));
	let xMin = $derived(xMax - parseRangeSeconds(selectedRange));

	async function loadHistory(range: string, silent = false) {
		selectedRange = range;
		if (!silent) loading = true;
		xMax = Math.floor(Date.now() / 1000);
		historyData = await fetchHostHistory(range, nodeId);
		loading = false;
		historyLoaded = true;
		setupRefresh();
	}

	// An hour or less is drawn from raw samples, which is what the websocket
	// carries, so the charts follow the stream instead of refetching.
	let liveTail = $derived(isLiveRange(selectedRange));

	const stopListening = onMessage((data: any) => {
		if (!liveTail || data.type !== 'host_metrics' || !data.host) return;
		if ((data.host.node_id || 'local') !== nodeId) return;

		historyData = appendPoint(historyData, data.host, parseRangeSeconds(selectedRange));
		xMax = Math.floor(Date.now() / 1000);
	});

	// Declared here rather than reusing the mount flag below: this callback
	// fires the moment it subscribes, before that declaration has run.
	let wasConnected = true;
	let historyLoaded = false;
	const stopWatchingConnection = connected.subscribe((isConnected) => {
		if (isConnected && !wasConnected && historyLoaded) loadHistory(selectedRange, true);
		wasConnected = isConnected;
	});

	let ts = $derived(historyData.map((m) => m.ts));
	const SYNC = 'host-detail';

	let cpuSeries = $derived([
		{ label: 'CPU', color: '#38bdf8', data: historyData.map((m) => m.cpu_percent) }
	]);

	let memSeries = $derived([
		{
			label: 'Used (GiB)',
			color: '#4ade80',
			data: historyData.map((m) => m.mem_used / (1024 * 1024 * 1024))
		}
	]);

	// Only the one minute average: the rollups keep that one, so a wide range
	// would otherwise draw two flat lines at zero and call them load.
	let loadSeries = $derived([
		{ label: 'Load (1m)', color: '#a78bfa', data: historyData.map((m) => m.load_1m) }
	]);

	let netSeries = $derived([
		{ label: 'Rx (KB/s)', color: '#38bdf8', data: historyData.map((m) => m.net_rx / 1000) },
		{ label: 'Tx (KB/s)', color: '#fb923c', data: historyData.map((m) => m.net_tx / 1000) }
	]);

	let memTotal = $derived(
		historyData.length > 0 ? historyData[0].mem_total / (1024 * 1024 * 1024) : undefined
	);

	let refreshInterval: ReturnType<typeof setInterval>;
	let initialized = $state(false);

	function setupRefresh() {
		clearInterval(refreshInterval);
		if (!autoRefresh || liveTail) return;
		// Wider ranges are drawn from rollups, which change once a minute.
		refreshInterval = setInterval(() => loadHistory(selectedRange, true), 60000);
	}

	$effect(() => {
		if (!initialized) {
			initialized = true;
			loadHistory(selectedRange);
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
</script>

<svelte:head>
	<title>Host {hostname} | CudaScope</title>
</svelte:head>

<div class="p-4 sm:p-6 space-y-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div class="flex items-center gap-3">
			<a href="/" class="text-sm text-text-muted hover:text-text-primary transition-colors">
				← Back
			</a>
			<div>
				<h2 class="text-lg font-semibold text-text-primary">Host: {hostname}</h2>
				{#if $nodes.length > 1}
					<p class="text-xs text-text-muted mt-0.5">node {nodeId}</p>
				{/if}
			</div>
		</div>

		<TimeRangePicker
			selected={selectedRange}
			onchange={(r) => loadHistory(r)}
			{autoRefresh}
			onRefreshToggle={handleRefreshToggle}
			onManualRefresh={() => loadHistory(selectedRange)}
		/>
	</div>

	{#if metrics}
		<div class="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">CPU</div>
				<div class="text-xl font-mono font-semibold" style="color: {utilColor(metrics.cpu_percent)}">
					{metrics.cpu_percent.toFixed(1)}%
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Memory</div>
				<div class="text-xl font-mono font-semibold text-text-primary">
					{formatBytes(metrics.mem_used)}
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Disk</div>
				<div class="text-xl font-mono font-semibold text-text-primary">
					{formatBytes(metrics.disk_used)}
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Load 1m</div>
				<div class="text-xl font-mono font-semibold text-text-primary">
					{metrics.load_1m.toFixed(2)}
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Net Rx</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">
					{formatNetRate(metrics.net_rx)}
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Net Tx</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">
					{formatNetRate(metrics.net_tx)}
				</div>
			</div>
		</div>

		<div class="bg-bg-card border border-border rounded-xl p-5 space-y-3">
			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">Memory</span>
					<span class="font-mono text-text-secondary">
						{formatBytes(metrics.mem_used)} / {formatBytes(metrics.mem_total)}
					</span>
				</div>
				<ProgressBar value={metrics.mem_used} max={metrics.mem_total} color="var(--color-accent)" />
			</div>
			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">Disk</span>
					<span class="font-mono text-text-secondary">
						{formatBytes(metrics.disk_used)} / {formatBytes(metrics.disk_total)}
					</span>
				</div>
				<ProgressBar value={metrics.disk_used} max={metrics.disk_total} color="var(--color-yellow)" />
			</div>
		</div>
	{/if}

	{#if !loading && ts.length >= 2}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">CPU (%)</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={cpuSeries}
					yMin={0}
					yMax={100}
					yLabel="%"
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">Memory (GiB)</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={memSeries}
					yMin={0}
					yMax={memTotal}
					yLabel="GiB"
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">Load average</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={loadSeries}
					yMin={0}
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">Network (KB/s)</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={netSeries}
					yMin={0}
					yLabel="KB/s"
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>
		</div>
	{:else if loading}
		<div class="text-sm text-text-muted text-center py-12">Loading…</div>
	{:else}
		<div class="text-sm text-text-muted text-center py-12">Not enough history for this range.</div>
	{/if}
</div>
