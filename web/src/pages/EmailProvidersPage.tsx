import React, { useState, useEffect } from 'react';
import { Container, Title, Button, Group, Stack, useComputedColorScheme, Modal, Box, Text, rem, ThemeIcon, Select } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconMail, IconPlus, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { ProviderList } from '../features/email-providers/components/ProviderList';
import { ProviderForm } from '../features/email-providers/components/ProviderForm';
import { SendEmailModal } from '../features/email-providers/components/SendEmailModal';
import { emailProviderService } from '../features/email-providers/services/emailProvider';
import type { EmailProvider } from '../api/panmail/v1/email_provider_pb';
import type { TestEmailProviderResponse } from '../api/panmail/v1/email_provider_service_pb';

export const EmailProvidersPage: React.FC = () => {
  const queryClient = useQueryClient();
  const [isModalOpen, setIsModalOpen] = useState(false);
  const [isSendModalOpen, setIsSendModalOpen] = useState(false);
  const [selectedProvider, setSelectedProvider] = useState<EmailProvider | null>(null);
  const [providerForSend, setProviderForSend] = useState<string | null>(null);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['emailProviders', pageToken, pageSize],
    queryFn: () => emailProviderService.listProviders(Number(pageSize), pageToken),
  });

  const providers = data?.providers ?? [];
  const nextPageToken = data?.nextPageToken;

  const handleNext = () => {
    if (nextPageToken) {
      setHistory([...history, pageToken ?? '']);
      setPageToken(nextPageToken);
    }
  };

  const handlePrev = () => {
    const newHistory = [...history];
    const prev = newHistory.pop();
    setHistory(newHistory);
    setPageToken(prev === '' ? undefined : prev);
  };

  const createMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.createProvider(values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emailProviders'] });
      notifications.show({ title: 'Success', message: 'Email provider created successfully', color: 'green' });
      setIsModalOpen(false);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to create provider', color: 'red' });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.updateProvider(selectedProvider!.id, values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emailProviders'] });
      notifications.show({ title: 'Success', message: 'Email provider updated successfully', color: 'green' });
      setIsModalOpen(false);
      setSelectedProvider(null);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to update provider', color: 'red' });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: (id: string) => emailProviderService.deleteProvider(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['emailProviders'] });
      notifications.show({ title: 'Success', message: 'Email provider deleted successfully', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to delete provider', color: 'red' });
    },
  });

  const testMutation = useMutation({
    mutationFn: (id: string) => emailProviderService.testProvider(id),
    onSuccess: (res: TestEmailProviderResponse) => {
      if (res.success) {
        notifications.show({ title: 'Success', message: 'Connection test passed!', color: 'green' });
      } else {
        notifications.show({ title: 'Test Failed', message: res.errorMessage, color: 'red' });
      }
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to test provider', color: 'red' });
    },
  });

  const testConfigMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.testProviderConfig(values),
    onSuccess: (res: TestEmailProviderResponse) => {
      if (res.success) {
        notifications.show({ title: 'Success', message: 'Connection test passed!', color: 'green' });
      } else {
        notifications.show({ title: 'Test Failed', message: res.errorMessage, color: 'red' });
      }
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to test configuration', color: 'red' });
    },
  });

  const sendMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.sendEmail(values),
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Email sent successfully!', color: 'green' });
      setIsSendModalOpen(false);
      setProviderForSend(null);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to send email', color: 'red' });
    },
  });

  const handleCreate = () => {
    setSelectedProvider(null);
    setIsModalOpen(true);
  };

  const handleEdit = (provider: EmailProvider) => {
    setSelectedProvider(provider);
    setIsModalOpen(true);
  };

  const handleSendTest = (id: string) => {
    setProviderForSend(id);
    setIsSendModalOpen(true);
  };

  const handleSubmit = (values: any) => {
    if (selectedProvider) {
      updateMutation.mutate(values);
    } else {
      createMutation.mutate(values);
    }
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconMail size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Email Providers</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Manage and test your email delivery infrastructure.</Text>
          </Box>
          <Group gap="md">
            <Select
              label="Page Size"
              size="xs"
              data={['10', '20', '50', '100']}
              value={pageSize}
              onChange={setPageSize}
              style={{ width: rem(80) }}
            />
            <Button
              onClick={handleCreate}
              leftSection={<IconPlus size={18} />}
              color="brand"
              radius="md"
              size="sm"
            >
              Add Provider
            </Button>
          </Group>
        </Group>

        <Box pos="relative">
          <ProviderList
            providers={providers}
            onEdit={handleEdit}
            onDelete={(id) => deleteMutation.mutate(id)}
            onTest={(id) => testMutation.mutate(id)}
            onSendTest={handleSendTest}
          />
          
          {(nextPageToken || history.length > 0) && (
            <Group justify="space-between" align="center" mt="md">
              <Text size="sm" c="dimmed" fw={500}>
                Showing page <Text span fw={700} c="brand">{history.length + 1}</Text>
              </Text>
              <Group gap="xs">
                <Button 
                  variant="light" 
                  onClick={handlePrev} 
                  disabled={history.length === 0}
                  leftSection={<IconChevronLeft size={14} />}
                  size="xs"
                  radius="md"
                >
                  Previous
                </Button>
                <Button 
                  variant="light" 
                  onClick={handleNext} 
                  disabled={!nextPageToken}
                  rightSection={<IconChevronRight size={14} />}
                  size="xs"
                  radius="md"
                >
                  Next
                </Button>
              </Group>
            </Group>
          )}
        </Box>
      </Stack>

      <Modal
        opened={isModalOpen}
        onClose={() => setIsModalOpen(false)}
        title={
          <Text fw={800} size="lg">
            {selectedProvider ? 'Edit Email Provider' : 'Add Email Provider'}
          </Text>
        }
        size="lg"
        radius="md"
      >
        <ProviderForm
          initialValues={selectedProvider}
          onSubmit={handleSubmit}
          onTest={(values) => testConfigMutation.mutate(values)}
          loading={createMutation.isPending || updateMutation.isPending}
          testing={testConfigMutation.isPending}
        />
      </Modal>

      <SendEmailModal
        opened={isSendModalOpen}
        onClose={() => setIsSendModalOpen(false)}
        initialProviderId={providerForSend || undefined}
        onSubmit={(values) => sendMutation.mutate(values)}
        loading={sendMutation.isPending}
      />
    </Container>
  );
};
