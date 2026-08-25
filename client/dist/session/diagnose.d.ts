/**
 * Why a connection to the local agent failed, in terms an operator can act on.
 *
 * The browser deliberately withholds WebSocket failure detail — a refused
 * connection, a rejected certificate and a blocked origin all surface as the
 * same contentless `error` event. So rather than reading the socket's failure,
 * this probes the agent over HTTP and reasons about what answers.
 */
export type AgentDiagnosisKind = "reachable" | "origin-blocked" | "wrong-scheme" | "unreachable";
export interface AgentDiagnosis {
    kind: AgentDiagnosisKind;
    /** One line, addressed to whoever is standing at the reader. */
    title: string;
    detail: string;
    /**
     * A URL to open in a new tab when we cannot tell two causes apart. Loading
     * the agent directly makes the browser answer: a certificate warning means
     * the agent is running but untrusted, "can't connect" means it is not
     * running. See `unreachable` below.
     */
    openUrl?: string;
}
/**
 * Works out why the reader could not be reached.
 *
 * Call it after a connection attempt fails; it costs one or two HTTP requests
 * to loopback.
 */
export declare function diagnoseAgent(serverUrl: string): Promise<AgentDiagnosis>;
