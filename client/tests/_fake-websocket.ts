import { vi } from "vitest";

export class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;

  static lastInstance: FakeWebSocket | null = null;
  static instances: FakeWebSocket[] = [];

  url: string;
  readyState = FakeWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: ((e: unknown) => void) | null = null;
  onclose: (() => void) | null = null;
  send = vi.fn<(data: string) => void>();

  constructor(url: string) {
    this.url = url;
    FakeWebSocket.lastInstance = this;
    FakeWebSocket.instances.push(this);
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.onopen?.();
  }

  emit(message: unknown) {
    this.onmessage?.({ data: JSON.stringify(message) });
  }

  fail(err: unknown = new Error("ws error")) {
    this.onerror?.(err);
  }

  close() {
    this.readyState = FakeWebSocket.CLOSED;
    this.onclose?.();
  }
}

export function installFakeWebSocket() {
  FakeWebSocket.lastInstance = null;
  FakeWebSocket.instances = [];
  // @ts-expect-error overriding global for test
  globalThis.WebSocket = FakeWebSocket;
}

/**
 * Open the most recent fake socket and pump fake timers far enough that
 * NFCClient.waitForConnection() (which polls every 100ms) sees OPEN and
 * resolves connect(). Use this whenever a test calls `client.connect()`
 * with `vi.useFakeTimers()` active.
 */
export async function openLastSocket(): Promise<void> {
  if (!FakeWebSocket.lastInstance) {
    throw new Error("openLastSocket: no fake socket has been created yet");
  }
  FakeWebSocket.lastInstance.open();
  // waitForConnection's recursive setTimeout(checkConnection, 100) needs
  // to fire once after open() to observe OPEN and resolve.
  await vi.advanceTimersByTimeAsync(150);
}
