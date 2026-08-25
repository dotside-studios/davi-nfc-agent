# Read and write your first tag

Build a page that shows a tag when it touches the reader, and writes a URL to
it. About fifteen minutes.

You need the agent built ([installation.md](../installation.md)), an NFC reader
plugged in, and one writable tag — an NTAG213/215/216 sticker or card is ideal.

No Node, no bundler, and no knowledge of the agent's protocol needed.

## 1. Start the agent

From the repository root:

```bash
./davi-nfc-agent
```

The agent puts an icon in your system tray and starts listening on port 9470.
Leave it running.

Confirm it is up by opening <http://localhost:9470/api/v1/health> in a browser.
You will see something like:

```json
{"status":"ok","type":"agent","timestamp":"2026-08-25T09:14:02+08:00","clients":0}
```

If your browser warns about the certificate instead, the agent is running with
TLS. Accept the warning and continue — everything below works the same, with
`https` in place of `http`.

## 2. Make a page

Create a folder next to the repository and copy the client library into it:

```bash
mkdir ~/nfc-tutorial
cp client/dist/nfc-client.js ~/nfc-tutorial/
```

In `~/nfc-tutorial/index.html`, write:

```html
<!doctype html>
<meta charset="utf-8" />
<title>NFC tutorial</title>

<h1>Present a tag</h1>
<pre id="out">not connected</pre>

<script src="nfc-client.js"></script>
<script>
  const out = document.getElementById("out");
  const client = new NFCClient("http://localhost:9470");

  client.on("connected", () => (out.textContent = "waiting for a tag…"));
  client.on("tagData", (tag) => {
    const records = (tag.message?.records ?? [])
      .map((r) => `      ${r.type}: ${r.content ?? "(binary)"}`)
      .join("\n");
    out.textContent = `uid:  ${tag.uid}\ntype: ${tag.type}\nholds:\n${records || "      (nothing)"}`;
  });
  client.on("tagRemoved", () => (out.textContent = "waiting for a tag…"));

  // The agent will refuse this first attempt — step 3 explains why.
  client.connect().catch(() => {});
</script>
```

## 3. Let the agent know about your page

Open a second terminal and serve the folder:

```bash
cd ~/nfc-tutorial && python3 -m http.server 8000
```

Your page is now at <http://localhost:8000>. Open it. It says "not connected"
and stays there. That's expected: the agent refuses connections from pages it
hasn't been told about, so a random site can't drive your reader.

Being refused is what puts your page on the agent's radar. Open the tray menu
and look under **Allowed Origins** — there's now an entry reading:

```
Allow localhost:8000
```

Click it, then reload the page. It reads "waiting for a tag…".

You can skip the round trip next time by starting the agent with the origin
already listed:

```bash
./davi-nfc-agent -allowed-origins "localhost:8000"
```

[Connecting from a web console](../../README.md#connecting-from-a-web-console)
covers the allowlist in full.

## 4. Read a tag

Put a tag on the reader. The page fills in:

```
uid:  04A2B33C4D5E80
type: NTAG215
holds:
      (nothing)
```

The tag is blank, so it holds no records. Take it off the reader and the page
returns to "waiting for a tag…".

Put it back on and leave it there for the next step.

## 5. Write to it

Add a button under the `<pre>`:

```html
<button id="write">Write a URL</button>
```

and a handler in the script, just above `client.connect()`:

```js
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
```

Reload, present the tag, and click **Write a URL**. The page reports something
like:

```
wrote 22 bytes to 04A2B33C4D5E80
```

You never told `write()` which tag to use. The client remembers the tag it last
saw and names it on the request, so the write went to the tag in front of you
and couldn't have gone to another one.

Take the tag off and put it back on. The page now shows the URL you wrote:

```
uid:  04A2B33C4D5E80
type: NTAG215
holds:
      uri: https://example.com
```

Tap the tag with a phone and it will offer to open example.com.

## 6. Fail on purpose

Take the tag off the reader, then click **Write a URL** with nothing on it. The
page reports:

```
write failed: request must name the tag it applies to (uid), or the device holding it (deviceID)
```

The client had no tag to name, so the agent refused instead of guessing. That's
what stops a write meant for one tag from landing on another. See
[Naming the Tag](../api.md#naming-the-tag).

## Next

You have a page that reads tags, writes to them, and won't write to the wrong
one. From here:

- [How-to guides](how-to.md) — recipes for specific tasks: other record kinds,
  locking, raw APDU exchanges, React
- [Reference](reference.md) — every method, event and type
- [api.md](../api.md) — the protocol underneath the library
