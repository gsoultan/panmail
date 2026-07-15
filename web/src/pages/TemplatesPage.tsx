import React, { useState, useEffect } from 'react';
import { Container, Title, Button, Group, Stack, Box, Text, rem, ThemeIcon, Table, ActionIcon, Badge, useComputedColorScheme, Select } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconTemplate, IconPlus, IconEdit, IconTrash, IconSend, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { templateService } from '../features/templates/services/template';
import { SendEmailModal } from '../features/email-providers/components/SendEmailModal';
import { emailProviderService } from '../features/email-providers/services/emailProvider';
import type { Template } from '../api/panmail/v1/template_pb';

export const TemplatesPage: React.FC = () => {
  const queryClient = useQueryClient();
  const navigate = useNavigate();
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('10');

  const [isSendModalOpen, setIsSendModalOpen] = useState(false);
  const [templateForSend, setTemplateForSend] = useState<string | null>(null);

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['templates', pageToken, pageSize],
    queryFn: () => templateService.listTemplates(Number(pageSize), pageToken),
  });

  const templates = data?.templates ?? [];
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

  const deleteMutation = useMutation({
    mutationFn: (id: string) => templateService.deleteTemplate(id),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['templates'] });
      notifications.show({ title: 'Success', message: 'Template deleted successfully', color: 'green' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to delete template', color: 'red' });
    },
  });

  const handleCreate = () => {
    navigate({ to: '/templates/new' });
  };

  const handleEdit = (template: Template) => {
    navigate({ to: `/templates/${template.id}/edit` });
  };

  const sendMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.sendEmail(values),
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Email sent successfully!', color: 'green' });
      setIsSendModalOpen(false);
      setTemplateForSend(null);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to send email', color: 'red' });
    },
  });

  const handleSendTest = (id: string) => {
    setTemplateForSend(id);
    setIsSendModalOpen(true);
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconTemplate size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Email Templates</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Design and manage reusable email templates with Handlebars support.</Text>
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
              Create Template
            </Button>
          </Group>
        </Group>

        <Box style={{
          backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
          borderRadius: rem(12),
          border: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))',
          overflow: 'hidden'
        }}>
          <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))' }}>
              <Table.Tr>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Template Name</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Subject</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">ID</Text></Table.Th>
                <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {templates.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={4} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No templates found. Create your first template to get started.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                templates.map((template) => (
                  <Table.Tr key={template.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{template.name}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{template.subject}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color="gray" radius="sm" tt="none">{template.id}</Badge>
                    </Table.Td>
                    <Table.Td>
                      <Group gap="xs" justify="flex-end">
                        <ActionIcon variant="light" color="green" onClick={() => handleSendTest(template.id)} title="Send Test Email">
                          <IconSend size={16} />
                        </ActionIcon>
                        <ActionIcon variant="light" color="brand" onClick={() => handleEdit(template)}>
                          <IconEdit size={16} />
                        </ActionIcon>
                        <ActionIcon variant="light" color="red" onClick={() => {
                          if (window.confirm('Are you sure you want to delete this template?')) {
                            deleteMutation.mutate(template.id);
                          }
                        }}>
                          <IconTrash size={16} />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Box>

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
      </Stack>

      <SendEmailModal
        opened={isSendModalOpen}
        onClose={() => setIsSendModalOpen(false)}
        initialTemplateId={templateForSend || undefined}
        onSubmit={(values) => sendMutation.mutate(values)}
        loading={sendMutation.isPending}
      />
    </Container>
  );
};
