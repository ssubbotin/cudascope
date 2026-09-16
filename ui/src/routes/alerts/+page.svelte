<script lang="ts">
	import { onDestroy, onMount } from 'svelte';
	import TimeRangePicker from '$lib/components/TimeRangePicker.svelte';
	import NodeSelector from '$lib/components/NodeSelector.svelte';
	import { alerts, nodes, selectedNode, fetchAlertHistory } from '$lib/stores/metrics';
	import type { AlertEvent } from '$lib/stores/metrics';
	import { alertName, alertValue, formatDuration, formatClock } from '$lib/utils/format';

	let selectedRange = $state('24h');
	let autoRefresh = $state(true);
	let history = $state<AlertEvent[]>([]);
	let loading = $state(false);
	let now = $state(Math.floor(Date.now() / 1000));

	async function loadHistory(range: string, silent = false) {
		selectedRange = range;
		if (!silent) loading = true;
		now = Math.floor(Date.now() / 1000);
		const node = $selectedNode === 'all' ? undefined : $selectedNode;
		history = await fetchAlertHistory(range, node);
		loading = false;
	}

	// Open events come from the live store, so the top of the page keeps up
	// with the state snapshots instead of waiting for the next fetch.
	let closed = $derived(history.filter((e) => e.ended_at));
	let open = $derived($alerts);

	function duration(e: AlertEvent): string {
		const end = e.ended_at ?? now;
		return formatDuration(Math.max(0, end - e.started_at));
	}

	function target(e: AlertEvent): string {
		const node = $nodes.length > 1 ? `${e.node_id} ` : '';
		return e.gpu_id === undefined ? `${node}node`.trim() : `${node}GPU ${e.gpu_id}`.trim();
	}

	// Subscribing explicitly rather than through $effect: loadHistory writes
	// state that an effect would then read back, which is how the vLLM page
	// once ended up re-fetching itself in a loop.
	let refreshInterval: ReturnType<typeof setInterval>;
	let unsubscribeNode: (() => void) | undefined;

	onMount(() => {
		loadHistory(selectedRange);

		let first = true;
		unsubscribeNode = selectedNode.subscribe(() => {
			if (first) {
				first = false;
				return;
			}
			loadHistory(selectedRange, true);
		});

		refreshInterval = setInterval(() => {
			now = Math.floor(Date.now() / 1000);
			if (autoRefresh) loadHistory(selectedRange, true);
		}, 30000);
	});

	onDestroy(() => {
		unsubscribeNode?.();
		clearInterval(refreshInterval);
	});
</script>

<svelte:head>
	<title>Alerts | CudaScope</title>
</svelte:head>

<div class="p-4 sm:p-6 space-y-6">
	<div class="flex flex-wrap items-center justify-between gap-3">
		<div>
			<h2 class="text-lg font-semibold text-text-primary">Alerts</h2>
			<p class="text-xs text-text-muted mt-0.5">
				Thresholds are evaluated on every collected sample, whether or not this page is open.
			</p>
		</div>
		<div class="flex items-center gap-2">
			<NodeSelector
				nodes={$nodes}
				selected={$selectedNode}
				onchange={(n) => selectedNode.set(n)}
			/>
			<TimeRangePicker
				selected={selectedRange}
				onchange={(r) => loadHistory(r)}
				{autoRefresh}
				onRefreshToggle={(v) => (autoRefresh = v)}
				onManualRefresh={() => loadHistory(selectedRange)}
			/>
		</div>
	</div>

	<section class="space-y-2">
		<h3 class="text-sm font-medium text-text-secondary">Open</h3>
		{#if open.length === 0}
			<div class="bg-bg-card border border-border rounded-xl p-6 text-center text-sm text-text-muted">
				Nothing is alerting right now.
			</div>
		{:else}
			<div class="bg-bg-card border border-red/40 rounded-xl divide-y divide-border">
				{#each open as event (event.id)}
					<div class="flex flex-wrap items-center gap-x-4 gap-y-1 p-4">
						<span class="text-sm font-medium text-red">{alertName(event.kind)}</span>
						<span class="text-sm text-text-primary">{target(event)}</span>
						<span class="text-xs text-text-muted">
							now {alertValue(event.kind, event.last_value)}, peak
							{alertValue(event.kind, event.peak_value)}, threshold
							{alertValue(event.kind, event.threshold)}
						</span>
						<span class="text-xs text-text-muted ml-auto">
							{formatClock(event.started_at)} · {duration(event)}
						</span>
					</div>
				{/each}
			</div>
		{/if}
	</section>

	<section class="space-y-2">
		<h3 class="text-sm font-medium text-text-secondary">
			Cleared
			{#if loading}<span class="text-xs text-text-muted">loading…</span>{/if}
		</h3>
		{#if closed.length === 0}
			<div class="bg-bg-card border border-border rounded-xl p-6 text-center text-sm text-text-muted">
				No alerts cleared in this range.
			</div>
		{:else}
			<div class="bg-bg-card border border-border rounded-xl overflow-x-auto">
				<table class="w-full text-sm">
					<thead class="text-xs text-text-muted border-b border-border">
						<tr>
							<th class="text-left font-medium px-4 py-2">Alert</th>
							<th class="text-left font-medium px-4 py-2">Target</th>
							<th class="text-right font-medium px-4 py-2">Peak</th>
							<th class="text-right font-medium px-4 py-2">Threshold</th>
							<th class="text-right font-medium px-4 py-2">Started</th>
							<th class="text-right font-medium px-4 py-2">Lasted</th>
						</tr>
					</thead>
					<tbody class="divide-y divide-border">
						{#each closed as event (event.id)}
							<tr class="hover:bg-bg-card-hover transition-colors">
								<td class="px-4 py-2 text-text-primary">{alertName(event.kind)}</td>
								<td class="px-4 py-2 text-text-secondary">{target(event)}</td>
								<td class="px-4 py-2 text-right text-text-secondary"
									>{alertValue(event.kind, event.peak_value)}</td
								>
								<td class="px-4 py-2 text-right text-text-muted"
									>{alertValue(event.kind, event.threshold)}</td
								>
								<td class="px-4 py-2 text-right text-text-muted">{formatClock(event.started_at)}</td>
								<td class="px-4 py-2 text-right text-text-secondary">{duration(event)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/if}
	</section>
</div>
