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
