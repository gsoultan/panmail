/// <reference types="vite/client" />

interface ImportMetaEnv {
  // Optional, because it genuinely can be absent: a build outside a git
  // checkout with nothing passed has no version to give. See src/version.ts.
  readonly VITE_APP_VERSION?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
