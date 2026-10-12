# Getting started

In this tutorial we start the agent, connect to it from a short Node.js script,
read a tag off a USB reader and write a text record back to it. It takes about
ten minutes.

You need:

- a PC/SC reader, such as an ACR122U, plugged in
- a writable tag: an NTAG213, NTAG215 or NTAG216 sticker works
- Node.js 22 or later, which has a built-in `WebSocket`

## 1. Install and start the agent

Download the binary for your platform from
[releases](https://github.com/dotside-studios/davi-nfc-agent/releases). On
Linux, install the PC/SC service first, as described in
[Installation](installation.md#linux).

Start it:

```bash
./davi-nfc-agent
```

A tray icon appears. On its first start the agent creates a config directory,
a TLS certificate and an API secret:

| OS | Config directory |
|----|------------------|
| Linux | `~/.config/davi-nfc-agent` |
| macOS | `~/Library/Application Support/davi-nfc-agent` |
| Windows | `%AppData%\davi-nfc-agent` |

## 2. Give the script the secret and the certificate

Every connection presents the API secret, including one from this machine.
Choose **Copy API Secret** in the tray menu, or read it from the file the agent
wrote. The certificate is self-signed, so Node.js is told to trust it with
`NODE_EXTRA_CA_CERTS`. On Linux:

```bash
export DAVI_NFC_SECRET="$(cat ~/.config/davi-nfc-agent/api-secret.txt)"
export NODE_EXTRA_CA_CERTS=~/.config/davi-nfc-agent/tls/server.crt
```

On macOS, use the macOS directory from the table above. In PowerShell:

```powershell
$env:DAVI_NFC_SECRET = Get-Content "$env:APPDATA\davi-nfc-agent\api-secret.txt"
$env:NODE_EXTRA_CA_CERTS = "$env:APPDATA\davi-nfc-agent\tls\server.crt"
```

## 3. Read a tag

Create `tag-demo.mjs`:

```javascript
const secret = process.env.DAVI_NFC_SECRET;
const ws = new WebSocket(`wss://localhost:9470/ws?secret=${encodeURIComponent(secret)}`);

ws.addEventListener("open", () => {
  console.log("Connected. Put a tag on the reader.");
});

ws.addEventListener("message", (event) => {
  const msg = JSON.parse(event.data);

  if (msg.type === "tagData" && msg.payload.uid) {
    const tag = msg.payload;
    console.log(`Tag ${tag.uid} (${tag.type}) says: ${tag.text || "nothing"}`);
  }
});

ws.addEventListener("close", () => console.log("Disconnected"));
```

Run it and put the tag on the reader:

```bash
node tag-demo.mjs
```

```
Connected. Put a tag on the reader.
Tag 04A1B2C3D4E5F6 (NTAG215) says: nothing
```

Every scan reaches every connected client as a `tagData` message. The `uid`
identifies the tag, and `text` is its first text record.

## 4. Write to it

A write names the tag it is for, using the `uid` from `tagData`. Replace the
`message` listener with this one, which writes once and prints the answer:

```javascript
let written = false;

ws.addEventListener("message", (event) => {
  const msg = JSON.parse(event.data);

  if (msg.type === "tagData" && msg.payload.uid) {
    const tag = msg.payload;
    console.log(`Tag ${tag.uid} (${tag.type}) says: ${tag.text || "nothing"}`);

    if (!written && tag.capabilities.canWrite) {
      written = true;
      ws.send(JSON.stringify({
        id: "write-1",
        type: "writeRequest",
        payload: {
          uid: tag.uid,
          records: [{ type: "text", content: "Hello from the tutorial" }],
        },
      }));
    }
  }

  if (msg.type === "writeResponse") {
    console.log(`Wrote ${msg.payload.bytesWritten} bytes to ${msg.payload.uid}`);
  }

  if (msg.type === "error") {
    console.log(`Refused: ${msg.payload.code}: ${msg.error}`);
  }
});
```

Run it again with the tag on the reader:

```
Connected. Put a tag on the reader.
Tag 04A1B2C3D4E5F6 (NTAG215) says: nothing
Wrote 30 bytes to 04A1B2C3D4E5F6
```

Lift the tag and put it back. The agent reads it again:

```
Tag 04A1B2C3D4E5F6 (NTAG215) says: Hello from the tutorial
```

Stop the script with Ctrl+C.

## What next

- Call the agent from a web page: [Connect from a browser](browser-access.md)
- Every message and field: [Client API](client-api.md)
- Use a phone as the reader: [Pair a phone](device-setup.md)
- A typed wrapper over the same protocol: [JavaScript client](javascript-client.md)
