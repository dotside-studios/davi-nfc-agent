import type { TagCapabilities, TagData } from "./types";

export interface RawTagPayload {
  uid?: string;
  type?: string;
  technology?: string;
  scannedAt?: string;
  text?: string;
  message?: TagData["message"];
  capabilities?: TagCapabilities;
  deviceID?: string;
  err?: string | null;
  [key: string]: unknown;
}

export function parseTagData(payload: RawTagPayload): TagData {
  const tagData: TagData = {
    uid: payload.uid || "",
    type: payload.type || "",
    technology: payload.technology || "",
    scannedAt: payload.scannedAt ? new Date(payload.scannedAt) : null,
    text: payload.text || "",
    message: payload.message || null,
    error: payload.err || null,
    capabilities: payload.capabilities,
    deviceID: payload.deviceID || undefined,
    _raw: payload,
  };

  if (tagData.message && tagData.message.type === "ndef") {
    tagData.ndefRecords = tagData.message.records || [];
  }

  return tagData;
}

export interface WireMessage<P = unknown> {
  id?: string;
  type?: string;
  payload?: P;
  success?: boolean;
  error?: string;
}

/**
 * The agent carries byte slices as base64 in both directions -- a transceive
 * command and its response, an NDEF record's raw payload. These are here rather
 * than left to each caller because the wire format is the client's business,
 * and every consumer that reached for `atob` was re-deriving it.
 */
export function encodeBase64(bytes: Uint8Array): string {
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}

export function decodeBase64(value: string): Uint8Array {
  const binary = atob(value);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}
