<script lang="ts">
	import type { GPUDevice, GPUMetrics } from '$lib/stores/metrics';
	import { alerts, gpuKey } from '$lib/stores/metrics';
	import ProgressBar from './ProgressBar.svelte';
	import Sparkline from './Sparkline.svelte';
	import { formatMiB, formatWatts, formatTemp, utilColor, tempColor, alertName, alertValue, throttleReasonNames, isThrottled } from '$lib/utils/format';

	interface Props {
		device: GPUDevice;
		metrics: GPUMetrics | undefined;
		history: GPUMetrics[];
		showNode?: boolean;
	}

	let { device, metrics, history, showNode = false }: Props = $props();

	let utilHistory = $derived(history.map((m) => m.gpu_util));
	let detailHref = $derived(
		showNode ? `/gpu/${device.id}?node=${device.node_id}` : `/gpu/${device.id}`
	);
	let gpuAlerts = $derived(
		$alerts.filter((a) => a.gpu_id === device.id && a.node_id === (device.node_id || 'local'))
	);
	let hasAlert = $derived(gpuAlerts.length > 0);
	let throttled = $derived(!!metrics && isThrottled(metrics.throttle_reasons));
	let throttleReasons = $derived(metrics ? throttleReasonNames(metrics.throttle_reasons) : []);
</script>

<!-- The link is the card, as on the host and vLLM cards. With the card in a
     wrapper instead, this one alone did not stretch to the height of its row
     and sat lower than its neighbours. -->
<a
	href={detailHref}
	class="flex h-full flex-col bg-bg-card border rounded-xl p-5 hover:bg-bg-card-hover transition-all duration-200 cursor-pointer {hasAlert
		? 'border-red/60'
		: 'border-border hover:border-accent/40'}"
>
	<div class="flex flex-1 flex-col">
		<div class="flex items-start justify-between gap-2 mb-4">
			<div class="min-w-0">
				<h3 class="text-sm font-medium text-text-primary">
					{#if showNode}
						<span class="text-text-muted">{device.node_id}:</span>
					{/if}
					GPU {device.id}
				</h3>
				<p class="text-xs text-text-muted mt-0.5 line-clamp-2 min-h-[2rem]">{device.name}</p>
			</div>
			<div class="flex shrink-0 items-center gap-1.5">
				{#if hasAlert}
					<span class="text-xs px-2 py-0.5 rounded-full bg-red/10 text-red border border-red/20" title={gpuAlerts.map((a) => `${alertName(a.kind)}: ${alertValue(a.kind, a.last_value)} (peak ${alertValue(a.kind, a.peak_value)}, threshold ${alertValue(a.kind, a.threshold)})`).join(', ')}>
						<svg class="w-3 h-3 inline-block -mt-0.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
							<path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z"/>
							<line x1="12" y1="9" x2="12" y2="13"/>
							<line x1="12" y1="17" x2="12.01" y2="17"/>
						</svg>
					</span>
				{/if}
				{#if throttled}
					<span
						class="text-xs px-2 py-0.5 rounded-full bg-orange/10 text-orange border border-orange/20"
						title="Throttling: {throttleReasons.join(', ')}"
					>
						throttled
					</span>
				{/if}
				{#if metrics}
					<span class="text-xs px-2 py-0.5 rounded-full bg-green/10 text-green border border-green/20">P{metrics.pstate}</span>
				{/if}
			</div>
		</div>

		{#if metrics}
			<!-- Utilization -->
			<!-- gap-3 rather than space-y-3: the latter sets a margin on every
			     row after the first and outweighs the plot's mt-auto, which is
			     what pins the plot to the bottom of the card. -->
			<div class="flex flex-1 flex-col gap-3">
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

				<!-- Sparkline. Always rendered: hiding it while the buffer fills
				     made the card change height a second after it appeared, and
				     left it shorter than its neighbours until then. -->
				<div class="mt-auto pt-2 border-t border-border">
					<div class="text-xs text-text-muted mb-1">Utilization</div>
					<Sparkline data={utilHistory} min={0} max={100} color={utilColor(metrics.gpu_util)} />
				</div>
			</div>
		{:else}
			<div class="text-sm text-text-muted py-4 text-center">Waiting for data...</div>
		{/if}
	</div>
</a>
