import { writable, derived } from 'svelte/store';
import { onMessage } from './websocket';

export interface GPUDevice {
	node_id: string;
	id: number;
	uuid: string;
	name: string;
	mem_total: number;
	driver_ver: string;
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

export interface Alert {
	node_id: string;
	gpu_id: number;
	metric: string;
	value: number;
	threshold: number;
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
export const alerts = writable<Alert[]>([]);

// Sparkline buffers: keep last 120 readings per GPU (keyed by "node:gpu_id")
const SPARKLINE_SIZE = 120;
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
				arr.push({ ...gpu, node_id: nodeId });
				if (arr.length > SPARKLINE_SIZE) arr = arr.slice(-SPARKLINE_SIZE);
				map.set(key, arr);
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
			arr.push(data.host);
			if (arr.length > SPARKLINE_SIZE) arr = arr.slice(-SPARKLINE_SIZE);
			return [...arr];
		});
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
		if (data.alerts) alerts.set(data.alerts);
	} catch (e) {
		console.error('Failed to fetch status:', e);
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
