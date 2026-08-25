import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NFCClient } from "../src/session/client";
import { parseTagData } from "../src/session/protocol";
import {
  FakeWebSocket,
  installFakeWebSocket,
  openLastSocket,
} from "./_fake-websocket";

describe("NFCClient lifecycle", () => {
  beforeEach(() => {
    installFakeWebSocket();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("connects, opens the socket, and emits 'connected'", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const onConnected = vi.fn();
    client.on("connected", onConnected);

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    expect(FakeWebSocket.lastInstance!.url).toBe("ws://localhost:18080/ws");
    expect(client.isConnected()).toBe(true);
    expect(onConnected).toHaveBeenCalledTimes(1);
  });

  it("appends the api secret to the websocket URL when provided", async () => {
    const client = new NFCClient("http://localhost:18080", {
      apiSecret: "abc xyz",
      autoReconnect: false,
    });
    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    expect(FakeWebSocket.lastInstance!.url).toBe(
      "ws://localhost:18080/ws?secret=abc%20xyz",
    );
  });

  it("strips trailing slash from server URL", async () => {
    const client = new NFCClient("http://localhost:18080/", { autoReconnect: false });
    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    expect(FakeWebSocket.lastInstance!.url).toBe("ws://localhost:18080/ws");
  });

  it("emits 'tagData' for broadcast messages without an id", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const onTag = vi.fn();
    client.on("tagData", onTag);

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    FakeWebSocket.lastInstance!.emit({
      type: "tagData",
      payload: { uid: "DEAD", type: "MIFARE", technology: "ISO14443A" },
    });

    expect(onTag).toHaveBeenCalledTimes(1);
    expect(onTag.mock.calls[0][0].uid).toBe("DEAD");
  });

  it("removes handlers via off()", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const onTag = vi.fn();
    client.on("tagData", onTag);
    client.off("tagData", onTag);

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    FakeWebSocket.lastInstance!.emit({ type: "tagData", payload: { uid: "X" } });
    expect(onTag).not.toHaveBeenCalled();
  });

  it("emits 'disconnected' on socket close and triggers reconnect when enabled", async () => {
    const client = new NFCClient("http://localhost:18080", {
      autoReconnect: true,
      reconnectDelay: 100,
      maxReconnectAttempts: 1,
    });
    const onDisconnected = vi.fn();
    client.on("disconnected", onDisconnected);

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    const first = FakeWebSocket.lastInstance!;
    first.close();

    expect(onDisconnected).toHaveBeenCalledTimes(1);
    expect(client.isConnected()).toBe(false);

    // Fast-forward the reconnect timer; a new WebSocket should be constructed.
    await vi.advanceTimersByTimeAsync(150);

    expect(FakeWebSocket.instances.length).toBe(2);
  });

  it("does not reconnect after intentional disconnect()", async () => {
    const client = new NFCClient("http://localhost:18080", {
      autoReconnect: true,
      reconnectDelay: 100,
    });

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    await client.disconnect();
    // Simulate ws close arriving after disconnect()
    FakeWebSocket.lastInstance!.close();

    await vi.advanceTimersByTimeAsync(500);
    expect(FakeWebSocket.instances.length).toBe(1);
  });

  it("emits 'error' with phase 'websocket' when ws.onerror fires", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const onError = vi.fn();
    client.on("error", onError);

    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    FakeWebSocket.lastInstance!.fail(new Error("ws boom"));

    expect(onError).toHaveBeenCalledTimes(1);
    expect(onError.mock.calls[0][0]).toMatchObject({
      error: expect.objectContaining({ message: "ws boom" }),
      phase: "websocket",
    });
  });
});

describe("NFCClient request/response", () => {
  beforeEach(() => {
    installFakeWebSocket();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("write() sends a writeRequest and resolves on matching response", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    const ws = FakeWebSocket.lastInstance!;
    const writePromise = client.write({
      records: [{ type: "text", content: "hello" }],
    });

    expect(ws.send).toHaveBeenCalledTimes(1);
    const sent = JSON.parse(ws.send.mock.calls[0][0]);
    expect(sent.type).toBe("writeRequest");
    expect(sent.id).toMatch(/^req_/);
    expect(sent.payload).toEqual({
      records: [{ type: "text", content: "hello" }],
    });

    ws.emit({ id: sent.id, success: true, payload: { message: "ok" } });

    await expect(writePromise).resolves.toEqual({ message: "ok" });
  });

  it("write() rejects on success=false response", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    const ws = FakeWebSocket.lastInstance!;
    const writePromise = client.write({ records: [] });
    const sent = JSON.parse(ws.send.mock.calls[0][0]);

    ws.emit({ id: sent.id, success: false, error: "no tag" });

    await expect(writePromise).rejects.toThrow("no tag");
  });

  it("write() rejects when not connected", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    await expect(client.write({ records: [] })).rejects.toThrow(
      /Not connected/,
    );
  });

  it("write() rejects on timeout", async () => {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const connectPromise = client.connect();
    await openLastSocket();
    await connectPromise;

    const writePromise = client.write({ records: [] });
    // Attach rejection handler before advancing time so the rejection is
    // never "unhandled" from Node's perspective.
    const assertion = expect(writePromise).rejects.toThrow(/timeout/);
    // No response — fast-forward past the 30s request timeout.
    await vi.advanceTimersByTimeAsync(30_000);

    await assertion;
  });

  it("healthCheck() returns the parsed JSON body verbatim", async () => {
    vi.useRealTimers();
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ status: "ok", timestamp: "t" }), { status: 200 }),
    );

    const client = new NFCClient("http://localhost:18080");
    await expect(client.healthCheck()).resolves.toEqual({
      status: "ok",
      timestamp: "t",
    });

    fetchSpy.mockRestore();
  });
});

describe("capability and error surface", () => {
  it("carries tag capabilities through parseTagData", () => {
    const tag = parseTagData({
      uid: "04:A1:B2:C3",
      type: "NTAG215",
      technology: "ISO14443A",
      capabilities: {
        canWrite: true,
        canLock: true,
        maxNdefSize: 504,
        tagFamily: "NTAG",
      },
    });

    expect(tag.capabilities?.canWrite).toBe(true);
    expect(tag.capabilities?.maxNdefSize).toBe(504);
    expect(tag.capabilities?.tagFamily).toBe("NTAG");
  });

  // A consumer must be able to tell "cannot write" from "did not say".
  it("leaves capabilities undefined when the agent sends none", () => {
    const tag = parseTagData({ uid: "04:A1:B2:C3", type: "NTAG215" });
    expect(tag.capabilities).toBeUndefined();
  });
});

describe("naming the tag a request applies to", () => {
  beforeEach(() => {
    installFakeWebSocket();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  async function connected() {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const connecting = client.connect();
    await openLastSocket();
    await connecting;
    return { client, ws: FakeWebSocket.lastInstance! };
  }

  function lastSent(ws: FakeWebSocket) {
    const calls = ws.send.mock.calls;
    return JSON.parse(calls[calls.length - 1][0]);
  }

  // Without a uid the agent refuses the request with TAG_NOT_NAMED.
  it("names the scanned tag on a write the caller did not target", async () => {
    const { client, ws } = await connected();
    ws.emit({ type: "tagData", payload: { uid: "04A1B2", type: "NTAG215" } });

    void client.write({ records: [{ type: "text", content: "hi" }] });

    expect(lastSent(ws).payload).toMatchObject({ uid: "04A1B2" });
  });

  it("carries the deviceID when the tag came from a paired device", async () => {
    const { client, ws } = await connected();
    ws.emit({
      type: "tagData",
      payload: { uid: "04A1B2", type: "NTAG215", deviceID: "phone-7" },
    });

    void client.lock();

    expect(lastSent(ws).payload).toEqual({ uid: "04A1B2", deviceID: "phone-7" });
  });

  it("prefers the target the caller named over the tag in the field", async () => {
    const { client, ws } = await connected();
    ws.emit({ type: "tagData", payload: { uid: "04A1B2" } });

    void client.getCapabilities({ uid: "CAFE" });

    expect(lastSent(ws).payload).toEqual({ uid: "CAFE" });
  });

  it("names no tag when the caller opted into the agent guessing", async () => {
    const { client, ws } = await connected();
    ws.emit({ type: "tagData", payload: { uid: "04A1B2" } });

    void client.write({ records: [], allowUntargeted: true });

    const payload = lastSent(ws).payload;
    expect(payload.allowUntargeted).toBe(true);
    expect(payload.uid).toBeUndefined();
  });

  it("stops naming a tag once it leaves the field", async () => {
    const { client, ws } = await connected();
    const onRemoved = vi.fn();
    client.on("tagRemoved", onRemoved);

    ws.emit({ type: "tagData", payload: { uid: "04A1B2" } });
    // A tagData broadcast with no uid is how removal is reported.
    ws.emit({ type: "tagData", payload: { uid: "" } });

    expect(onRemoved).toHaveBeenCalledWith({ uid: "04A1B2" });
    expect(client.currentTag()).toBeNull();

    void client.lock();
    expect(lastSent(ws).payload).toEqual({});
  });

  // cardPresent describes the local reader, and is false the whole time a
  // phone is holding a tag.
  it("keeps a phone's tag when the local reader reports no card", async () => {
    const { client, ws } = await connected();
    ws.emit({ type: "tagData", payload: { uid: "04A1B2", deviceID: "phone-7" } });
    ws.emit({ type: "deviceStatus", payload: { connected: true, cardPresent: false } });

    expect(client.currentTag()?.uid).toBe("04A1B2");
  });

  it("drops the local reader's tag when it reports no card", async () => {
    const { client, ws } = await connected();
    ws.emit({ type: "tagData", payload: { uid: "04A1B2" } });
    ws.emit({ type: "deviceStatus", payload: { connected: true, cardPresent: false } });

    expect(client.currentTag()).toBeNull();
  });
});

describe("tag operations", () => {
  beforeEach(() => {
    installFakeWebSocket();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  async function connected() {
    const client = new NFCClient("http://localhost:18080", { autoReconnect: false });
    const connecting = client.connect();
    await openLastSocket();
    await connecting;
    return { client, ws: FakeWebSocket.lastInstance! };
  }

  it("transceive() base64-encodes the command and decodes the response", async () => {
    const { client, ws } = await connected();
    const exchange = client.transceive({
      data: new Uint8Array([0xff, 0xca, 0x00, 0x00, 0x00]),
    });

    const sent = JSON.parse(ws.send.mock.calls[0][0]);
    expect(sent.type).toBe("transceiveRequest");
    expect(sent.payload.data).toBe("/8oAAAA=");
    expect(sent.payload.raw).toBe(false);

    ws.emit({ id: sent.id, success: true, payload: { data: "kAA=" } });

    await expect(exchange).resolves.toEqual(new Uint8Array([0x90, 0x00]));
  });

  it("transceive() refuses an empty command without a round trip", async () => {
    const { client, ws } = await connected();
    await expect(client.transceive({ data: new Uint8Array() })).rejects.toThrow(
      /requires a command/,
    );
    expect(ws.send).not.toHaveBeenCalled();
  });

  it("getCapabilities() unwraps the capabilities the agent nests", async () => {
    const { client, ws } = await connected();
    const asking = client.getCapabilities();
    const sent = JSON.parse(ws.send.mock.calls[0][0]);
    expect(sent.type).toBe("capabilitiesRequest");

    ws.emit({
      id: sent.id,
      success: true,
      payload: { capabilities: { canWrite: true, maxNdefSize: 504 } },
    });

    await expect(asking).resolves.toEqual({ canWrite: true, maxNdefSize: 504 });
  });

  it("lock() sends a lockRequest and resolves with the outcome", async () => {
    const { client, ws } = await connected();
    const locking = client.lock();
    const sent = JSON.parse(ws.send.mock.calls[0][0]);
    expect(sent.type).toBe("lockRequest");

    ws.emit({ id: sent.id, success: true, payload: { message: "ok", locked: true } });

    await expect(locking).resolves.toEqual({ message: "ok", locked: true });
  });

  it("rejects with the agent's error code and retryability", async () => {
    const { client, ws } = await connected();
    const writing = client.write({ records: [] });
    const sent = JSON.parse(ws.send.mock.calls[0][0]);

    ws.emit({
      id: sent.id,
      type: "error",
      success: false,
      error: "tag is read-only",
      payload: { code: "READ_ONLY", retryable: false, op: "WriteData", tagUID: "04A1B2" },
    });

    await expect(writing).rejects.toMatchObject({
      name: "NFCRequestError",
      message: "tag is read-only",
      code: "READ_ONLY",
      retryable: false,
      op: "WriteData",
      tagUID: "04A1B2",
    });
  });

  it("fails a request in flight when the socket closes", async () => {
    const { client, ws } = await connected();
    const writing = client.write({ records: [] });
    const assertion = expect(writing).rejects.toThrow(/connection closed/);
    ws.close();
    await assertion;
  });
});

describe("reconnection backoff", () => {
  beforeEach(() => {
    installFakeWebSocket();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("doubles the delay between attempts, up to the ceiling", async () => {
    const client = new NFCClient("http://localhost:18080", {
      autoReconnect: true,
      reconnectDelay: 100,
      maxReconnectDelay: 250,
      maxReconnectAttempts: 5,
    });

    const connecting = client.connect();
    await openLastSocket();
    await connecting;

    FakeWebSocket.lastInstance!.close();
    await vi.advanceTimersByTimeAsync(99);
    expect(FakeWebSocket.instances.length).toBe(1);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances.length).toBe(2);

    FakeWebSocket.lastInstance!.close();
    await vi.advanceTimersByTimeAsync(199);
    expect(FakeWebSocket.instances.length).toBe(2);
    await vi.advanceTimersByTimeAsync(1);
    expect(FakeWebSocket.instances.length).toBe(3);

    // Third attempt would be 400ms; the ceiling holds it at 250ms.
    FakeWebSocket.lastInstance!.close();
    await vi.advanceTimersByTimeAsync(250);
    expect(FakeWebSocket.instances.length).toBe(4);
  });

  it("reconnects again after an explicit disconnect and reconnect", async () => {
    const client = new NFCClient("http://localhost:18080", {
      autoReconnect: true,
      reconnectDelay: 100,
    });

    let connecting = client.connect();
    await openLastSocket();
    await connecting;
    await client.disconnect();

    connecting = client.connect();
    await openLastSocket();
    await connecting;

    FakeWebSocket.lastInstance!.close();
    await vi.advanceTimersByTimeAsync(150);

    expect(FakeWebSocket.instances.length).toBe(3);
  });
});
