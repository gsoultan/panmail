/** A data encryption key: 32 bytes as 64 hex characters. */
export const DATA_KEY_PATTERN = /^[0-9a-fA-F]{64}$/;

/** A new key from the browser's CSPRNG. */
export function generateDataKey(): string {
  const bytes = new Uint8Array(32);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
}
