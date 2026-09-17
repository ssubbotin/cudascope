import { writable, derived } from 'svelte/store';
import { onMessage } from './websocket';

export interface GPUDevice {
	node_id: string;
	id: number;
	uuid: string;
	name: string;
	mem_total: number;
	driver_ver: string;
	ecc_supported: boolean;
	throttle_supported: boolean;
}

export interface GPUMetrics {
	node_id?: string;
	ts: number;
	gpu_id: number;
	gpu_util: number;
	mem_util: number;
	mem_used: number;
	temperature: number;
	fan_speed: number;
	power_draw: number;
	power_limit: number;
	clock_gfx: number;
	clock_mem: number;
	pcie_tx: number;
	pcie_rx: number;
	pstate: number;
	encoder_util: number;
	decoder_util: number;
	throttle_reasons: number;
	ecc_corrected: number;
	ecc_uncorrected: number;
}

export interface HostMetrics {
	ts: number;
	node_id: string;
	cpu_percent: number;
	mem_used: number;
	mem_total: number;
	disk_used: number;
	disk_total: number;
	net_rx: number;
	net_tx: number;
	load_1m: number;
	load_5m: number;
	load_15m: number;
}

export interface GPUProcess {
	node_id?: string;
	ts: number;
	gpu_id: number;
	pid: number;
	name: string;
	gpu_mem: number;
}

export interface Node {
	node_id: string;
	hostname: string;
	gpu_count: number;
	first_seen: number;
	last_seen: number;
	online: boolean;
}

export interface VLLMMetrics {
	ts: number;
	node_id: string;
	model_name: string;
	requests_running: number;
	requests_waiting: number;
	kv_cache_usage: number;
	generation_tokens_total: number;
	prompt_tokens_total: number;
	ttft_avg: number;
	tpot_avg: number;
	token_throughput: number;
	prefix_cache_hit_rate: number;
	num_preemptions: number;
}

// AlertKind names what an event is about. The first three carry a measured
// value; the last two carry the age of the newest data in seconds.
export type AlertKind =
	| 'temperature'
	| 'gpu_util'
	| 'mem_util'
	| 'node_silent'
	| 'collector_stalled';

// AlertEvent is one alert from the moment it opened to the moment it
// cleared. Open events have no ended_at.
export interface AlertEvent {
	id: number;
	node_id: string;
	gpu_id?: number;
	kind: AlertKind;
	threshold: number;
	started_at: number;
	ended_at?: number;
	peak_value: number;
	last_value: number;
}

// Helper: create a composite key for multi-node GPU identification
export function gpuKey(nodeId: string | undefined, gpuId: number): string {
	return `${nodeId || 'local'}:${gpuId}`;
}

// Stores
export const nodes = writable<Node[]>([]);
export const selectedNode = writable<string>('all');
export const devices = writable<GPUDevice[]>([]);
export const latestGPU = writable<GPUMetrics[]>([]);
export const latestHosts = writable<Map<string, HostMetrics>>(new Map());
export const processes = writable<GPUProcess[]>([]);
export const alerts = writable<AlertEvent[]>([]);
export const latestVLLM = writable<VLLMMetrics | null>(null);
export const vllmHistory = writable<VLLMMetrics[]>([]);

// Sparkline buffers hold a stretch of time, not a count of samples.
//
// Counting samples meant the GPU plot covered two minutes (one sample a
// second) while the vLLM plot beside it covered ten (one every five), so the
// same busy period appeared in different places on two charts standing side
// by side, and an idle-looking GPU sat next to a busy-looking engine.
const SPARKLINE_WINDOW = '5m';
const SPARKLINE_WINDOW_SECONDS = 300;

// trimToWindow drops what has fallen out of the window. The newest sample is
// the reference, so a series that stopped arriving keeps its shape instead of
// emptying itself.
function trimToWindow<T extends { ts: number }>(points: T[]): T[] {
	if (points.length === 0) return points;
	const cutoff = points[points.length - 1].ts - SPARKLINE_WINDOW_SECONDS;
	return points[0].ts >= cutoff ? points : points.filter((p) => p.ts >= cutoff);
}
export const gpuHistory = writable<Map<string, GPUMetrics[]>>(new Map());
export const hostHistory = writable<HostMetrics[]>([]);

// Backward compat: derived single-host for standalone use
export const latestHost = derived(latestHosts, ($hosts) => {
	if ($hosts.size === 0) return null;
	return $hosts.values().next().value ?? null;
});

// Process WebSocket messages
onMessage((data: any) => {
	if (data.type === 'gpu_metrics' && data.gpus) {
		const nodeId = data.node_id || 'local';

		latestGPU.update((arr) => {
			// Remove old entries for this node, add new ones
			const other = arr.filter((g) => (g.node_id || 'local') !== nodeId);
			return [...other, ...data.gpus.map((g: GPUMetrics) => ({ ...g, node_id: nodeId }))];
		});

		gpuHistory.update((map) => {
			for (const gpu of data.gpus) {
				const key = gpuKey(nodeId, gpu.gpu_id);
				let arr = map.get(key) || [];
				// A sample no newer than the last one is one the buffer already
				// holds: seeding from the API and the stream overlap by a second
				// or two at page load.
				if (arr.length > 0 && gpu.ts <= arr[arr.length - 1].ts) continue;
				map.set(key, trimToWindow([...arr, { ...gpu, node_id: nodeId }]));
			}
			return new Map(map);
		});
	}

	if (data.type === 'host_metrics' && data.host) {
		const nodeId = data.host.node_id || data.node_id || 'local';
		latestHosts.update((map) => {
			map.set(nodeId, data.host);
			return new Map(map);
		});
		hostHistory.update((arr) => {
			if (arr.length > 0 && data.host.ts <= arr[arr.length - 1].ts) return arr;
			return trimToWindow([...arr, data.host]);
		});
	}

	if (data.type === 'vllm_metrics' && data.vllm) {
		latestVLLM.set(data.vllm);
		vllmHistory.update((arr) => {
			if (arr.length > 0 && data.vllm.ts <= arr[arr.length - 1].ts) return arr;
			return trimToWindow([...arr, data.vllm]);
		});
	}

	// Nodes, devices and alerts used to arrive once, in the single status
	// call made at mount, so an open tab could show a dead node as online
	// and an alert count from page load.
	if (data.type === 'state') {
		if (data.nodes) nodes.set(data.nodes);
		if (data.devices) devices.set(data.devices);
		alerts.set(data.alerts ?? []);
	}

	if (data.type === 'gpu_processes' && data.processes) {
		const nodeId = data.node_id || 'local';
		processes.update((arr) => {
			// Remove old entries for this node, add new ones
			const other = arr.filter((p) => (p.node_id || 'local') !== nodeId);
			return [...other, ...data.processes.map((p: GPUProcess) => ({ ...p, node_id: nodeId }))];
		});
	}
});

// Fetch initial status
export async function fetchStatus() {
	try {
		const res = await fetch('/api/v1/status');
		const data = await res.json();
		if (data.nodes) nodes.set(data.nodes);
		if (data.devices) devices.set(data.devices);
		if (data.gpus) latestGPU.set(data.gpus);
		if (data.hosts) {
			const map = new Map<string, HostMetrics>();
			for (const h of data.hosts) {
				map.set(h.node_id, h);
			}
			latestHosts.set(map);
		}
		if (data.processes) processes.set(data.processes);
		alerts.set(data.alerts ?? []);
		if (data.vllm) latestVLLM.set(data.vllm);

		await seedSparklines(data.devices ?? [], !!data.vllm);
	} catch (e) {
		console.error('Failed to fetch status:', e);
	}
}

// Fill the sparkline buffers from stored history.
//
// They are fed by the stream, so on a freshly loaded page they start empty:
// the card renders without its sparkline and grows one a second or two later.
// Seeding them makes a reloaded page look like one that has been open for a
// while, which is what it looked like after any client-side navigation, since
// the buffers live in this module and survive it.
async function seedSparklines(deviceList: GPUDevice[], withVLLM: boolean) {
	const work: Promise<unknown>[] = deviceList.map(async (device) => {
		const points = await fetchGPUHistory(device.id, SPARKLINE_WINDOW, device.node_id);
		if (points.length === 0) return;

		gpuHistory.update((map) => {
			const key = gpuKey(device.node_id, device.id);
			// The stream may have arrived first; it is more current than this.
			if ((map.get(key)?.length ?? 0) > 0) return map;
			map.set(key, trimToWindow(points));
			return new Map(map);
		});
	});

	work.push(
		(async () => {
			const points = await fetchHostHistory(SPARKLINE_WINDOW);
			if (points.length === 0) return;
			hostHistory.update((arr) => (arr.length > 0 ? arr : trimToWindow(points)));
		})()
	);

	if (withVLLM) {
		work.push(
			(async () => {
				const points = await fetchVLLMHistory(SPARKLINE_WINDOW);
				if (points.length === 0) return;
				vllmHistory.update((arr) => (arr.length > 0 ? arr : trimToWindow(points)));
			})()
		);
	}

	await Promise.all(work);
}

// Parse a range string like "5m", "1h", "24h", "7d", "30d" into seconds
export function parseRangeSeconds(range: string): number {
	const match = range.match(/^(\d+)([smhd])$/);
	if (!match) return 300;
	const val = parseInt(match[1]);
	switch (match[2]) {
		case 's': return val;
		case 'm': return val * 60;
		case 'h': return val * 3600;
		case 'd': return val * 86400;
		default: return 300;
	}
}

// Fetch historical GPU metrics
export async function fetchGPUHistory(gpuId: number, range: string, nodeId?: string): Promise<GPUMetrics[]> {
	try {
		let url = `/api/v1/gpus/${gpuId}/metrics?range=${range}`;
		if (nodeId) url += `&node=${nodeId}`;
		const res = await fetch(url);
		return await res.json();
	} catch {
		return [];
	}
}

// Fetch historical vLLM metrics
export async function fetchVLLMHistory(range: string, nodeId?: string): Promise<VLLMMetrics[]> {
	try {
		let url = `/api/v1/vllm/metrics?range=${range}`;
		if (nodeId) url += `&node=${nodeId}`;
		const res = await fetch(url);
		return await res.json();
	} catch {
		return [];
	}
}

// Fetch historical host metrics
export async function fetchHostHistory(range: string, nodeId?: string): Promise<HostMetrics[]> {
	try {
		let url = `/api/v1/host/metrics?range=${range}`;
		if (nodeId) url += `&node=${nodeId}`;
		const res = await fetch(url);
		return await res.json();
	} catch {
		return [];
	}
}

// Fetch the alert journal, newest first
export async function fetchAlertHistory(
	range: string,
	nodeId?: string,
	limit = 200
): Promise<AlertEvent[]> {
	try {
		let url = `/api/v1/alerts/history?range=${range}&limit=${limit}`;
		if (nodeId) url += `&node=${nodeId}`;
		const res = await fetch(url);
		return await res.json();
	} catch {
		return [];
	}
}

// Ranges up to an hour are drawn from raw samples, which is exactly what the
// websocket delivers, so their charts can follow the stream instead of
// refetching the whole window every few seconds.
export const LIVE_RANGE_SECONDS = 3600;

export function isLiveRange(range: string): boolean {
	return parseRangeSeconds(range) <= LIVE_RANGE_SECONDS;
}

// appendPoint adds a sample to a chart series, dropping what has fallen out
// of the window. Samples that are not newer than the last one are ignored,
// so a repeated or late snapshot cannot bend the axis backwards.
export function appendPoint<T extends { ts: number }>(
	list: T[],
	point: T,
	windowSeconds: number
): T[] {
	if (list.length > 0 && point.ts <= list[list.length - 1].ts) return list;

	const cutoff = point.ts - windowSeconds;
	const kept = list.length > 0 && list[0].ts < cutoff ? list.filter((p) => p.ts >= cutoff) : list;
	return [...kept, point];
}
