import React from 'react';
import { Modal, Group, ThemeIcon, Text } from '@mantine/core';
import { IconSend } from '@tabler/icons-react';
import { SendEmailForm } from './SendEmailForm';
import { notifications } from '@mantine/notifications';
import { emailProviderService } from '../services/emailProvider';
import { useMutation } from '@tanstack/react-query';

interface SendEmailModalProps {
  opened: boolean;
  onClose: () => void;
  onSubmit?: (values: any) => void;
  loading?: boolean;
  initialTemplateId?: string;
  initialProviderId?: string;
}

export const SendEmailModal: React.FC<SendEmailModalProps> = ({
  opened,
  onClose,
  onSubmit,
  loading: externalLoading,
  initialTemplateId,
  initialProviderId
}) => {
  const sendMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.sendEmail(values),
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Email sent successfully!', color: 'green' });
      onClose();
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to send email', color: 'red' });
    },
  });

  const handleSubmit = (values: any) => {
    if (onSubmit) {
      onSubmit(values);
    } else {
      sendMutation.mutate(values);
    }
  };

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color="brand" size="md">
            <IconSend size={18} />
          </ThemeIcon>
          <Text fw={800} size="lg">Test Email Delivery</Text>
        </Group>
      }
      centered
      size="xl"
      radius="md"
    >
      <SendEmailForm
        onSubmit={handleSubmit}
        loading={externalLoading || sendMutation.isPending}
        initialTemplateId={initialTemplateId}
        initialProviderId={initialProviderId}
      />
    </Modal>
  );
};
