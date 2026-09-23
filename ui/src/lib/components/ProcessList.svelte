<script lang="ts">
	import type { GPUProcess } from '$lib/stores/metrics';
	import { formatMiB } from '$lib/utils/format';

	interface Props {
		processes: GPUProcess[];
		showNode?: boolean;
	}

	let { processes, showNode = false }: Props = $props();

	let sorted = $derived([...processes].sort((a, b) => b.gpu_mem - a.gpu_mem));
</script>

{#if sorted.length > 0}
	<div class="bg-bg-card border border-border rounded-xl p-5">
		<h3 class="text-sm font-medium text-text-primary mb-3">GPU Processes</h3>
		<div class="overflow-x-auto">
			<table class="w-full text-sm">
				<thead>
					<tr class="text-xs text-text-muted border-b border-border">
						{#if showNode}
							<th class="text-left pb-2 pr-4 font-medium">Node</th>
						{/if}
						<th class="text-left pb-2 pr-4 font-medium">GPU</th>
						<th class="text-left pb-2 pr-4 font-medium">PID</th>
						<th class="text-left pb-2 font-medium">Process</th>
						<th class="text-right pb-2 font-medium">VRAM</th>
					</tr>
				</thead>
				<tbody>
					{#each sorted as proc}
						<tr class="border-b border-border/50 last:border-0 align-top">
							{#if showNode}
								<td class="py-1.5 pr-4 text-text-muted text-xs">{proc.node_id || 'local'}</td>
							{/if}
							<td class="py-1.5 pr-4 font-mono text-text-muted">{proc.gpu_id}</td>
							<td class="py-1.5 pr-4 font-mono text-text-secondary">{proc.pid}</td>
							<td class="py-1.5 pr-4 text-text-primary">
								{proc.name}
								{#if proc.cmdline}
									<!-- Two lines are enough to tell jobs apart; the rest is on hover. -->
									<div
										class="font-mono text-xs text-text-muted line-clamp-2 break-all"
										title={proc.cmdline}
									>
										{proc.cmdline}
									</div>
								{/if}
							</td>
							<td class="py-1.5 text-right font-mono text-accent whitespace-nowrap">{formatMiB(proc.gpu_mem)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
{/if}
