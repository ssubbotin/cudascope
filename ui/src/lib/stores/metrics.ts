import { writable, derived, get } from 'svelte/store';
import { onMessage } from './websocket';

export interface GPUDevice {
	id: number;
	uuid: string;
	name: string;
	mem_total: number;
	driver_ver: string;
}

export interface GPUMetrics {
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
	ts: number;
	gpu_id: number;
	pid: number;
	name: string;
	gpu_mem: number;
}

// Stores
export const devices = writable<GPUDevice[]>([]);
export const latestGPU = writable<GPUMetrics[]>([]);
export const latestHost = writable<HostMetrics | null>(null);
export const processes = writable<GPUProcess[]>([]);

// Sparkline buffers: keep last 60 readings per GPU
const SPARKLINE_SIZE = 120;
export const gpuHistory = writable<Map<number, GPUMetrics[]>>(new Map());
export const hostHistory = writable<HostMetrics[]>([]);

// Process WebSocket messages
onMessage((data: any) => {
	if (data.type === 'gpu_metrics' && data.gpus) {
		latestGPU.set(data.gpus);

		gpuHistory.update((map) => {
			for (const gpu of data.gpus) {
				let arr = map.get(gpu.gpu_id) || [];
				arr.push(gpu);
				if (arr.length > SPARKLINE_SIZE) arr = arr.slice(-SPARKLINE_SIZE);
				map.set(gpu.gpu_id, arr);
			}
			return new Map(map);
		});
	}

	if (data.type === 'host_metrics' && data.host) {
		latestHost.set(data.host);
		hostHistory.update((arr) => {
			arr.push(data.host);
			if (arr.length > SPARKLINE_SIZE) arr = arr.slice(-SPARKLINE_SIZE);
			return [...arr];
		});
	}

	if (data.type === 'gpu_processes' && data.processes) {
		processes.set(data.processes);
	}
});

// Fetch initial status
export async function fetchStatus() {
	try {
		const res = await fetch('/api/v1/status');
		const data = await res.json();
		if (data.devices) devices.set(data.devices);
		if (data.gpus) latestGPU.set(data.gpus);
		if (data.host) latestHost.set(data.host);
		if (data.processes) processes.set(data.processes);
	} catch (e) {
		console.error('Failed to fetch status:', e);
	}
}

// Fetch historical GPU metrics
export async function fetchGPUHistory(gpuId: number, range: string): Promise<GPUMetrics[]> {
	try {
		const res = await fetch(`/api/v1/gpus/${gpuId}/metrics?range=${range}`);
		return await res.json();
	} catch {
		return [];
	}
}

// Fetch historical host metrics
export async function fetchHostHistory(range: string): Promise<HostMetrics[]> {
	try {
		const res = await fetch(`/api/v1/host/metrics?range=${range}`);
		return await res.json();
	} catch {
		return [];
	}
}
