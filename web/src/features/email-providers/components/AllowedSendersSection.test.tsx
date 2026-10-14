import { describe, expect, mock, test } from 'bun:test';
import React from 'react';
import { act, fireEvent, render } from '@testing-library/react';
import { MantineProvider } from '@mantine/core';
import { ProviderType } from '../../../api/panmail/v1/provider_type_pb';
import { AllowedSendersSection } from './AllowedSendersSection';

/**
 * A controlled wrapper, so the section sees its own changes the way it does
 * inside the provider form.
 */
const Harness: React.FC<{
  initial?: string[];
  type?: ProviderType;
  onChange?: (next: string[]) => void;
  smtpUsername?: string;
}> = ({ initial = [], type = ProviderType.SES, onChange, smtpUsername }) => {
  const [value, setValue] = React.useState(initial);
  return (
    <AllowedSendersSection
      providerType={type}
      value={value}
      smtpUsername={smtpUsername}
      onChange={(next) => {
        setValue(next);
        onChange?.(next);
      }}
    />
  );
};

const renderSection = async (props: React.ComponentProps<typeof Harness> = {}) => {
  const result = render(
    <MantineProvider>
      <Harness {...props} />
    </MantineProvider>,
  );
  await act(async () => {});
  return result;
};

const input = (container: HTMLElement) =>
  container.querySelector('#allowed-senders-input') as HTMLInputElement;

const type = async (container: HTMLElement, text: string) => {
  await act(async () => {
    fireEvent.change(input(container), { target: { value: text } });
    fireEvent.keyDown(input(container), { key: 'Enter' });
  });
};

describe('AllowedSendersSection', () => {
  test('an empty list reads as open to any sender', async () => {
    const { container } = await renderSection();
    expect(container.textContent).toContain('Any sender');
  });

  test('typing an entry adds it in canonical form', async () => {
    const onChange = mock(() => {});
    const { container } = await renderSection({ onChange });
    await type(container, 'Acme.com');

    expect(onChange).toHaveBeenLastCalledWith(['*@acme.com']);
    expect(container.textContent).toContain('1 allowed');
    expect(container.textContent).toContain('Added "Acme.com" as *@acme.com');
  });

  test('an invalid entry is explained and kept for fixing', async () => {
    const onChange = mock(() => {});
    const { container } = await renderSection({ onChange });
    await type(container, 'billing@');

    expect(onChange).not.toHaveBeenCalled();
    expect(input(container).value).toBe('billing@');
    expect(container.textContent).toContain('is not a full address');
  });

  test('an entry can be removed', async () => {
    const onChange = mock(() => {});
    const { container } = await renderSection({ initial: ['a@x.io', 'b@x.io'], onChange });
    const remove = container.querySelector('[aria-label="Remove a@x.io"]') as HTMLElement;
    await act(async () => {
      fireEvent.click(remove);
    });
    expect(onChange).toHaveBeenLastCalledWith(['b@x.io']);
  });

  test('the checker answers with the rule the gateway applies', async () => {
    const { container } = await renderSection({ initial: ['*@acme.com'] });
    const probe = container.querySelector('#allowed-senders-check') as HTMLInputElement;

    await act(async () => {
      fireEvent.change(probe, { target: { value: 'Ops <ops@acme.com>' } });
    });
    expect(container.textContent).toContain('Allowed by *@acme.com');

    await act(async () => {
      fireEvent.change(probe, { target: { value: 'ops@mail.acme.com' } });
    });
    expect(container.textContent).toContain('Refused');
  });

  test('SMTP offers the signed-in account in one click', async () => {
    const onChange = mock(() => {});
    const { container } = await renderSection({
      type: ProviderType.SMTP,
      smtpUsername: 'notifications@acme.com',
      onChange,
    });
    const button = [...container.querySelectorAll('button')].find((b) =>
      b.textContent?.includes('Add notifications@acme.com'),
    );
    await act(async () => {
      fireEvent.click(button!);
    });
    expect(onChange).toHaveBeenLastCalledWith(['notifications@acme.com']);
  });

  test('IMAP and POP3 do not show the section', async () => {
    for (const t of [ProviderType.IMAP, ProviderType.POP3]) {
      const { container, unmount } = await renderSection({ type: t });
      expect(container.textContent).not.toContain('Allowed senders');
      unmount();
    }
  });
});
