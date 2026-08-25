import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The agent this dev server proxies to. Override when the agent runs on a
// non-default port: VITE_AGENT=https://localhost:9480 npm run dev
const agent = process.env.VITE_AGENT ?? 'http://localhost:9470'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],

  resolve: {
    alias: {
      // The console is a consumer of the agent's own client library, under the
      // same name the production apps import it by. Aliased to the source in
      // this repo rather than an installed copy, so a protocol change that
      // breaks a consumer breaks the console's build in the same commit.
      '@davi/nfc-agent-client': fileURLToPath(
        new URL('../../client/src/index.ts', import.meta.url),
      ),
    },
  },

  // Relative asset URLs. The console is embedded in the agent binary and served
  // from its root, but a relative base also survives being mounted somewhere
  // else without a rebuild.
  base: './',

  build: {
    // Committed to the repo and embedded with go:embed, so `go build .` works
    // without Node. See webui/frontend/.gitignore.
    outDir: 'dist',
    // Everything ends up inside the binary, so an extra request costs a memcpy
    // rather than a round trip. Inlining keeps the served page to two files.
    assetsInlineLimit: 4096,
    sourcemap: false,
  },

  server: {
    port: 5273,
    proxy: {
      // The control API and both WebSocket endpoints are proxied to a real
      // agent, so the dev server drives actual hardware rather than a mock.
      //
      // The control surface refuses anything that is not loopback and
      // same-origin, so during development the agent sees the proxy — itself
      // loopback — and the Origin header is rewritten to match.
      '/control': {
        target: agent,
        changeOrigin: true,
        secure: false,
        ws: true,
      },
      '/ws': {
        target: agent,
        changeOrigin: true,
        secure: false,
        ws: true,
      },
    },
  },
})
