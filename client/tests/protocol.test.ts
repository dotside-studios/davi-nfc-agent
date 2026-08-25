import { describe, expect, it } from "vitest";
import { decodeBase64, encodeBase64, parseTagData } from "../src/session/protocol";

describe("parseTagData", () => {
  it("maps known fields with sensible defaults", () => {
    const result = parseTagData({
      uid: "04A1B2C3",
      type: "MIFARE Classic 1K",
      technology: "ISO14443A",
      scannedAt: "2026-04-29T12:00:00Z",
      text: "hello",
    });

    expect(result.uid).toBe("04A1B2C3");
    expect(result.type).toBe("MIFARE Classic 1K");
    expect(result.technology).toBe("ISO14443A");
    expect(result.scannedAt).toEqual(new Date("2026-04-29T12:00:00Z"));
    expect(result.text).toBe("hello");
    expect(result.message).toBeNull();
    expect(result.error).toBeNull();
  });

  it("defaults missing fields to empty strings and null timestamp", () => {
    const result = parseTagData({});
    expect(result.uid).toBe("");
    expect(result.type).toBe("");
    expect(result.technology).toBe("");
    expect(result.scannedAt).toBeNull();
    expect(result.text).toBe("");
    expect(result.message).toBeNull();
  });

  it("propagates the err field as error", () => {
    const result = parseTagData({ err: "read failed" });
    expect(result.error).toBe("read failed");
  });

  it("exposes ndef records when message.type is 'ndef'", () => {
    // The agent carries a record's undecoded payload as base64, not bytes.
    const records = [{ type: "text", content: "hi", tnf: 1, payload: "AmVuaGk=" }];
    const result = parseTagData({
      message: { type: "ndef", records },
    });
    expect(result.message).toEqual({ type: "ndef", records });
    expect(result.ndefRecords).toEqual(records);
  });

  it("does not set ndefRecords when message.type is 'raw'", () => {
    const result = parseTagData({
      message: { type: "raw", data: "AQID" },
    });
    expect(result.message).toEqual({ type: "raw", data: "AQID" });
    expect(result.ndefRecords).toBeUndefined();
  });

  it("preserves the raw payload under _raw for debugging", () => {
    const payload = { uid: "X", custom: "field" };
    const result = parseTagData(payload);
    expect(result._raw).toBe(payload);
  });
});

describe("base64", () => {
  it("round-trips bytes the wire carries as base64", () => {
    const bytes = new Uint8Array([0x00, 0xff, 0x90, 0x00, 0x7f]);
    expect(decodeBase64(encodeBase64(bytes))).toEqual(bytes);
  });

  it("encodes a PC/SC get-UID APDU the way the agent decodes it", () => {
    expect(encodeBase64(new Uint8Array([0xff, 0xca, 0x00, 0x00, 0x00]))).toBe(
      "/8oAAAA=",
    );
  });
});

describe("the device that scanned a tag", () => {
  it("names the paired device, and leaves it undefined for the local reader", () => {
    expect(parseTagData({ uid: "X", deviceID: "phone-7" }).deviceID).toBe("phone-7");
    expect(parseTagData({ uid: "X" }).deviceID).toBeUndefined();
  });
});
