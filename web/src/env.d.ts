/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Optional, because it genuinely can be absent: a plain `bun run build`
  // passes nothing, and only the release paths set it. See src/version.ts.
  readonly VITE_APP_VERSION?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
