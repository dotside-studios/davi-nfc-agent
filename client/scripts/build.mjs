// Builds dist/ from src/: a classic script with globals, an ESM bundle, and
// declarations. Committed, like webui/frontend/dist, so consuming this library
// needs no Node.

import { execFileSync } from "node:child_process";
import { rmSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import * as esbuild from "esbuild";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const entry = resolve(root, "src/index.ts");
const outdir = resolve(root, "dist");

rmSync(outdir, { recursive: true, force: true });

const shared = {
  entryPoints: [entry],
  bundle: true,
  target: "es2020",
  charset: "utf8",
  logLevel: "info",
};

// Assigns its exports onto the page rather than returning them, so
// `new NFCClient(...)` works from an ordinary <script>.
await esbuild.build({
  ...shared,
  format: "iife",
  globalName: "DaviNFC",
  footer: {
    js: "Object.assign(globalThis, DaviNFC);",
  },
  outfile: resolve(outdir, "nfc-client.js"),
});

await esbuild.build({
  ...shared,
  format: "esm",
  outfile: resolve(outdir, "nfc-client.esm.js"),
});

// esbuild does not emit declarations. react/ is left out: a project with React
// imports the source through the package.
execFileSync(
  process.execPath,
  [
    resolve(root, "node_modules/typescript/bin/tsc"),
    "--ignoreConfig",
    "--declaration",
    "--emitDeclarationOnly",
    "--outDir",
    outdir,
    "--rootDir",
    resolve(root, "src"),
    "--target",
    "es2023",
    "--lib",
    "ES2023,DOM",
    "--module",
    "esnext",
    "--moduleResolution",
    "bundler",
    "--strict",
    "--skipLibCheck",
    entry,
  ],
  { cwd: root, stdio: "inherit" },
);
