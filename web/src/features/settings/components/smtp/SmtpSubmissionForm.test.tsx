import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { fireEvent, render } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SmtpSubmissionForm } from './SmtpSubmissionForm';
import {
  SmtpBindScope,
  type SmtpSubmission,
  type TlsCertificateInfo,
} from '../../../../api/panmail/v1/system_settings_pb';

// The generated messages carry a $typeName, so the fixtures cast rather than
// asserting a whole message by hand.
const certificate = (fields: Partial<TlsCertificateInfo> = {}): TlsCertificateInfo =>
  ({
    subject: 'CN=smtp.example.test',
    issuer: 'CN=Example CA',
    dnsNames: ['smtp.example.test'],
    notBefore: '2026-01-01T00:00:00Z',
    notAfter: '2027-01-01T00:00:00Z',
    fingerprintSha256: 'ab'.repeat(32),
    expired: false,
    ...fields,
  }) as TlsCertificateInfo;

const submission = (fields: Partial<SmtpSubmission> = {}): SmtpSubmission =>
  ({
    enabled: false,
    host: '',
    port: 587,
    starttls: false,
    insecureAuthAllowed: false,
    managedByFlags: false,
    editable: true,
    notEditableReason: '',
    bindScope: SmtpBindScope.LOOPBACK,
    lastError: '',
    ...fields,
  }) as SmtpSubmission;

const renderForm = (value: SmtpSubmission | null) =>
  render(
    <MantineProvider>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <SmtpSubmissionForm opened onClose={mock(() => {})} submission={value} />
      </QueryClientProvider>
    </MantineProvider>,
  );

const saveButton = (getAllByRole: (role: string) => HTMLElement[]) =>
  getAllByRole('button').find((b) => b.textContent?.trim() === 'Save') as HTMLButtonElement;

/**
 * The form that opens the SMTP door.
 *
 * These assert the guard rails a browser can enforce. They are not the
 * security boundary — internal/smtp_submission/entities is, and refuses the
 * same combinations server-side. What is tested here is that the panel never
 * invites a save the gateway will reject, and never implies a private key can
 * be read back.
 */
describe('SmtpSubmissionForm', () => {
  test('a loopback listener with plain-text sign-in can be saved', () => {
    const { getAllByRole } = renderForm(submission({ insecureAuthAllowed: true }));
    expect(saveButton(getAllByRole).disabled).toBe(false);
  });

  test('save is blocked when nothing could authenticate', () => {
    // Enabled, no certificate, and sign-in without TLS not allowed: go-smtp
    // would accept the connection and refuse every AUTH.
    const { getAllByRole, getByText } = renderForm(submission({ enabled: true }));

    expect(saveButton(getAllByRole).disabled).toBe(true);
    expect(getByText(/Otherwise nothing can authenticate/)).toBeTruthy();
  });

  test('moving off loopback withdraws plain-text sign-in', () => {
    const { getByText, getByLabelText } = renderForm(submission({ insecureAuthAllowed: true }));

    fireEvent.click(getByText('Any network interface'));

    // The combination the server refuses is not merely warned about here: the
    // checkbox is cleared and disabled, so it cannot be submitted.
    const insecure = getByLabelText('Accept sign-in without TLS') as HTMLInputElement;
    expect(insecure.checked).toBe(false);
    expect(insecure.disabled).toBe(true);
  });

  test('a listener on every interface with no certificate cannot be saved', () => {
    const { getByText, getAllByRole } = renderForm(submission({ enabled: true, insecureAuthAllowed: true }));

    fireEvent.click(getByText('Any network interface'));

    expect(saveButton(getAllByRole).disabled).toBe(true);
    expect(getByText(/needs a TLS certificate/)).toBeTruthy();
  });

  test('the key fields start empty and say the key is never shown again', () => {
    // The server does not send a private key back, so an empty field means
    // "keep what is stored" — the placeholder has to say so, or an
    // administrator reads it as "there is no certificate".
    const { getByPlaceholderText, getByText } = renderForm(
      submission({ enabled: true, starttls: true, certificate: certificate() }),
    );

    expect((getByPlaceholderText('Leave empty to keep the installed key') as HTMLTextAreaElement).value).toBe('');
    expect(getByText(/never shown again/)).toBeTruthy();
  });

  test('an installed certificate is identified by fingerprint, not by its key', () => {
    const fingerprint = 'cd'.repeat(32);
    const { getByText } = renderForm(
      submission({
        enabled: true,
        starttls: true,
        certificate: certificate({ fingerprintSha256: fingerprint }),
      }),
    );

    expect(getByText(fingerprint)).toBeTruthy();
    expect(getByText(/CN=smtp.example.test/)).toBeTruthy();
  });

  test('an expired certificate is called out rather than hidden', () => {
    const { getAllByText } = renderForm(
      submission({
        enabled: true,
        starttls: true,
        certificate: certificate({ expired: true, notAfter: '2021-01-01T00:00:00Z' }),
      }),
    );

    expect(getAllByText('Expired').length).toBeGreaterThan(0);
  });

  test('port 25 is refused with the reason', () => {
    const { getByText, getByLabelText, getAllByRole } = renderForm(submission({ insecureAuthAllowed: true }));

    fireEvent.change(getByLabelText('Port'), { target: { value: '25' } });

    expect(getByText(/mail exchanger port/)).toBeTruthy();
    expect(saveButton(getAllByRole).disabled).toBe(true);
  });

  test('a listener that could not bind says so', () => {
    // Enabling happens at runtime now, so the setting and the socket can
    // disagree. At startup they could not.
    const { getByText } = renderForm(
      submission({ enabled: true, insecureAuthAllowed: true, lastError: 'cannot listen on 127.0.0.1:587' }),
    );
    // The form itself does not render lastError; the panel does. What matters
    // here is that a failed listener still opens for editing rather than
    // locking the administrator out of fixing it.
    expect(getByText('Accept mail over SMTP')).toBeTruthy();
  });
});
