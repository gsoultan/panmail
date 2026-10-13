import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { execFileSync } from 'node:child_process'

// In development Vite serves the app and forwards the gateway's own routes to
// the Go process, which is what lets the UI keep HMR while still talking to a
// real backend. The ConnectRPC client uses an empty baseUrl (src/services/client.ts),
// so every request goes to whichever origin served the page — in production the
// gateway itself, here Vite, which is why the proxy has to exist.
//
// Both values are set by scripts/dev/frontend.sh.
const apiTarget = process.env.PANMAIL_API_TARGET || 'http://127.0.0.1:8080'
const devPort = Number(process.env.PANMAIL_DEV_WEB_PORT) || 5173

// The routes the gateway owns, from the mux in cmd/api/main.go. ConnectRPC
// procedures are addressed as /<proto package>.<Service>/<Method>, so they are
// matched by the `panmail.v1.` package prefix rather than listed one by one —
// a new service then needs no change here. Anything unmatched stays with Vite,
// including the SPA routes.
const gatewayRoutes = [
  'panmail\\.v1\\.',
  'grpc\\.health\\.v1\\.',
  'healthz',
  'webhooks/',
  'inbound/',
  'track/',
].join('|')

// The version the sidebar shows (src/version.ts). Release paths pass it in
// VITE_APP_VERSION and that always wins. Anything else, a plain `bun run build`
// or the dev server, asks git instead, so a build from a tagged checkout reads
// as that tag rather than as `development`. `git describe` gives `v2.2.2` on the
// tag itself and `v2.2.2-3-gabc1234` three commits past it; the leading `v` is
// dropped to match what goreleaser passes. Outside a git checkout (the Docker
// build context has no .git) it stays unset and the UI says `development`.
function gitVersion(): string | undefined {
  try {
    return execFileSync('git', ['describe', '--tags', '--always', '--dirty'], {
      encoding: 'utf8',
      stdio: ['ignore', 'pipe', 'ignore'],
    })
      .trim()
      .replace(/^v/, '') || undefined
  } catch {
    return undefined
  }
}
const appVersion = process.env.VITE_APP_VERSION?.trim() || gitVersion()

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  define: appVersion
    ? { 'import.meta.env.VITE_APP_VERSION': JSON.stringify(appVersion) }
    : {},
  server: {
    port: devPort,
    // Fail rather than silently moving to the next port: the scripts print the
    // URL they expect, and a server on a different one makes that a lie.
    strictPort: true,
    proxy: {
      [`^/(${gatewayRoutes})`]: {
        target: apiTarget,
        changeOrigin: false,
      },
    },
  },
  build: {
    rollupOptions: {
      output: {
        manualChunks(id) {
          if (id.includes('node_modules')) {
            if (id.includes('@mantine')) {
              return 'mantine';
            }
            if (id.includes('@tabler/icons-react')) {
              return 'icons';
            }
            if (id.includes('recharts') || id.includes('d3')) {
              return 'charts';
            }
            if (id.includes('react') || id.includes('@tanstack') || id.includes('zustand')) {
              return 'vendor';
            }
            return 'others';
          }
        }
      }
    },
    chunkSizeWarningLimit: 600, // Optional: slightly increase warning limit if needed, but aim for < 500kb
  }
})
