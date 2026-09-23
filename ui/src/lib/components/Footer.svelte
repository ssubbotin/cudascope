<script lang="ts">
	import { build } from '$lib/stores/metrics';

	const repo = 'https://github.com/ssubbotin/cudascope';

	const pad = (n: number) => String(n).padStart(2, '0');

	// The build time in the viewer's own zone, like every chart on the page,
	// and spelled out by hand: the browser's locale would put a month name in
	// its own language into an English line. The exact UTC stamp is on hover.
	let builtAt = $derived.by(() => {
		if (!$build?.built_at) return '';
		const d = new Date($build.built_at);
		if (isNaN(d.getTime())) return '';
		return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
	});
</script>

<footer class="w-full max-w-7xl mx-auto px-4 sm:px-6 pb-6 pt-2">
	<div class="border-t border-border pt-4 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-text-muted">
		<span>CudaScope</span>
		{#if $build}
			{#if $build.revision}
				<a
					href="{repo}/commit/{$build.revision}"
					class="font-mono hover:text-text-primary transition-colors"
					title="Commit {$build.revision}">{$build.version}</a
				>
			{:else}
				<span class="font-mono">{$build.version}</span>
			{/if}
			{#if builtAt}
				<span aria-hidden="true">·</span>
				<span title={$build.built_at}>built {builtAt}</span>
			{/if}
		{/if}
		<span aria-hidden="true">·</span>
		<a href={repo} class="hover:text-text-primary transition-colors" target="_blank" rel="noopener noreferrer"
			>GitHub</a
		>
	</div>
</footer>
