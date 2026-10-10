/**
 * The shortest password the gateway accepts for any account.
 *
 * It mirrors MinPasswordLength in internal/auth/usecases. The setup wizard used
 * to allow six characters while the server wanted twelve, and the server only
 * found out after it had written its configuration — which locked the instance
 * with nobody able to sign in. The forms check the same number so a password
 * the server will refuse never leaves the browser.
 */
export const MIN_PASSWORD_LENGTH = 12;

/** A form validator: an error message, or null when the password is long enough. */
export function passwordLengthError(value: string): string | null {
  return value.length < MIN_PASSWORD_LENGTH
    ? `Password must be at least ${MIN_PASSWORD_LENGTH} characters`
    : null;
}
