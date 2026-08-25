import { useCallback, useEffect, useRef, useState } from "react";
import { NFCClient } from "../session/client";
import { diagnoseAgent, type AgentDiagnosis } from "../session/diagnose";
import type {
  LockResponse,
  NFCClientOptions,
  NFCErrorEvent,
  TagCapabilities,
  TagData,
  TagTarget,
  TransceiveRequest,
  WriteRequest,
  WriteResponse,
} from "../session/types";

export type ConnectionStatus = "disconnected" | "connecting" | "connected";

export interface UseNFCClientReturn {
  connectionStatus: ConnectionStatus;
  lastTag: TagData | null;
  /** Seeded from the scan, replaced by `refreshCapabilities`. */
  capabilities: TagCapabilities | null;
  error: NFCErrorEvent | null;
  /**
   * Why the connection failed, in terms an operator can act on. Null while
   * connected, or while the first attempt is still in flight.
   */
  diagnosis: AgentDiagnosis | null;
  reconnect: () => Promise<void>;
  clearLastTag: () => void;
  write: (request: WriteRequest) => Promise<WriteResponse>;
  /** Irreversible. */
  lock: (target?: TagTarget) => Promise<LockResponse>;
  transceive: (request: TransceiveRequest) => Promise<Uint8Array>;
  refreshCapabilities: (target?: TagTarget) => Promise<TagCapabilities>;
}

export function useNFCClient(
  serverUrl: string,
  options?: NFCClientOptions,
): UseNFCClientReturn {
  const clientRef = useRef<NFCClient | null>(null);
  const [connectionStatus, setConnectionStatus] =
    useState<ConnectionStatus>("disconnected");
  const [lastTag, setLastTag] = useState<TagData | null>(null);
  const [capabilities, setCapabilities] = useState<TagCapabilities | null>(null);
  const [error, setError] = useState<NFCErrorEvent | null>(null);
  const [diagnosis, setDiagnosis] = useState<AgentDiagnosis | null>(null);

  // Guards the probe so a reconnect loop cannot fire one per attempt.
  const diagnosing = useRef(false);

  const runDiagnosis = useCallback(async (url: string) => {
    if (diagnosing.current) return;
    diagnosing.current = true;
    try {
      setDiagnosis(await diagnoseAgent(url));
    } catch {
      // Leave the last diagnosis in place; a failed probe is not information.
    }
  }, []);

  const connect = useCallback(async () => {
    if (!clientRef.current) return;
    setConnectionStatus("connecting");
    setError(null);
    try {
      await clientRef.current.connect();
    } catch (err) {
      setConnectionStatus("disconnected");
      setError({
        error: err instanceof Error ? err : new Error(String(err)),
        phase: "connection",
      });
      void runDiagnosis(clientRef.current.serverUrl);
    }
  }, [runDiagnosis]);

  const reconnect = useCallback(async () => {
    if (clientRef.current?.isConnected()) {
      await clientRef.current.disconnect();
    }
    // An explicit retry means the operator changed something; probe again
    // rather than showing them a stale reason.
    diagnosing.current = false;
    setDiagnosis(null);
    await connect();
  }, [connect]);

  const clearLastTag = useCallback(() => {
    setLastTag(null);
    setCapabilities(null);
  }, []);

  const write = useCallback(async (request: WriteRequest) => {
    if (!clientRef.current) throw new Error("NFC reader is not connected");
    return clientRef.current.write(request);
  }, []);

  const lock = useCallback(async (target?: TagTarget) => {
    if (!clientRef.current) throw new Error("NFC reader is not connected");
    return clientRef.current.lock(target);
  }, []);

  const transceive = useCallback(async (request: TransceiveRequest) => {
    if (!clientRef.current) throw new Error("NFC reader is not connected");
    return clientRef.current.transceive(request);
  }, []);

  const refreshCapabilities = useCallback(async (target?: TagTarget) => {
    if (!clientRef.current) throw new Error("NFC reader is not connected");
    const caps = await clientRef.current.getCapabilities(target);
    setCapabilities(caps);
    return caps;
  }, []);

  useEffect(() => {
    const client = new NFCClient(serverUrl, options);
    clientRef.current = client;

    const handleConnected = () => {
      setConnectionStatus("connected");
      setError(null);
      setDiagnosis(null);
      diagnosing.current = false;
    };
    const handleDisconnected = () => setConnectionStatus("disconnected");
    const handleTagData = (data: TagData) => {
      setLastTag(data);
      if (data.capabilities) setCapabilities(data.capabilities);
    };
    const handleTagRemoved = () => {
      setLastTag(null);
      setCapabilities(null);
    };
    const handleError = (err: NFCErrorEvent) => setError(err);

    client.on("connected", handleConnected);
    client.on("disconnected", handleDisconnected);
    client.on("tagData", handleTagData);
    client.on("tagRemoved", handleTagRemoved);
    client.on("error", handleError);

    void connect();

    return () => {
      client.off("connected", handleConnected);
      client.off("disconnected", handleDisconnected);
      client.off("tagData", handleTagData);
      client.off("tagRemoved", handleTagRemoved);
      client.off("error", handleError);
      void client.disconnect();
    };
    // serverUrl/options are read on mount; consumers should remount when they change.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [connect]);

  return {
    connectionStatus,
    lastTag,
    capabilities,
    error,
    diagnosis,
    reconnect,
    clearLastTag,
    write,
    lock,
    transceive,
    refreshCapabilities,
  };
}
