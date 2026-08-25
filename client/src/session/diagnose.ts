/**
 * Why a connection to the local agent failed, in terms an operator can act on.
 *
 * The browser deliberately withholds WebSocket failure detail — a refused
 * connection, a rejected certificate and a blocked origin all surface as the
 * same contentless `error` event. So rather than reading the socket's failure,
 * this probes the agent over HTTP and reasons about what answers.
 */

export type AgentDiagnosisKind =
  | "reachable"
  | "origin-blocked"
  | "wrong-scheme"
  | "unreachable";

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

const PROBE_TIMEOUT_MS = 4000;

async function probe(url: string): Promise<Response | null> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    return await fetch(url, { signal: controller.signal });
  } catch {
    // A network-level failure. Indistinguishable by design between "nothing is
    // listening" and "the certificate was rejected" — see diagnoseAgent.
    return null;
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Works out why the reader could not be reached.
 *
 * Call it after a connection attempt fails; it costs one or two HTTP requests
 * to loopback.
 */
export async function diagnoseAgent(serverUrl: string): Promise<AgentDiagnosis> {
  const base = serverUrl.replace(/\/$/, "");
  const health = await probe(`${base}/api/v1/health`);

  if (health?.ok) {
    // The agent answered, so it is running and the browser trusts it. The
    // socket is refused for a reason above the transport.
    return {
      kind: "origin-blocked",
      title: "The agent is running but refused this page",
      detail:
        `The NFC agent answered at ${base}, so it is running and trusted. It rejected the ` +
        `connection from this site, which means this site is not on its allowed-origins list. ` +
        `Allow ${location.host} on the agent.`,
    };
  }

  if (health) {
    // A response, but not a healthy one. Report what it said rather than
    // guessing; a 403 here is the origin check on the HTTP route.
    return {
      kind: "origin-blocked",
      title: "The agent refused this page",
      detail:
        `The NFC agent answered with ${health.status}. If that is 403, this site is not on its ` +
        `allowed-origins list — allow ${location.host} on the agent.`,
    };
  }

  // Nothing answered. If the site is pointed at https but the agent is serving
  // plain http, the http probe will succeed and name the misconfiguration
  // exactly.
  if (base.startsWith("https://")) {
    const plain = await probe(`${base.replace(/^https:/, "http:")}/api/v1/health`);
    if (plain?.ok) {
      return {
        kind: "wrong-scheme",
        title: "The agent is running without encryption",
        detail:
          `This site is configured for ${base}, but the agent is answering over http on the same ` +
          `port. Point this site at the http address, or start the agent with TLS.`,
      };
    }
  }

  // Two causes remain and the browser will not tell them apart: the agent is
  // not running, or it is running with a certificate this browser does not
  // trust. Opening it directly makes the browser itself answer.
  return {
    kind: "unreachable",
    title: "Can't reach the NFC agent",
    detail:
      "The agent runs on this computer. Either it isn't running, or this browser doesn't trust " +
      "its certificate yet — which looks identical from here. Open it directly to find out: a " +
      "certificate warning means it's running and its certificate needs trusting, and a " +
      "connection error means it isn't running.",
    openUrl: base,
  };
}
