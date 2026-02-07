<script lang="ts">
	import { connected } from '$lib/stores/websocket';
	import { nodes } from '$lib/stores/metrics';
	import { themePreference, cycleTheme } from '$lib/stores/theme';

	const themeIcons: Record<string, string> = {
		dark: 'M21 12.79A9 9 0 1111.21 3 7 7 0 0021 12.79z',
		light: 'M12 3v1m0 16v1m-8-9H3m18 0h-1m-2.636-6.364l-.707.707M6.343 17.657l-.707.707m0-12.728l.707.707m11.314 11.314l.707.707M12 8a4 4 0 100 8 4 4 0 000-8z',
		system: 'M9.75 17L9 20l-1 1h8l-1-1-.75-3M3 13h18M5 17h14a2 2 0 002-2V5a2 2 0 00-2-2H5a2 2 0 00-2 2v10a2 2 0 002 2z'
	};

	let iconPath = $derived(themeIcons[$themePreference] || themeIcons.dark);
	let onlineNodes = $derived($nodes.filter((n) => n.online).length);
	let totalNodes = $derived($nodes.length);
</script>

<nav class="border-b border-border px-4 sm:px-6 py-3 flex items-center justify-between bg-bg-secondary/50 backdrop-blur-sm sticky top-0 z-50">
	<a href="/" class="flex items-center gap-3 hover:opacity-80 transition-opacity">
		<svg class="w-7 h-7 text-accent" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
			<rect x="2" y="3" width="20" height="18" rx="2"/>
			<path d="M7 8h2v8H7zM11 6h2v12h-2zM15 10h2v4h-2z"/>
		</svg>
		<h1 class="text-lg font-semibold text-text-primary tracking-tight">CudaScope</h1>
	</a>

	<div class="flex items-center gap-3">
		{#if totalNodes > 1}
			<div class="flex items-center gap-1.5 text-xs text-text-muted">
				<svg class="w-3.5 h-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
					<rect x="2" y="2" width="20" height="8" rx="2"/>
					<rect x="2" y="14" width="20" height="8" rx="2"/>
					<circle cx="6" cy="6" r="1" fill="currentColor"/>
					<circle cx="6" cy="18" r="1" fill="currentColor"/>
				</svg>
				<span>{onlineNodes}/{totalNodes} nodes</span>
			</div>
		{/if}

		<div class="flex items-center gap-2 text-sm">
			<div class="w-2 h-2 rounded-full {$connected ? 'bg-green' : 'bg-red'} animate-pulse"></div>
			<span class="text-text-muted hidden sm:inline">{$connected ? 'Live' : 'Disconnected'}</span>
		</div>

		<button
			onclick={cycleTheme}
			class="p-1.5 rounded-lg text-text-muted hover:text-text-primary hover:bg-bg-card transition-colors"
			title="Theme: {$themePreference}"
		>
			<svg class="w-4 h-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
				<path d={iconPath} />
			</svg>
		</button>
	</div>
</nav>
