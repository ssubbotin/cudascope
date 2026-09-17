<script lang="ts">
	import type { OllamaMetrics, OllamaPoint } from '$lib/stores/metrics';
	import ProgressBar from './ProgressBar.svelte';
	import Sparkline from './Sparkline.svelte';
	import { formatBytes } from '$lib/utils/format';

	interface Props {
		metrics: OllamaMetrics | null;
		history: OllamaPoint[];
	}

	let { metrics, history }: Props = $props();

	let models = $derived(metrics?.models ?? []);
	// Gigabytes held on the card, over the same window the other cards plot.
	// The line is a staircase: it steps up on a load and back down when the
	// keep alive expires.
	let vramHistory = $derived(history.map((p) => p.vram_bytes / (1024 * 1024 * 1024)));
	let totalVram = $derived(models.reduce((sum, m) => sum + m.vram_bytes, 0));
	let totalSize = $derived(models.reduce((sum, m) => sum + m.size_bytes, 0));

	// A model with less in VRAM than its size has the rest in system memory,
	// which is the usual reason it answers slower than it did yesterday.
	function onGPU(size: number, vram: number): number {
		if (size <= 0) return 0;
		return (vram / size) * 100;
	}

	function fitColor(share: number): string {
		if (share >= 99.5) return 'var(--color-green)';
		if (share >= 50) return 'var(--color-yellow)';
		return 'var(--color-red)';
	}

	// Ollama unloads a model once its keep alive runs out.
	function expiresIn(unix: number | undefined, now: number): string {
		if (!unix) return '';
		const left = unix - now;
		if (left <= 0) return 'expiring';
		if (left < 60) return `${Math.round(left)}s`;
		if (left < 3600) return `${Math.round(left / 60)}m`;
		return `${(left / 3600).toFixed(1)}h`;
	}

	// Ticks once a second so the countdown moves without a new sample.
	let now = $state(Math.floor(Date.now() / 1000));
	$effect(() => {
		const id = setInterval(() => (now = Math.floor(Date.now() / 1000)), 1000);
		return () => clearInterval(id);
	});
</script>

<a
	href="/ollama"
	class="flex h-full flex-col bg-bg-card border border-border rounded-xl p-5 hover:bg-bg-card-hover hover:border-accent/40 transition-all duration-200 cursor-pointer"
>
	<div class="flex items-center gap-2 mb-4">
		<svg
			class="w-4 h-4 text-accent"
			viewBox="0 0 24 24"
			fill="none"
			stroke="currentColor"
			stroke-width="2"
		>
			<path d="M12 3c-1.7 0-3 1.3-3 3v1.2A4 4 0 0 0 6 11v4a4 4 0 0 0 4 4h4a4 4 0 0 0 4-4v-4a4 4 0 0 0-3-3.8V6c0-1.7-1.3-3-3-3z" />
			<circle cx="10" cy="13" r="1" fill="currentColor" />
			<circle cx="14" cy="13" r="1" fill="currentColor" />
		</svg>
		<h3 class="text-sm font-medium text-text-primary">Ollama</h3>
		{#if metrics?.version}
			<span class="ml-auto text-xs font-mono text-accent">{metrics.version}</span>
		{/if}
	</div>

	{#if !metrics}
		<div class="text-sm text-text-muted py-4 text-center">No ollama server configured</div>
	{:else}
		<!-- gap-3, never space-y-3: a margin on each row would outweigh the
		     summary's mt-auto and unpin it from the bottom. -->
		<div class="flex flex-1 flex-col gap-3">
			{#if models.length === 0}
				<div class="text-sm text-text-muted py-2">
					Nothing loaded. The next request pays for a model load.
				</div>
			{:else}
				{#each models.slice(0, 3) as model (model.name)}
					<div>
						<div class="flex justify-between text-xs mb-1 gap-2">
							<span class="text-text-muted truncate" title={model.name}>{model.name}</span>
							<span class="font-mono text-text-secondary whitespace-nowrap">
								{formatBytes(model.vram_bytes)} / {formatBytes(model.size_bytes)}
							</span>
						</div>
						<ProgressBar
							value={onGPU(model.size_bytes, model.vram_bytes)}
							color={fitColor(onGPU(model.size_bytes, model.vram_bytes))}
						/>
					</div>
				{/each}
				{#if models.length > 3}
					<div class="text-xs text-text-muted">and {models.length - 3} more</div>
				{/if}
			{/if}

			<div class="grid grid-cols-3 gap-2 pt-2 border-t border-border">
				<div class="text-center">
					<div class="text-xs text-text-muted">Loaded</div>
					<div class="text-sm font-mono text-text-secondary">{models.length}</div>
				</div>
				<div class="text-center">
					<div class="text-xs text-text-muted">On GPU</div>
					<div
						class="text-sm font-mono whitespace-nowrap"
						style="color: {fitColor(onGPU(totalSize, totalVram))}"
					>
						{models.length === 0 ? '--' : formatBytes(totalVram)}
					</div>
				</div>
				<div class="text-center">
					<div class="text-xs text-text-muted">Unloads in</div>
					<div class="text-sm font-mono text-text-secondary">
						{models.length === 0 ? '--' : expiresIn(models[0].expires_at, now) || '--'}
					</div>
				</div>
			</div>

			<!-- Plot. Pinned to the bottom with mt-auto, so the divider above it
			     lines up with the other cards' however much content sits above.
			     No upper bound: this is gigabytes, not a percentage. -->
			<div class="mt-auto pt-2 border-t border-border">
				<div class="text-xs text-text-muted mb-1">On GPU (GiB)</div>
				<Sparkline data={vramHistory} color="var(--color-green)" />
			</div>
		</div>
	{/if}
</a>
