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
export declare function parseTagData(payload: RawTagPayload): TagData;
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
export declare function encodeBase64(bytes: Uint8Array): string;
export declare function decodeBase64(value: string): Uint8Array;
