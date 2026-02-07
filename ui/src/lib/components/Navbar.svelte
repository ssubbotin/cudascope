<script lang="ts">
	import { connected } from '$lib/stores/websocket';
</script>

<nav class="border-b border-border px-6 py-3 flex items-center justify-between bg-bg-secondary/50 backdrop-blur-sm sticky top-0 z-50">
	<div class="flex items-center gap-3">
		<svg class="w-7 h-7 text-accent" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
			<rect x="2" y="3" width="20" height="18" rx="2"/>
			<path d="M7 8h2v8H7zM11 6h2v12h-2zM15 10h2v4h-2z"/>
		</svg>
		<h1 class="text-lg font-semibold text-text-primary tracking-tight">CudaScope</h1>
	</div>

	<div class="flex items-center gap-4">
		<div class="flex items-center gap-2 text-sm">
			<div class="w-2 h-2 rounded-full {$connected ? 'bg-green' : 'bg-red'} animate-pulse"></div>
			<span class="text-text-muted">{$connected ? 'Live' : 'Disconnected'}</span>
		</div>
	</div>
</nav>
