/**
 * realtime — Web 端 WebSocket 实时推送客户端(08 §4.8)
 *
 * 服务端→客户端推送通道,「任务完成」感知主路径(轮询降级为低频兜底)。
 * 协议:
 *   上行:{"type":"auth","token":"<JWT>"}(连接后 5s 内必须发,否则服务端断开)
 *   下行:{"type":"task.completed","data":{"task_id":N}}
 * 心跳:服务端协议级 ping,浏览器自动 pong,客户端无需处理。
 * 重连:指数退避(1s 起倍增,封顶 30s),重连成功立即回调 onReconnect 补漏。
 */

// 与 services/chat.ts 同源:BASE_URL 形如 /api/v1(生产挂 /chat/ 子路径由反代处理)
const BASE_URL: string =
  (import.meta.env.VITE_API_BASE_URL as string | undefined) || '/api/v1';

export type RealtimeEventHandler = (data: unknown) => void;

interface RealtimeOptions {
  /** JWT(身份来源,首条消息认证) */
  token: string;
  /** 事件处理:task.completed 等 */
  onEvent: (type: string, data: unknown) => void;
  /** 重连成功回调(用于立即 poll 一次补漏) */
  onReconnect?: () => void;
}

let socket: WebSocket | null = null;
let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
let reconnectDelay = 1000;
let closedByUser = false;

function buildWsUrl(): string {
  // http(s)://host + BASE_URL → ws(s)://host + BASE_URL + /ws
  const url = new URL(BASE_URL, window.location.origin);
  const scheme = url.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${scheme}//${url.host}${url.pathname.replace(/\/$/, '')}/ws`;
}

function scheduleReconnect(opts: RealtimeOptions): void {
  if (closedByUser || reconnectTimer !== null) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    connectRealtime(opts);
  }, reconnectDelay);
  reconnectDelay = Math.min(reconnectDelay * 2, 30_000);
}

/** connectRealtime 建立连接并发送 auth;失败/断开自动重连(指数退避)。 */
export function connectRealtime(opts: RealtimeOptions): void {
  closedByUser = false;
  if (socket && (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING)) {
    return; // 已连接/连接中
  }
  reconnectDelay = 1000;

  let ws: WebSocket;
  try {
    ws = new WebSocket(buildWsUrl());
  } catch {
    scheduleReconnect(opts);
    return;
  }
  socket = ws;
  // 注:重连统一走 scheduleReconnect(opts),此处闭包引用 opts 即可

  ws.onmessage = (event: MessageEvent) => {
    try {
      const msg = JSON.parse(event.data as string) as { type?: string; data?: unknown };
      if (msg.type && msg.type !== 'ping') {
        opts.onEvent(msg.type, msg.data);
      }
    } catch (err) {
      console.error('realtime: bad message', err);
    }
  };

  ws.onopen = (() => {
    // 首连与重连都在这里发 auth;重连成功重置退避并回调补漏。
    // 服务端有协议级 ping 保活,客户端无需额外心跳。
    let first = true;
    return () => {
      ws.send(JSON.stringify({ type: 'auth', token: opts.token }));
      if (first) {
        first = false;
      } else {
        reconnectDelay = 1000; // 重置退避
        opts.onReconnect?.();
      }
    };
  })();

  ws.onclose = () => {
    socket = null;
    scheduleReconnect(opts);
  };

  ws.onerror = () => {
    // onclose 会跟着触发,重连统一在 onclose 处理
  };
}

/** disconnect 关闭连接并停止重连(登出/页面卸载用)。 */
export function disconnectRealtime(): void {
  closedByUser = true;
  if (reconnectTimer !== null) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (socket) {
    socket.close();
    socket = null;
  }
}
