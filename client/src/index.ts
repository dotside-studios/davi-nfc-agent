export { NFCClient, NFCRequestError } from "./session/client";
export {
  decodeBase64,
  encodeBase64,
  parseTagData,
} from "./session/protocol";
export { diagnoseAgent } from "./session/diagnose";
export type { AgentDiagnosis, AgentDiagnosisKind } from "./session/diagnose";
export type {
  DeviceStatus,
  HealthCheckResponse,
  LockResponse,
  NDEFRecord,
  NDEFRecordWrite,
  NFCClientOptions,
  NFCErrorEvent,
  NFCEventHandler,
  NFCEventName,
  NFCEventPayloadMap,
  TagCapabilities,
  TagData,
  TagMessage,
  TagTarget,
  TransceiveRequest,
  WriteRecord,
  WriteRequest,
  WriteResponse,
} from "./session/types";
