<script lang="ts">
	import '../app.css';
	import Navbar from '$lib/components/Navbar.svelte';
	import { connect, disconnect } from '$lib/stores/websocket';
	import { fetchStatus } from '$lib/stores/metrics';
	import { initTheme } from '$lib/stores/theme';
	import { onMount, onDestroy } from 'svelte';

	let { children } = $props();

	onMount(() => {
		initTheme();
		fetchStatus();
		connect();
	});

	onDestroy(() => {
		disconnect();
	});
</script>

<div class="min-h-screen bg-bg-primary transition-colors duration-200">
	<Navbar />
	<main class="max-w-7xl mx-auto px-4 sm:px-6 py-6">
		{@render children()}
	</main>
</div>
