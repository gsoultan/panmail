import { describe, expect, test } from 'bun:test';
import { MIN_PASSWORD_LENGTH, passwordLengthError } from './password';

describe('passwordLengthError', () => {
  test('agrees with the server minimum', () => {
    // internal/auth/usecases.MinPasswordLength. A form that accepts less is a
    // form whose submissions the server refuses.
    expect(MIN_PASSWORD_LENGTH).toBe(12);
  });

  test.each([
    ['', true],
    ['secret', true], // the setup wizard's old minimum
    ['12345678901', true],
    ['123456789012', false],
    ['a-long-enough-password', false],
  ])('%p refused: %p', (value, refused) => {
    expect(passwordLengthError(value) !== null).toBe(refused);
  });
});
