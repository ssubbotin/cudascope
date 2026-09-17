<script lang="ts">
	import { page } from '$app/stores';
	import { onDestroy } from 'svelte';
	import { connected } from '$lib/stores/websocket';
	import TimeSeriesChart from '$lib/components/TimeSeriesChart.svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import ProgressBar from '$lib/components/ProgressBar.svelte';
	import {
		latestOllama,
		nodes,
		fetchOllamaHistory,
		parseRangeSeconds
	} from '$lib/stores/metrics';
	import type { OllamaPoint } from '$lib/stores/metrics';
	import { formatBytes } from '$lib/utils/format';

	let nodeId = $derived($page.url.searchParams.get('node') || 'local');
	let metrics = $derived($latestOllama);
	let hostname = $derived($nodes.find((n) => n.node_id === nodeId)?.hostname ?? nodeId);
	let models = $derived(metrics?.models ?? []);

	let selectedRange = $state('1h');
	let autoRefresh = $state(true);
	let historyData = $state<OllamaPoint[]>([]);
	let loading = $state(false);

	let xMax = $state(Math.floor(Date.now() / 1000));
	let xMin = $derived(xMax - parseRangeSeconds(selectedRange));

	async function loadHistory(range: string, silent = false) {
		selectedRange = range;
		if (!silent) loading = true;
		xMax = Math.floor(Date.now() / 1000);
		historyData = await fetchOllamaHistory(range, nodeId);
		loading = false;
		historyLoaded = true;
		setupRefresh();
	}

	// The tiles above follow the stream through the store, which updates
	// latestOllama on every reading. The charts follow the clock: a reading
	// arrives every ten seconds and mostly says the same thing, so redrawing
	// the history on each one would buy nothing.

	let wasConnected = true;
	let historyLoaded = false;
	const stopWatchingConnection = connected.subscribe((isConnected) => {
		if (isConnected && !wasConnected && historyLoaded) loadHistory(selectedRange, true);
		wasConnected = isConnected;
	});

	let ts = $derived(historyData.map((p) => p.ts));
	const SYNC = 'ollama-detail';

	let memorySeries = $derived([
		{
			label: 'On GPU (GiB)',
			color: '#4ade80',
			data: historyData.map((p) => p.vram_bytes / (1024 * 1024 * 1024))
		},
		{
			label: 'Total (GiB)',
			color: '#38bdf8',
			data: historyData.map((p) => p.size_bytes / (1024 * 1024 * 1024))
		}
	]);

	let loadedSeries = $derived([
		{ label: 'Models', color: '#a78bfa', data: historyData.map((p) => p.loaded) }
	]);

	// Which model was loaded when, read off the same ticks the charts use.
	let timeline = $derived.by(() => {
		const out: { from: number; to: number; models: string }[] = [];
		for (const p of historyData) {
			const last = out[out.length - 1];
			if (last && last.models === p.models) {
				last.to = p.ts;
				continue;
			}
			out.push({ from: p.ts, to: p.ts, models: p.models });
		}
		return out.reverse();
	});

	function span(from: number, to: number): string {
		const seconds = Math.max(to - from, 0);
		if (seconds < 60) return `${seconds}s`;
		if (seconds < 3600) return `${Math.round(seconds / 60)}m`;
		return `${(seconds / 3600).toFixed(1)}h`;
	}

	function clock(unix: number): string {
		return new Date(unix * 1000).toLocaleTimeString();
	}

	function onGPU(size: number, vram: number): number {
		if (size <= 0) return 0;
		return (vram / size) * 100;
	}

	function fitColor(share: number): string {
		if (share >= 99.5) return 'var(--color-green)';
		if (share >= 50) return 'var(--color-yellow)';
		return 'var(--color-red)';
	}

	let refreshInterval: ReturnType<typeof setInterval>;
	let initialized = $state(false);

	function setupRefresh() {
		clearInterval(refreshInterval);
		if (!autoRefresh) return;
		refreshInterval = setInterval(() => loadHistory(selectedRange, true), 30000);
	}

	$effect(() => {
		if (!initialized) {
			initialized = true;
			loadHistory(selectedRange);
		}
	});

	onDestroy(() => {
		clearInterval(refreshInterval);
		stopWatchingConnection();
	});

	function handleRefreshToggle(enabled: boolean) {
		autoRefresh = enabled;
		setupRefresh();
	}
</script>

<svelte:head>
	<title>Ollama | CudaScope</title>
</svelte:head>

<div class="p-4 sm:p-6 space-y-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div class="flex items-center gap-3">
			<a href="/" class="text-sm text-text-muted hover:text-text-primary transition-colors">
				← Back
			</a>
			<div>
				<h2 class="text-lg font-semibold text-text-primary">Ollama</h2>
				<p class="text-xs text-text-muted mt-0.5">
					{hostname}{metrics?.version ? ` · ${metrics.version}` : ''}
				</p>
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

	<div class="bg-bg-card border border-border rounded-xl p-5 space-y-4">
		<h3 class="text-xs font-medium text-text-muted">Loaded now</h3>
		{#if models.length === 0}
			<p class="text-sm text-text-muted">
				Nothing loaded. The next request pays for a model load.
			</p>
		{:else}
			{#each models as model (model.name)}
				<div>
					<div class="flex justify-between text-xs mb-1 gap-2">
						<span class="text-text-primary font-medium">{model.name}</span>
						<span class="font-mono text-text-secondary whitespace-nowrap">
							{formatBytes(model.vram_bytes)} on GPU / {formatBytes(model.size_bytes)} total
						</span>
					</div>
					<ProgressBar
						value={onGPU(model.size_bytes, model.vram_bytes)}
						color={fitColor(onGPU(model.size_bytes, model.vram_bytes))}
					/>
					{#if model.vram_bytes < model.size_bytes}
						<p class="text-xs text-yellow mt-1">
							{formatBytes(model.size_bytes - model.vram_bytes)} of this model is in system memory,
							which is slower than the card.
						</p>
					{/if}
					{#if model.context_length}
						<p class="text-xs text-text-muted mt-1">
							context {model.context_length.toLocaleString()} tokens
						</p>
					{/if}
				</div>
			{/each}
		{/if}
	</div>

	{#if !loading && ts.length >= 2}
		<div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">Memory held (GiB)</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={memorySeries}
					yMin={0}
					yLabel="GiB"
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>

			<div class="bg-bg-card border border-border rounded-xl p-5">
				<h3 class="text-xs font-medium text-text-muted mb-3">Models loaded</h3>
				<TimeSeriesChart
					timestamps={ts}
					series={loadedSeries}
					yMin={0}
					syncKey={SYNC}
					{xMin}
					{xMax}
				/>
			</div>
		</div>

		<div class="bg-bg-card border border-border rounded-xl p-5">
			<h3 class="text-xs font-medium text-text-muted mb-3">What was loaded</h3>
			<div class="overflow-x-auto">
				<table class="w-full text-sm">
					<thead class="text-xs text-text-muted">
						<tr class="border-b border-border">
							<th class="text-left font-medium px-3 py-2">Models</th>
							<th class="text-right font-medium px-3 py-2">From</th>
							<th class="text-right font-medium px-3 py-2">To</th>
							<th class="text-right font-medium px-3 py-2">Held</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-border">
						{#each timeline.slice(0, 20) as row (row.from)}
							<tr>
								<td class="px-3 py-2 text-text-primary">{row.models || 'nothing loaded'}</td>
								<td class="px-3 py-2 text-right text-text-muted">{clock(row.from)}</td>
								<td class="px-3 py-2 text-right text-text-muted">{clock(row.to)}</td>
								<td class="px-3 py-2 text-right text-text-secondary">{span(row.from, row.to)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{:else if loading}
		<div class="text-sm text-text-muted text-center py-12">Loading…</div>
	{:else}
		<div class="text-sm text-text-muted text-center py-12">
			Not enough history for this range. Ollama readings are kept for as long as the raw samples
			are.
		</div>
	{/if}
</div>
