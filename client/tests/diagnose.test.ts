import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { diagnoseAgent } from "../src/session/diagnose";

// The whole point of diagnoseAgent is that a browser reports "agent missing",
// "certificate rejected" and "origin refused" identically. Each case below is a
// failure an operator has to be told apart, so the message names one remedy.
describe("diagnoseAgent", () => {
  beforeEach(() => {
    vi.stubGlobal("location", { host: "shop.davi.social" });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  const ok = () => new Response('{"status":"ok"}', { status: 200 });

  it("blames the origin allowlist when the agent answers but the socket did not", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(ok()));

    const d = await diagnoseAgent("https://localhost:9470");

    expect(d.kind).toBe("origin-blocked");
    // Names the exact origin to add, so nobody has to work it out.
    expect(d.detail).toContain("shop.davi.social");
    expect(d.detail).toContain("allowed-origins");
  });

  it("reports the status when the agent answers unhealthily", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(new Response("forbidden", { status: 403 })),
    );

    const d = await diagnoseAgent("https://localhost:9470");

    expect(d.kind).toBe("origin-blocked");
    expect(d.detail).toContain("403");
  });

  it("names a scheme mismatch when the agent answers over http but the site expects https", async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce(ok());
    vi.stubGlobal("fetch", fetchMock);

    const d = await diagnoseAgent("https://localhost:9470");

    expect(d.kind).toBe("wrong-scheme");
    expect(d.detail).toContain("http address");
    expect(fetchMock.mock.calls[1]?.[0]).toBe("http://localhost:9470/api/v1/health");
  });

  it("hands back a link when not-running and untrusted cannot be told apart", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));

    const d = await diagnoseAgent("https://localhost:9470");

    expect(d.kind).toBe("unreachable");
    // The browser is the only thing that can disambiguate these two, so the
    // operator has to be sent to it.
    expect(d.openUrl).toBe("https://localhost:9470");
    expect(d.detail).toContain("certificate warning");
    expect(d.detail).toContain("isn't running");
  });

  it("does not probe for a scheme mismatch when already on http", async () => {
    const fetchMock = vi.fn().mockRejectedValue(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetchMock);

    const d = await diagnoseAgent("http://localhost:9470");

    expect(d.kind).toBe("unreachable");
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("tolerates a trailing slash on the configured URL", async () => {
    const fetchMock = vi.fn().mockResolvedValue(ok());
    vi.stubGlobal("fetch", fetchMock);

    await diagnoseAgent("https://localhost:9470/");

    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://localhost:9470/api/v1/health");
  });
});
