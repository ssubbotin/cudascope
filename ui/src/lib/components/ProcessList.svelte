<script lang="ts">
	import type { GPUProcess } from '$lib/stores/metrics';
	import { formatMiB } from '$lib/utils/format';

	interface Props {
		processes: GPUProcess[];
	}

	let { processes }: Props = $props();

	let sorted = $derived([...processes].sort((a, b) => b.gpu_mem - a.gpu_mem));
</script>

{#if sorted.length > 0}
	<div class="bg-bg-card border border-border rounded-xl p-5">
		<h3 class="text-sm font-medium text-text-primary mb-3">GPU Processes</h3>
		<div class="overflow-x-auto">
			<table class="w-full text-sm">
				<thead>
					<tr class="text-xs text-text-muted border-b border-border">
						<th class="text-left pb-2 font-medium">GPU</th>
						<th class="text-left pb-2 font-medium">PID</th>
						<th class="text-left pb-2 font-medium">Process</th>
						<th class="text-right pb-2 font-medium">VRAM</th>
					</tr>
				</thead>
				<tbody>
					{#each sorted as proc}
						<tr class="border-b border-border/50 last:border-0">
							<td class="py-1.5 font-mono text-text-muted">{proc.gpu_id}</td>
							<td class="py-1.5 font-mono text-text-secondary">{proc.pid}</td>
							<td class="py-1.5 text-text-primary">{proc.name}</td>
							<td class="py-1.5 text-right font-mono text-accent">{formatMiB(proc.gpu_mem)}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</div>
{/if}
