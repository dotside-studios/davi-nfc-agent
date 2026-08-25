# Tutorial: read and write a tag

Builds a page that shows a tag on the reader and writes a URL to it.

Requires the agent built ([installation](../installation.md)), a reader plugged
in, and one writable tag — NTAG213/215/216 is ideal. No Node or bundler.

## 1. Start the agent

```bash
./davi-nfc-agent -allowed-origins "localhost:8000"
```

It listens on port 9470. Leave it running.

The flag allows the origin the page will be served from. Without it the agent
refuses the connection, which is what stops a random site from driving the
reader — see
[Connecting from a web console](../../README.md#connecting-from-a-web-console).

Check <http://localhost:9470/api/v1/health>:

```json
{"status":"ok","type":"agent","timestamp":"2026-08-25T09:14:02+08:00","clients":0}
```

A certificate warning instead means the agent is running with TLS. Accept it;
everything below works with `https` in place of `http`.

## 2. Build the page

```bash
mkdir ~/nfc-tutorial
cp client/dist/nfc-client.js ~/nfc-tutorial/
```

`~/nfc-tutorial/index.html`:

```html
<!doctype html>
<meta charset="utf-8" />
<title>NFC tutorial</title>

<h1>Present a tag</h1>
<pre id="out">not connected</pre>
<button id="write">Write a URL</button>

<script src="nfc-client.js"></script>
<script>
  const out = document.getElementById("out");
  const client = new NFCClient("http://localhost:9470");

  client.on("connected", () => (out.textContent = "waiting for a tag…"));
  client.on("tagRemoved", () => (out.textContent = "waiting for a tag…"));
  client.on("tagData", (tag) => {
    const records = (tag.message?.records ?? [])
      .map((r) => `      ${r.type}: ${r.content ?? "(binary)"}`)
      .join("\n");
    out.textContent = `uid:  ${tag.uid}\ntype: ${tag.type}\nholds:\n${records || "      (nothing)"}`;
  });

  document.getElementById("write").onclick = async () => {
    try {
      const result = await client.write({
        records: [{ type: "uri", content: "https://example.com" }],
      });
      out.textContent = `wrote ${result.bytesWritten} bytes to ${result.uid}`;
    } catch (err) {
      out.textContent = `write failed: ${err.message}`;
    }
  };

  client.connect().catch((err) => (out.textContent = err.message));
</script>
```

## 3. Serve it

```bash
cd ~/nfc-tutorial && python3 -m http.server 8000
```

Open <http://localhost:8000>. It reads "waiting for a tag…".

## 4. Read a tag

Put a tag on the reader:

```
uid:  04A2B33C4D5E80
type: NTAG215
holds:
      (nothing)
```

Take it off and the page returns to "waiting for a tag…". Put it back on.

## 5. Write to it

Click **Write a URL**:

```
wrote 22 bytes to 04A2B33C4D5E80
```

The call named no tag. The client tracks the tag it last saw and names it, so
the write went to the tag in front of you and could not have gone elsewhere.

Take the tag off and put it back on:

```
uid:  04A2B33C4D5E80
type: NTAG215
holds:
      uri: https://example.com
```

Tapping it with a phone now offers to open example.com.

## 6. Write with no tag present

Take the tag off, then click **Write a URL**:

```
write failed: request must name the tag it applies to (uid), or the device holding it (deviceID)
```

The client had no tag to name, so the agent refused rather than guessing — which
is what stops a write meant for one tag from landing on another. See
[Naming the tag](../api.md#naming-the-tag).

## Next

- [Library docs](README.md) — the rest of the API
- [api.md](../api.md) — the protocol underneath
