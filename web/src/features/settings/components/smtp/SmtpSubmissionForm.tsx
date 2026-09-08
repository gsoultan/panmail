import React from 'react';
import {
  Modal,
  Stack,
  Group,
  Text,
  Button,
  Switch,
  NumberInput,
  Textarea,
  Alert,
  Code,
  SegmentedControl,
  Checkbox,
  Divider,
  Badge,
} from '@mantine/core';
import { IconAlertTriangle, IconLock, IconTrash } from '@tabler/icons-react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  SmtpBindScope,
  type SmtpSubmission,
} from '../../../../api/panmail/v1/system_settings_pb';
import { settingsService } from '../../../../services/settings';
import {
  initialFormValues,
  problemFor,
  validateForm,
  willHaveTls,
  type SmtpSubmissionFormValues,
} from './submissionRules';

interface SmtpSubmissionFormProps {
  opened: boolean;
  onClose: () => void;
  submission: SmtpSubmission | null | undefined;
}

/** A short, human description of the stored certificate. */
const CertificateSummary: React.FC<{ submission: SmtpSubmission | null | undefined }> = ({
  submission,
}) => {
  const certificate = submission?.certificate;
  if (!certificate) {
    return (
      <Text size="sm" c="dimmed">
        No certificate is installed.
      </Text>
    );
  }

  return (
    <Stack gap={4}>
      <Group gap="xs">
        <Text size="sm">{certificate.subject}</Text>
        {certificate.expired && (
          <Badge color="red" variant="light" size="sm">
            Expired
          </Badge>
        )}
      </Group>
      <Text size="xs" c="dimmed">
        Valid until {certificate.notAfter} · issued by {certificate.issuer}
      </Text>
      {certificate.dnsNames.length > 0 && (
        <Text size="xs" c="dimmed">
          Valid for {certificate.dnsNames.join(', ')}
        </Text>
      )}
      <Text size="xs" c="dimmed">
        SHA-256 <Code>{certificate.fingerprintSha256}</Code>
      </Text>
    </Stack>
  );
};

/**
 * SmtpSubmissionForm opens and closes the SMTP door.
 *
 * The private key is write-only in both directions: the server never sends one
 * back, so the field starts empty and an empty field means "keep what is
 * stored". Removing a certificate is a separate, explicit action, because an
 * empty textarea cannot mean both "unchanged" and "delete it".
 */
export const SmtpSubmissionForm: React.FC<SmtpSubmissionFormProps> = ({
  opened,
  onClose,
  submission,
}) => {
  const queryClient = useQueryClient();
  const [values, setValues] = React.useState<SmtpSubmissionFormValues>(() =>
    initialFormValues(submission),
  );

  // Re-seed whenever the dialog opens, so it never shows the values from a
  // previous edit that was cancelled.
  React.useEffect(() => {
    if (opened) setValues(initialFormValues(submission));
  }, [opened, submission]);

  const hasStoredCertificate = Boolean(submission?.certificate);
  const problems = validateForm(values, hasStoredCertificate);
  const onLoopback = values.bindScope === SmtpBindScope.LOOPBACK;

  const save = useMutation({
    mutationFn: () =>
      settingsService.updateSmtpSubmission({
        enabled: values.enabled,
        bindScope: values.bindScope,
        port: values.port,
        tlsCertificatePem: values.certificatePem.trim(),
        tlsPrivateKeyPem: values.privateKeyPem.trim(),
        allowInsecureAuth: values.allowInsecureAuth,
        clearTls: values.clearTls,
      }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['smtpSubmission'] });
      onClose();
    },
  });

  const update = <K extends keyof SmtpSubmissionFormValues>(
    key: K,
    value: SmtpSubmissionFormValues[K],
  ) => setValues((current) => ({ ...current, [key]: value }));

  // Moving off loopback while plain-text sign-in is on would produce a
  // combination the server refuses. Clearing it with the move is the reading
  // that matches the intent, and the warning below says what happened.
  const changeScope = (scope: SmtpBindScope) =>
    setValues((current) => ({
      ...current,
      bindScope: scope,
      allowInsecureAuth: scope === SmtpBindScope.LOOPBACK ? current.allowInsecureAuth : false,
    }));

  const formProblem = problemFor(problems, 'form');
  const willBeEncrypted = willHaveTls(values, hasStoredCertificate);

  return (
    <Modal opened={opened} onClose={onClose} title="SMTP submission" size="lg">
      <Stack gap="md">
        <Switch
          checked={values.enabled}
          onChange={(event) => update('enabled', event.currentTarget.checked)}
          label="Accept mail over SMTP"
          description="A second door onto the same pipeline. Rate limits, suppressions and sender checks apply exactly as they do to the API."
        />

        <Divider />

        <Stack gap="xs">
          <Text size="sm" fw={500}>
            Reachable from
          </Text>
          <SegmentedControl
            value={String(values.bindScope || SmtpBindScope.LOOPBACK)}
            onChange={(value) => changeScope(Number(value) as SmtpBindScope)}
            data={[
              { label: 'This machine only', value: String(SmtpBindScope.LOOPBACK) },
              { label: 'Any network interface', value: String(SmtpBindScope.ALL_INTERFACES) },
            ]}
          />
          <Text size="xs" c="dimmed">
            {onLoopback
              ? 'Binds 127.0.0.1. Only processes on this host can connect — a sidecar, or a reverse proxy that terminates TLS for you.'
              : 'Binds 0.0.0.0. Anything that can route to this host can connect, so a TLS certificate is required.'}
          </Text>
        </Stack>

        <NumberInput
          label="Port"
          value={values.port}
          onChange={(value) => update('port', typeof value === 'number' ? value : 0)}
          min={1}
          max={65535}
          error={problemFor(problems, 'port')}
          description="587 is the submission port. 25 is refused: it is where a mail exchanger answers, and this gateway is not one."
        />

        <Divider label="TLS" labelPosition="left" />

        <CertificateSummary submission={submission} />

        <Textarea
          label="Certificate (PEM)"
          placeholder={
            hasStoredCertificate
              ? 'Leave empty to keep the installed certificate'
              : '-----BEGIN CERTIFICATE-----'
          }
          autosize
          minRows={3}
          maxRows={6}
          value={values.certificatePem}
          onChange={(event) => update('certificatePem', event.currentTarget.value)}
          error={problemFor(problems, 'certificatePem')}
          description="Include any intermediates, in order."
        />

        <Textarea
          label="Private key (PEM)"
          placeholder={
            hasStoredCertificate
              ? 'Leave empty to keep the installed key'
              : '-----BEGIN PRIVATE KEY-----'
          }
          autosize
          minRows={3}
          maxRows={6}
          value={values.privateKeyPem}
          onChange={(event) => update('privateKeyPem', event.currentTarget.value)}
          error={problemFor(problems, 'privateKeyPem')}
          description="Encrypted before it is stored and never shown again — not here, and not through the API."
        />

        {hasStoredCertificate && (
          <Checkbox
            checked={values.clearTls}
            onChange={(event) => update('clearTls', event.currentTarget.checked)}
            label={
              <Group gap={6}>
                <IconTrash size={14} />
                <Text size="sm">Remove the installed certificate</Text>
              </Group>
            }
          />
        )}

        <Divider />

        <Checkbox
          checked={values.allowInsecureAuth}
          onChange={(event) => update('allowInsecureAuth', event.currentTarget.checked)}
          disabled={!onLoopback}
          label="Accept sign-in without TLS"
          description={
            onLoopback
              ? 'Only for a loopback listener. The password is an API key, so this is safe here and nowhere else.'
              : 'Unavailable: this listener can be reached from other machines.'
          }
          error={problemFor(problems, 'allowInsecureAuth')}
        />

        {values.enabled && !willBeEncrypted && values.allowInsecureAuth && (
          <Alert icon={<IconAlertTriangle size={16} />} color="orange" variant="light">
            <Text size="sm">
              API keys will cross this connection in plain text. That is acceptable on
              loopback, where nothing can be on the path, and nowhere else.
            </Text>
          </Alert>
        )}

        {values.enabled && willBeEncrypted && (
          <Alert icon={<IconLock size={16} />} color="teal" variant="light">
            <Text size="sm">Clients will authenticate over STARTTLS.</Text>
          </Alert>
        )}

        {formProblem && (
          <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
            <Text size="sm">{formProblem}</Text>
          </Alert>
        )}

        {save.isError && (
          <Alert icon={<IconAlertTriangle size={16} />} color="red" variant="light">
            <Text size="sm">{(save.error as Error).message}</Text>
          </Alert>
        )}

        <Group justify="flex-end">
          <Button variant="subtle" onClick={onClose} disabled={save.isPending}>
            Cancel
          </Button>
          <Button
            onClick={() => save.mutate()}
            loading={save.isPending}
            disabled={problems.length > 0}
          >
            Save
          </Button>
        </Group>
      </Stack>
    </Modal>
  );
};
