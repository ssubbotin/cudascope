<script lang="ts">
	import type { HostMetrics } from '$lib/stores/metrics';
	import ProgressBar from './ProgressBar.svelte';
	import { formatBytes, formatNetRate, utilColor } from '$lib/utils/format';

	interface Props {
		metrics: HostMetrics | null;
	}

	let { metrics }: Props = $props();
</script>

<div class="bg-bg-card border border-border rounded-xl p-5">
	<div class="flex items-center gap-2 mb-4">
		<svg class="w-4 h-4 text-text-muted" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
			<rect x="2" y="2" width="20" height="8" rx="2"/>
			<rect x="2" y="14" width="20" height="8" rx="2"/>
			<circle cx="6" cy="6" r="1" fill="currentColor"/>
			<circle cx="6" cy="18" r="1" fill="currentColor"/>
		</svg>
		<h3 class="text-sm font-medium text-text-primary">Host</h3>
		{#if metrics}
			<span class="text-xs text-text-muted ml-auto">{metrics.node_id}</span>
		{/if}
	</div>

	{#if metrics}
		<div class="space-y-3">
			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">CPU</span>
					<span class="font-mono" style="color: {utilColor(metrics.cpu_percent)}">{metrics.cpu_percent.toFixed(1)}%</span>
				</div>
				<ProgressBar value={metrics.cpu_percent} color={utilColor(metrics.cpu_percent)} />
			</div>

			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">Memory</span>
					<span class="font-mono text-text-secondary">{formatBytes(metrics.mem_used)} / {formatBytes(metrics.mem_total)}</span>
				</div>
				<ProgressBar value={metrics.mem_used} max={metrics.mem_total} color="var(--color-accent)" />
			</div>

			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">Disk</span>
					<span class="font-mono text-text-secondary">{formatBytes(metrics.disk_used)} / {formatBytes(metrics.disk_total)}</span>
				</div>
				<ProgressBar value={metrics.disk_used} max={metrics.disk_total} color="var(--color-yellow)" />
			</div>

			<div class="grid grid-cols-3 gap-2 pt-2 border-t border-border">
				<div class="text-center">
					<div class="text-xs text-text-muted">Load</div>
					<div class="text-sm font-mono text-text-secondary">{metrics.load_1m.toFixed(2)}</div>
				</div>
				<div class="text-center">
					<div class="text-xs text-text-muted">Net Rx</div>
					<div class="text-sm font-mono text-text-secondary">{formatNetRate(metrics.net_rx)}</div>
				</div>
				<div class="text-center">
					<div class="text-xs text-text-muted">Net Tx</div>
					<div class="text-sm font-mono text-text-secondary">{formatNetRate(metrics.net_tx)}</div>
				</div>
			</div>
		</div>
	{:else}
		<div class="text-sm text-text-muted py-4 text-center">Waiting for data...</div>
	{/if}
</div>
