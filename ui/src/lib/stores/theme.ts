import { writable } from 'svelte/store';

export type Theme = 'dark' | 'light' | 'system';

function getInitialTheme(): Theme {
	if (typeof localStorage === 'undefined') return 'system';
	return (localStorage.getItem('cudascope-theme') as Theme) || 'system';
}

function resolveTheme(pref: Theme): 'dark' | 'light' {
	if (pref === 'system') {
		if (typeof window === 'undefined') return 'dark';
		return window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
	}
	return pref;
}

export const themePreference = writable<Theme>(getInitialTheme());
export const resolvedTheme = writable<'dark' | 'light'>('dark');

export function applyTheme(pref: Theme) {
	themePreference.set(pref);
	if (typeof localStorage !== 'undefined') {
		localStorage.setItem('cudascope-theme', pref);
	}
	const resolved = resolveTheme(pref);
	resolvedTheme.set(resolved);
	if (typeof document !== 'undefined') {
		document.documentElement.classList.toggle('light', resolved === 'light');
	}
}

export function initTheme() {
	const pref = getInitialTheme();
	applyTheme(pref);

	// Listen for system preference changes
	if (typeof window !== 'undefined') {
		window.matchMedia('(prefers-color-scheme: light)').addEventListener('change', () => {
			const current = getInitialTheme();
			if (current === 'system') applyTheme('system');
		});
	}
}

export function cycleTheme() {
	let current: Theme = 'dark';
	themePreference.subscribe((v) => (current = v))();
	const order: Theme[] = ['dark', 'light', 'system'];
	const next = order[(order.indexOf(current) + 1) % order.length];
	applyTheme(next);
}
