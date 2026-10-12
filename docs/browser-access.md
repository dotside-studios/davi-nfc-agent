# Connect from a browser

A web page can read and write tags through the agent on the operator's
machine. Three things must be in place, and a failure of any of them looks the
same to the page: the connection does not open. This guide sets up each one,
then connects. For why the agent asks for each, see [Security](security.md).

## 1. Allow the page's origin

The agent admits a browser connection only from an origin on its allowlist.
`davi.social`, `shop.davi.social`, `localhost:3000` and `localhost:3002` are
allowed out of the box. For any other origin, either:

- open the page and let it try to connect, then choose **Allow
  console.example.com** from the tray, which saves it; or
- start the agent with the origin, which also saves it:

  ```bash
  ./davi-nfc-agent -allowed-origins "console.example.com,localhost:5173"
  ```

Entries are matched on `host:port`, and a full URL is reduced to it. The
Control Center's **Security → Browser origins** lists the allowlist and every
origin refused since the agent started.

## 2. Make the browser trust the agent's certificate

The agent's certificate is self-signed, and a browser refuses a `wss://`
connection to a certificate it does not trust, without a warning to click
through. Choose one:

- **Provide a certificate** for a name you control that resolves to the agent,
  issued by a public authority:

  ```bash
  ./davi-nfc-agent -cert agent.example.com.pem -key agent.example.com-key.pem
  ```

  Connect to `wss://agent.example.com:9470`. Nothing is installed.

- **Install a local certificate authority** on the operator's machine:

  ```bash
  ./davi-nfc-agent -install-ca
  ```

  or choose **Trust this agent in browsers** in the Control Center under
  **Security → Device trust**. Connect to `wss://localhost:9470`. The authority
  can vouch for any name to that machine, so prefer a provided certificate
  where you can arrange one.

Check it by opening `https://localhost:9470/api/v1/health` in the browser. A
JSON answer with no warning means the certificate is trusted.

## 3. Give the page the API secret

Every connection presents the API secret. The operator copies it from the tray
with **Copy API Secret**. A page served to many operators cannot carry it, so
ask for it once and keep it on that machine, for instance in `localStorage`.

## 4. Connect

Copy the client library into the site. It is built into `client/dist` in this
repository:

```bash
cp client/dist/nfc-client.esm.js your-site/
```

```html
<script type="module">
  import { NFCClient, diagnoseAgent } from "./nfc-client.esm.js";

  const AGENT = "https://localhost:9470";
  const client = new NFCClient(AGENT, { apiSecret: localStorage.getItem("nfcAgentSecret") });

  client.on("tagData", (tag) => {
    document.querySelector("#uid").textContent = tag.uid;
  });

  try {
    await client.connect();
  } catch {
    const why = await diagnoseAgent(AGENT);
    document.querySelector("#status").textContent = `${why.title}. ${why.detail}`;
  }
</script>
```

A failed WebSocket carries no reason, so `diagnoseAgent` probes the agent over
HTTP to name the likely one:

| `kind` | Cause | Fix |
|--------|-------|-----|
| `origin-blocked` | The agent answered, so it is running and trusted, and refused this origin | Step 1 |
| `wrong-scheme` | The page uses `https://`, the agent serves plain HTTP | Point the page at `http://`, or start the agent with TLS |
| `unreachable` | Not running, or its certificate is not trusted. `openUrl` is set | Open `openUrl`: a certificate warning means step 2, a connection error means the agent is not running |

`diagnoseAgent` cannot see a wrong secret, which also fails the connection.
If it reports `origin-blocked` for an origin that is on the allowlist, check
the secret.

The full client API is in [JavaScript client](javascript-client.md), and the
protocol under it in [Client API](client-api.md).
