import { writable } from 'svelte/store';
import type { GPUMetrics, HostMetrics, GPUProcess, GPUDevice } from './metrics';

export const connected = writable(false);

let ws: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;

type MessageHandler = (data: any) => void;
const handlers: MessageHandler[] = [];

export function onMessage(handler: MessageHandler) {
	handlers.push(handler);
	return () => {
		const idx = handlers.indexOf(handler);
		if (idx >= 0) handlers.splice(idx, 1);
	};
}

export function connect() {
	if (ws) return;

	const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
	const url = `${proto}//${location.host}/api/v1/ws`;

	ws = new WebSocket(url);

	ws.onopen = () => {
		connected.set(true);
		if (reconnectTimer) {
			clearTimeout(reconnectTimer);
			reconnectTimer = null;
		}
	};

	ws.onmessage = (ev) => {
		try {
			const data = JSON.parse(ev.data);
			for (const h of handlers) h(data);
		} catch {}
	};

	ws.onclose = () => {
		connected.set(false);
		ws = null;
		reconnectTimer = setTimeout(connect, 2000);
	};

	ws.onerror = () => {
		ws?.close();
	};
}

export function disconnect() {
	if (reconnectTimer) {
		clearTimeout(reconnectTimer);
		reconnectTimer = null;
	}
	if (ws) {
		ws.close();
		ws = null;
	}
}
