<script lang="ts">
	import { onDestroy } from 'svelte';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import ProgressBar from '$lib/components/ProgressBar.svelte';
	import { latestVLLM, fetchVLLMHistory, parseRangeSeconds } from '$lib/stores/metrics';
	import type { VLLMMetrics } from '$lib/stores/metrics';
	import { utilColor } from '$lib/utils/format';

	let selectedRange = $state('5m');
	let autoRefresh = $state(true);
	let historyData = $state<VLLMMetrics[]>([]);
	let loading = $state(false);

	let xMax = $state(Math.floor(Date.now() / 1000));
	let xMin = $derived(xMax - parseRangeSeconds(selectedRange));

	async function loadHistory(range: string, silent = false) {
		selectedRange = range;
		if (!silent) loading = true;
		xMax = Math.floor(Date.now() / 1000);
		historyData = await fetchVLLMHistory(range);
		loading = false;
	}

	let ts = $derived(historyData.map((m) => m.ts));
	const SYNC = 'vllm-detail';

	let throughputSeries = $derived([
		{ label: 'tok/s', color: '#38bdf8', data: historyData.map((m) => m.token_throughput) }
	]);

	let requestsSeries = $derived([
		{ label: 'Running', color: '#4ade80', data: historyData.map((m) => m.requests_running) },
		{ label: 'Waiting', color: '#fbbf24', data: historyData.map((m) => m.requests_waiting) }
	]);

	let kvCacheSeries = $derived([
		{ label: 'KV Cache %', color: '#a78bfa', data: historyData.map((m) => m.kv_cache_usage * 100) }
	]);

	let latencySeries = $derived([
		{ label: 'TTFT (ms)', color: '#f87171', data: historyData.map((m) => m.ttft_avg * 1000) },
		{ label: 'per token (ms)', color: '#fb923c', data: historyData.map((m) => m.tpot_avg * 1000) }
	]);

	let tokensSeries = $derived([
		{ label: 'Generation', color: '#38bdf8', data: historyData.map((m) => m.generation_tokens_total) },
		{ label: 'Prompt', color: '#4ade80', data: historyData.map((m) => m.prompt_tokens_total) }
	]);

	let cacheSeries = $derived([
		{ label: 'Hit Rate %', color: '#2dd4bf', data: historyData.map((m) => m.prefix_cache_hit_rate * 100) }
	]);

	function formatTps(v: number): string {
		if (v >= 100) return v.toFixed(0);
		if (v >= 10) return v.toFixed(1);
		return v.toFixed(2);
	}

	function formatMs(seconds: number): string {
		if (seconds <= 0) return '--';
		const ms = seconds * 1000;
		if (ms >= 1000) return (ms / 1000).toFixed(1) + 's';
		return ms.toFixed(0) + 'ms';
	}

	let refreshInterval: ReturnType<typeof setInterval>;
	let initialized = $state(false);

	function setupRefresh() {
		clearInterval(refreshInterval);
		if (autoRefresh) {
			refreshInterval = setInterval(() => loadHistory(selectedRange, true), 10000);
		}
	}

	$effect(() => {
		if (!initialized) {
			initialized = true;
			loadHistory(selectedRange);
			setupRefresh();
		}
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
	<title>CudaScope - vLLM</title>
</svelte:head>

<div class="space-y-6">
	<!-- Header -->
	<div class="flex flex-col sm:flex-row sm:items-center justify-between gap-3">
		<div class="flex items-center gap-4">
			<a href="/" class="text-text-muted hover:text-accent transition-colors text-sm">&larr; Back</a>
			<div>
				<h2 class="text-lg font-semibold text-text-primary">
					vLLM: {$latestVLLM?.model_name || 'disconnected'}
				</h2>
				<p class="text-xs text-text-muted">Inference server metrics</p>
			</div>
		</div>
		<TimeRangePicker
			selected={selectedRange}
			onchange={loadHistory}
			{autoRefresh}
			onRefreshToggle={handleRefreshToggle}
			onManualRefresh={() => loadHistory(selectedRange)}
		/>
	</div>

	<!-- Live Stats -->
	{#if $latestVLLM}
		{@const m = $latestVLLM}
		<div class="grid grid-cols-2 sm:grid-cols-4 lg:grid-cols-7 gap-3">
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Throughput</div>
				<div class="text-xl font-mono font-semibold" style="color: {m.token_throughput > 50 ? 'var(--color-green)' : m.token_throughput > 10 ? 'var(--color-yellow)' : 'var(--color-red)'}">
					{formatTps(m.token_throughput)}
				</div>
				<div class="text-xs text-text-muted">tok/s</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Running</div>
				<div class="text-xl font-mono font-semibold text-accent">{m.requests_running}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Waiting</div>
				<div class="text-xl font-mono font-semibold" style="color: {m.requests_waiting > 0 ? 'var(--color-yellow)' : 'var(--color-text-secondary)'}">
					{m.requests_waiting}
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">KV Cache</div>
				<div class="text-xl font-mono font-semibold" style="color: {utilColor(m.kv_cache_usage * 100)}">
					{(m.kv_cache_usage * 100).toFixed(1)}%
				</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">TTFT</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">{formatMs(m.ttft_avg)}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Per Token</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">{formatMs(m.tpot_avg)}</div>
			</div>
			<div class="bg-bg-card border border-border rounded-lg p-3 text-center">
				<div class="text-xs text-text-muted">Cache Hit</div>
				<div class="text-xl font-mono font-semibold text-text-secondary">{(m.prefix_cache_hit_rate * 100).toFixed(0)}%</div>
			</div>
		</div>
	{:else}
		<div class="bg-bg-card border border-border rounded-lg p-6 text-center text-text-muted">
			No vLLM endpoint configured or server unreachable
		</div>
	{/if}

	<!-- Charts -->
	{#if loading}
		<div class="text-center text-text-muted py-8">Loading history...</div>
	{:else if historyData.length > 0}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<TimeSeriesChart title="Token Throughput (tok/s)" {ts} series={throughputSeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
			<TimeSeriesChart title="Active Requests" {ts} series={requestsSeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
			<TimeSeriesChart title="KV Cache Usage (%)" {ts} series={kvCacheSeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
			<TimeSeriesChart title="Latency (ms)" {ts} series={latencySeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
			<TimeSeriesChart title="Total Tokens" {ts} series={tokensSeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
			<TimeSeriesChart title="Prefix Cache Hit Rate (%)" {ts} series={cacheSeries} xMin={xMin} xMax={xMax} syncKey={SYNC} />
		</div>
	{:else}
		<div class="text-center text-text-muted py-8">No data for selected time range</div>
	{/if}
</div>
