<script lang="ts">
	import type { VLLMMetrics } from '$lib/stores/metrics';
	import ProgressBar from './ProgressBar.svelte';
	import Sparkline from './Sparkline.svelte';
	import { utilColor } from '$lib/utils/format';

	interface Props {
		metrics: VLLMMetrics | null;
		history: VLLMMetrics[];
	}

	let { metrics, history }: Props = $props();

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

	let throughputData = $derived(history.map(m => m.token_throughput));
</script>

<a href="/vllm" class="block bg-bg-card border border-border rounded-xl p-5 hover:bg-bg-card-hover hover:border-accent/40 transition-all duration-200 cursor-pointer">
	<div class="flex items-center gap-2 mb-4">
		<svg class="w-4 h-4 text-accent" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
			<path d="M12 2L2 7l10 5 10-5-10-5z"/>
			<path d="M2 17l10 5 10-5"/>
			<path d="M2 12l10 5 10-5"/>
		</svg>
		<h3 class="text-sm font-medium text-text-primary">vLLM</h3>
		{#if metrics}
			<span class="ml-auto text-xs font-mono text-accent">{metrics.model_name || 'unknown'}</span>
		{/if}
	</div>

	{#if metrics}
		<div class="space-y-3">
			<!-- Token throughput -->
			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">Throughput</span>
					<span class="font-mono text-lg font-bold" style="color: {metrics.token_throughput > 50 ? 'var(--color-green)' : metrics.token_throughput > 10 ? 'var(--color-yellow)' : 'var(--color-red)'}">
						{formatTps(metrics.token_throughput)} tok/s
					</span>
				</div>
				{#if throughputData.length > 1}
					<Sparkline data={throughputData} height={28} color="var(--color-accent)" />
				{/if}
			</div>

			<!-- Active requests -->
			<div class="grid grid-cols-2 gap-3">
				<div>
					<div class="text-xs text-text-muted mb-1">Running</div>
					<div class="font-mono text-lg text-text-primary">{metrics.requests_running}</div>
				</div>
				<div>
					<div class="text-xs text-text-muted mb-1">Waiting</div>
					<div class="font-mono text-lg" style="color: {metrics.requests_waiting > 0 ? 'var(--color-yellow)' : 'var(--color-text-secondary)'}">
						{metrics.requests_waiting}
					</div>
				</div>
			</div>

			<!-- KV Cache -->
			<div>
				<div class="flex justify-between text-xs mb-1">
					<span class="text-text-muted">KV Cache</span>
					<span class="font-mono" style="color: {utilColor(metrics.kv_cache_usage * 100)}">
						{(metrics.kv_cache_usage * 100).toFixed(1)}%
					</span>
				</div>
				<ProgressBar value={metrics.kv_cache_usage * 100} color={utilColor(metrics.kv_cache_usage * 100)} />
			</div>

			<!-- Latency -->
			<div class="grid grid-cols-2 gap-3">
				<div>
					<div class="text-xs text-text-muted mb-1">TTFT</div>
					<div class="font-mono text-sm text-text-secondary">{formatMs(metrics.ttft_avg)}</div>
				</div>
				<div>
					<div class="text-xs text-text-muted mb-1">per token</div>
					<div class="font-mono text-sm text-text-secondary">{formatMs(metrics.tpot_avg)}</div>
				</div>
			</div>

			<!-- Prefix cache -->
			{#if metrics.prefix_cache_hit_rate > 0}
				<div>
					<div class="flex justify-between text-xs mb-1">
						<span class="text-text-muted">Prefix Cache Hit</span>
						<span class="font-mono text-text-secondary">{(metrics.prefix_cache_hit_rate * 100).toFixed(1)}%</span>
					</div>
					<ProgressBar value={metrics.prefix_cache_hit_rate * 100} color="var(--color-accent)" />
				</div>
			{/if}
		</div>
	{:else}
		<div class="text-sm text-text-muted py-4 text-center">
			No vLLM endpoint configured
		</div>
	{/if}
</a>
