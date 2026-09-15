import { describe, expect, test } from 'bun:test';
import { formatVersion } from './version';

/**
 * The sidebar used to render `v{VITE_APP_VERSION}` against a value goreleaser
 * passed as a tag. Releases shipped reading `vv0.0.0` — one `v` from the tag,
 * one from the template, and `0.0.0` because the variable the hook expanded was
 * never exported to it. These pin down the shape that fixed it.
 */
describe('formatVersion', () => {
  test('prefixes a release version with a single v', () => {
    // What goreleaser's {{ .Version }} passes for tag v1.6.1.
    expect(formatVersion('1.6.1')).toBe('v1.6.1');
  });

  test('does not double the v when handed a tag', () => {
    expect(formatVersion('v1.6.1')).toBe('v1.6.1');
  });

  test('keeps a snapshot version readable', () => {
    expect(formatVersion('1.6.2-SNAPSHOT-89b206e')).toBe('v1.6.2-SNAPSHOT-89b206e');
  });

  test('leaves a named build unprefixed', () => {
    // `development` is the default in goreleaser, the Dockerfile and
    // `panmail build`; CI builds its image as `ci`. Neither is a number.
    expect(formatVersion('development')).toBe('development');
    expect(formatVersion('ci')).toBe('ci');
  });

  test('reports development rather than inventing a release number', () => {
    // A local `bun run build` sets nothing. The old fallback claimed 1.0.0,
    // which is indistinguishable from an actual release.
    expect(formatVersion(undefined)).toBe('development');
    expect(formatVersion('')).toBe('development');
    expect(formatVersion('   ')).toBe('development');
  });
});
