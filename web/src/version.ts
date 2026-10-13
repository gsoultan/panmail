/**
 * The version string the sidebar shows.
 *
 * There is no endpoint serving this, so whatever built the bundle decides what
 * it says. Three things build it, and all three pass the version the same way:
 * goreleaser (`VITE_APP_VERSION={{ .Version }}`), the Dockerfile
 * (`--build-arg VERSION`), and `panmail build --built-ui` (`--version`).
 * When none of them passes one, vite.config.ts asks `git describe` instead, and
 * `make build` and `panmail build` do the same by default, so a local build of a
 * tagged checkout shows that tag too.
 *
 * "The same way" means without a leading `v` — the value matches the
 * `main.Version` ldflag exactly, so the sidebar and `panmail --version` cannot
 * disagree about what is running. The `v` belongs to the display, which is how
 * the CLI already prints it ("Panmail Gateway v%s" in cmd/api/main.go).
 */

/**
 * Renders a build's version for display, adding the `v` only to something that
 * looks like a version number.
 *
 * Unreleased builds carry a word rather than a number — `development` is the
 * default outside a git checkout, and CI builds its image as `ci`. Prefixing
 * those would read `vdevelopment`, so the prefix is conditional. A missing or
 * blank value means nobody passed one and git could not say either, so it
 * reports itself as `development` too rather than inventing a release number.
 */
export function formatVersion(raw: string | undefined): string {
  const version = raw?.trim();
  if (!version) return 'development';
  return /^\d/.test(version) ? `v${version}` : version;
}

export const versionLabel = formatVersion(import.meta.env.VITE_APP_VERSION);
