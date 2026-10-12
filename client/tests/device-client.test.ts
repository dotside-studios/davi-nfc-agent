// @vitest-environment happy-dom
import { beforeAll, beforeEach, describe, expect, it } from "vitest";
import { FakeWebSocket, installFakeWebSocket } from "./_fake-websocket";

type DeviceClient = {
  connect(): Promise<void>;
};
type DeviceClientClass = new (
  serverUrl: string,
  options?: Record<string, unknown>,
) => DeviceClient;

let NFCDeviceClient: DeviceClientClass;

beforeAll(async () => {
  await import("../nfc-device-client.js");
  NFCDeviceClient = (window as unknown as { NFCDeviceClient: DeviceClientClass })
    .NFCDeviceClient;
});

async function connectedUrl(serverUrl: string, options: Record<string, unknown> = {}) {
  const client = new NFCDeviceClient(serverUrl, { autoReconnect: false, ...options });
  const connecting = client.connect().catch(() => {});
  const url = FakeWebSocket.lastInstance!.url;
  FakeWebSocket.lastInstance!.close();
  await connecting;
  return url;
}

describe("NFCDeviceClient connection URL", () => {
  beforeEach(() => {
    installFakeWebSocket();
  });

  it.each([
    ["ws://localhost:9470", "ws://localhost:9470/ws?mode=device"],
    ["ws://localhost:9470/", "ws://localhost:9470/ws?mode=device"],
    ["https://host:9470", "wss://host:9470/ws?mode=device"],
    ["wss://host:9470/ws", "wss://host:9470/ws?mode=device"],
    ["wss://host:9470/agent/", "wss://host:9470/agent/ws?mode=device"],
    ["wss://host:9470/ws?secret=TOKEN", "wss://host:9470/ws?secret=TOKEN&mode=device"],
    ["wss://host:9470?secret=TOKEN", "wss://host:9470/ws?secret=TOKEN&mode=device"],
    ["wss://host:9470/ws?mode=device&secret=TOKEN&", "wss://host:9470/ws?mode=device&secret=TOKEN"],
  ])("%s connects to %s", async (serverUrl, expected) => {
    expect(await connectedUrl(serverUrl)).toBe(expected);
  });

  it("sends the secret option as ?secret=", async () => {
    expect(await connectedUrl("wss://host:9470", { secret: "a+b/c=" })).toBe(
      "wss://host:9470/ws?mode=device&secret=a%2Bb%2Fc%3D",
    );
  });

  it("prefers the secret option over one in the URL", async () => {
    expect(await connectedUrl("wss://host:9470/ws?secret=OLD", { secret: "NEW" })).toBe(
      "wss://host:9470/ws?secret=NEW&mode=device",
    );
  });
});
