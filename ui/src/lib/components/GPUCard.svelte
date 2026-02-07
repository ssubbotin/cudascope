<script lang="ts">
	import type { GPUDevice, GPUMetrics } from '$lib/stores/metrics';
	import ProgressBar from './ProgressBar.svelte';
	import Sparkline from './Sparkline.svelte';
	import { formatMiB, formatWatts, formatTemp, utilColor, tempColor } from '$lib/utils/format';

	interface Props {
		device: GPUDevice;
		metrics: GPUMetrics | undefined;
		history: GPUMetrics[];
	}

	let { device, metrics, history }: Props = $props();

	let utilHistory = $derived(history.map((m) => m.gpu_util));
	let memHistory = $derived(history.map((m) => m.mem_used));
</script>

<a href="/gpu/{device.id}" class="block">
	<div class="bg-bg-card border border-border rounded-xl p-5 hover:border-accent/40 hover:bg-bg-card-hover transition-all duration-200 cursor-pointer">
		<div class="flex items-center justify-between mb-4">
			<div>
				<h3 class="text-sm font-medium text-text-primary">GPU {device.id}</h3>
				<p class="text-xs text-text-muted mt-0.5">{device.name}</p>
			</div>
			{#if metrics}
				<span class="text-xs px-2 py-0.5 rounded-full bg-green/10 text-green border border-green/20">P{metrics.pstate}</span>
			{/if}
		</div>

		{#if metrics}
			<!-- Utilization -->
			<div class="space-y-3">
				<div>
					<div class="flex justify-between text-xs mb-1">
						<span class="text-text-muted">GPU</span>
						<span class="font-mono" style="color: {utilColor(metrics.gpu_util)}">{metrics.gpu_util.toFixed(0)}%</span>
					</div>
					<ProgressBar value={metrics.gpu_util} color={utilColor(metrics.gpu_util)} />
				</div>

				<div>
					<div class="flex justify-between text-xs mb-1">
						<span class="text-text-muted">VRAM</span>
						<span class="font-mono text-text-secondary">{formatMiB(metrics.mem_used)} / {formatMiB(device.mem_total)}</span>
					</div>
					<ProgressBar value={metrics.mem_used} max={device.mem_total} color="var(--color-accent)" />
				</div>

				<!-- Stats row -->
				<div class="grid grid-cols-3 gap-2 pt-2 border-t border-border">
					<div class="text-center">
						<div class="text-xs text-text-muted">Temp</div>
						<div class="text-sm font-mono" style="color: {tempColor(metrics.temperature)}">{formatTemp(metrics.temperature)}</div>
					</div>
					<div class="text-center">
						<div class="text-xs text-text-muted">Fan</div>
						<div class="text-sm font-mono text-text-secondary">{metrics.fan_speed}%</div>
					</div>
					<div class="text-center">
						<div class="text-xs text-text-muted">Power</div>
						<div class="text-sm font-mono text-text-secondary">{formatWatts(metrics.power_draw)}</div>
					</div>
				</div>

				<!-- Sparkline -->
				{#if utilHistory.length > 1}
					<div class="pt-2 border-t border-border">
						<div class="text-xs text-text-muted mb-1">Utilization</div>
						<Sparkline data={utilHistory} color={utilColor(metrics.gpu_util)} />
					</div>
				{/if}
			</div>
		{:else}
			<div class="text-sm text-text-muted py-4 text-center">Waiting for data...</div>
		{/if}
	</div>
</a>
