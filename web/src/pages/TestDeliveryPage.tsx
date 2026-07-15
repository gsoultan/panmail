import React from 'react';
import { Container, Title, Text, Group, Stack, rem, ThemeIcon, Box, Paper } from '@mantine/core';
import { IconSend } from '@tabler/icons-react';
import { notifications } from '@mantine/notifications';
import { useMutation } from '@tanstack/react-query';
import { SendEmailForm } from '../features/email-providers/components/SendEmailForm';
import { emailProviderService } from '../features/email-providers/services/emailProvider';

export const TestDeliveryPage: React.FC = () => {
  const sendMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.sendEmail(values),
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Email sent successfully!', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to send email', color: 'red' });
    },
  });

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="brand" size="md">
              <IconSend size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Email Delivery Test</Title>
          </Group>
          <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>
            Test your email delivery setup by sending a real email through any configured provider.
          </Text>
        </Box>

        <Paper withBorder radius="md" p={0} style={{ overflow: 'hidden' }}>
          <Box p="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))" style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
             <Text fw={700} size="sm">SEND TEST EMAIL</Text>
          </Box>
          <Box p="xl">
            <SendEmailForm
              onSubmit={(values) => sendMutation.mutate(values)}
              loading={sendMutation.isPending}
            />
          </Box>
        </Paper>
      </Stack>
    </Container>
  );
};
