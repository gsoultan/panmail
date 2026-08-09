import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { ProviderForm } from './ProviderForm';

/**
 * The load-edit-save path through the real component.
 *
 * providerFormValues.test.ts covers the transforms in isolation; this covers
 * them being wired together, which is where the bug actually lived. The form
 * looked correct — the DKIM switch read on — because the section derived the
 * checkbox from the values it was shown. The form state behind it still held
 * undefined, and that is what submit read.
 */

/**
 * Renders and lets the layout effects settle.
 *
 * Mantine's Popover and FloatingIndicator measure themselves after mount and
 * set state from the result, which lands outside the act() that render wraps.
 * Flushing here keeps that update inside one, so the warning does not bury a
 * real failure in the output.
 */
const renderForm = async (props: Parameters<typeof ProviderForm>[0]) => {
  const result = render(
    <MantineProvider>
      <ProviderForm {...props} />
    </MantineProvider>,
  );
  await act(async () => {});
  return result;
};

const submit = async (container: HTMLElement) => {
  const form = container.querySelector('form');
  if (!form) throw new Error('no form rendered');
  // fireEvent rather than a hand-built Event: the global Event here is Bun's,
  // and jsdom's dispatchEvent rejects anything not built from its own realm.
  await act(async () => {
    fireEvent.submit(form);
  });
};

// A provider as the API returns it: config in a oneof, secrets redacted.
const savedSmtpProvider = (config: Record<string, unknown>) => ({
  id: 'prov_123',
  name: 'Primary SMTP',
  type: ProviderType.SMTP,
  config: {
    case: 'smtp',
    value: {
      host: 'smtp.example.com',
      port: 587,
      username: 'postmaster@example.com',
      password: '',
      useSsl: true,
      skipVerify: false,
      ...config,
    },
  },
});

describe('editing a saved provider', () => {
  test('saving a DKIM-signed provider untouched keeps its signing config', async () => {
    const onSubmit = mock(() => {});
    const provider = savedSmtpProvider({
      dkim: { domain: 'example.com', selector: 's1', privateKey: '' },
    });

    const { container } = await renderForm({ initialValues: provider, onSubmit });
    await submit(container);

    expect(onSubmit).toHaveBeenCalledTimes(1);
    const sent = (onSubmit.mock.calls[0] as unknown as [any])[0];
    expect(sent.smtp.dkim.domain).toBe('example.com');
    expect(sent.smtp.dkim.selector).toBe('s1');
  });

  test('saving an OAuth provider untouched keeps its credentials', async () => {
    const onSubmit = mock(() => {});
    const provider = savedSmtpProvider({
      oauth2: {
        mechanism: 'XOAUTH2',
        clientId: 'client-id',
        clientSecret: '',
        refreshToken: '',
        tokenEndpoint: 'https://login.example/token',
        scope: '',
      },
    });

    const { container } = await renderForm({ initialValues: provider, onSubmit });
    await submit(container);

    const sent = (onSubmit.mock.calls[0] as unknown as [any])[0];
    expect(sent.smtp.oauth2.clientId).toBe('client-id');
    expect(sent.smtp.oauth2.tokenEndpoint).toBe('https://login.example/token');
  });

  test('the form-only switches are never sent to the API', async () => {
    const onSubmit = mock(() => {});
    const { container } = await renderForm({ initialValues: savedSmtpProvider({}), onSubmit });
    await submit(container);

    const sent = (onSubmit.mock.calls[0] as unknown as [any])[0];
    expect(sent.smtp).not.toHaveProperty('dkimEnabled');
    expect(sent.smtp).not.toHaveProperty('authMode');
  });

  test('a provider with neither feature saves them cleared', async () => {
    const onSubmit = mock(() => {});
    const { container } = await renderForm({ initialValues: savedSmtpProvider({}), onSubmit });
    await submit(container);

    const sent = (onSubmit.mock.calls[0] as unknown as [any])[0];
    expect(sent.smtp.dkim).toEqual({ domain: '', selector: '', privateKey: '' });
    expect(sent.smtp.oauth2.clientId).toBe('');
  });

  test('the existing host and username survive the round trip', async () => {
    const onSubmit = mock(() => {});
    const { container } = await renderForm({ initialValues: savedSmtpProvider({}), onSubmit });
    await submit(container);

    const sent = (onSubmit.mock.calls[0] as unknown as [any])[0];
    expect(sent.smtp.host).toBe('smtp.example.com');
    expect(sent.smtp.username).toBe('postmaster@example.com');
    expect(sent.smtp.port).toBe(587);
  });
});

describe('validation', () => {
  test('a provider with too short a name does not submit', async () => {
    const onSubmit = mock(() => {});
    const { container } = await renderForm({
      initialValues: { ...savedSmtpProvider({}), name: 'x' },
      onSubmit,
    });
    await submit(container);

    expect(onSubmit).not.toHaveBeenCalled();
  });

  test('a new provider starts empty and so does not submit', async () => {
    const onSubmit = mock(() => {});
    const { container } = await renderForm({ onSubmit });
    await submit(container);

    expect(onSubmit).not.toHaveBeenCalled();
  });
});

describe('rendering', () => {
  test('an SMTP provider shows the DKIM and OAuth sections', async () => {
    const { container } = await renderForm({ initialValues: savedSmtpProvider({}), onSubmit: () => {} });
    expect(container.textContent).toContain('DKIM');
  });

  // Which fields exist depends on the selected protocol, so the value
  // subscription has to drive a re-render.
  test('an API provider shows its own fields instead', async () => {
    const { container } = await renderForm({
      initialValues: {
        id: 'p1',
        name: 'SendGrid',
        type: ProviderType.SENDGRID,
        config: { case: 'sendgrid', value: { apiKey: '', baseUrl: '' } },
      },
      onSubmit: () => {},
    });
    expect(container.textContent).not.toContain('DKIM');
  });
});
