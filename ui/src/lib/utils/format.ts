export function formatBytes(bytes: number): string {
	if (bytes === 0) return '0 B';
	const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
	const i = Math.floor(Math.log(bytes) / Math.log(1024));
	return (bytes / Math.pow(1024, i)).toFixed(1) + ' ' + units[i];
}

export function formatMiB(mib: number): string {
	if (mib >= 1024) return (mib / 1024).toFixed(1) + ' GiB';
	return mib.toFixed(0) + ' MiB';
}

export function formatWatts(w: number): string {
	return w.toFixed(0) + ' W';
}

export function formatPercent(v: number): string {
	return v.toFixed(1) + '%';
}

export function formatTemp(c: number): string {
	return c + '\u00B0C';
}

export function formatNetRate(bytesPerSec: number): string {
	if (bytesPerSec === 0) return '0 B/s';
	const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
	const i = Math.floor(Math.log(bytesPerSec) / Math.log(1000));
	return (bytesPerSec / Math.pow(1000, i)).toFixed(1) + ' ' + units[i];
}

export function tempColor(temp: number): string {
	if (temp < 50) return 'var(--color-green)';
	if (temp < 70) return 'var(--color-yellow)';
	if (temp < 85) return 'var(--color-orange)';
	return 'var(--color-red)';
}

export function utilColor(pct: number): string {
	if (pct < 50) return 'var(--color-green)';
	if (pct < 80) return 'var(--color-yellow)';
	return 'var(--color-red)';
}

const alertNames: Record<string, string> = {
	temperature: 'Temperature',
	gpu_util: 'GPU utilization',
	mem_util: 'Memory utilization',
	node_silent: 'Node silent',
	collector_stalled: 'Collection stalled',
	xid: 'Driver fault (Xid)'
};

export function alertName(kind: string): string {
	return alertNames[kind] ?? kind;
}

// Silence alerts carry an age in seconds where the others carry a reading.
export function alertValue(kind: string, value: number): string {
	if (kind === 'node_silent' || kind === 'collector_stalled') {
		return formatDuration(value);
	}
	if (kind === 'temperature') return formatTemp(Math.round(value));
	return value.toFixed(0) + '%';
}

// alertSummary states an event in one line. Each kind means something
// different by its numbers: a threshold breach carries a reading, a silence
// carries an age, and a driver fault carries a code and a count.
export function alertSummary(
	kind: string,
	last: number,
	peak: number,
	threshold: number
): string {
	if (kind === 'xid') {
		return `code ${last}, ${peak} error${peak === 1 ? '' : 's'}`;
	}
	if (kind === 'node_silent' || kind === 'collector_stalled') {
		return `silent for ${formatDuration(last)}, threshold ${formatDuration(threshold)}`;
	}
	return `now ${alertValue(kind, last)}, peak ${alertValue(kind, peak)}, threshold ${alertValue(kind, threshold)}`;
}

// A driver fault has no threshold to cross, so printing one reads as a
// measurement that was never taken.
export function alertThreshold(kind: string, threshold: number): string {
	if (kind === 'xid') return '\u2014';
	return alertValue(kind, threshold);
}

export function alertPeak(kind: string, peak: number): string {
	if (kind === 'xid') return `${peak} error${peak === 1 ? '' : 's'}`;
	return alertValue(kind, peak);
}

export function formatDuration(seconds: number): string {
	if (seconds < 60) return Math.round(seconds) + 's';
	if (seconds < 3600) return Math.round(seconds / 60) + 'm';
	if (seconds < 86400) return (seconds / 3600).toFixed(1) + 'h';
	return (seconds / 86400).toFixed(1) + 'd';
}

export function formatClock(unix: number): string {
	return new Date(unix * 1000).toLocaleString();
}

// NVML clock throttle reasons, by bit. Idle and the two "someone set the
// clocks" reasons are states rather than slowdowns, so they are named but
// not counted as throttling.
const throttleBits: { bit: number; name: string; slowdown: boolean }[] = [
	{ bit: 1, name: 'Idle', slowdown: false },
	{ bit: 2, name: 'App clock limit', slowdown: false },
	{ bit: 4, name: 'Power cap', slowdown: true },
	{ bit: 8, name: 'Hardware slowdown', slowdown: true },
	{ bit: 16, name: 'Sync boost', slowdown: true },
	{ bit: 32, name: 'Thermal (software)', slowdown: true },
	{ bit: 64, name: 'Thermal (hardware)', slowdown: true },
	{ bit: 128, name: 'Power brake', slowdown: true },
	{ bit: 256, name: 'Display clocks', slowdown: false }
];

export function throttleReasonNames(mask: number): string[] {
	return throttleBits.filter((r) => (mask & r.bit) !== 0).map((r) => r.name);
}

export function isThrottled(mask: number): boolean {
	return throttleBits.some((r) => r.slowdown && (mask & r.bit) !== 0);
}
