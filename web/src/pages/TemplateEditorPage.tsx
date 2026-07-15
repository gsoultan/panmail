import React from 'react';
import { Container, Title, Button, Group, Stack, Box, Text, rem, ThemeIcon, ActionIcon, Loader, Center } from '@mantine/core';
import { notifications } from '@mantine/notifications';
import { IconTemplate, IconArrowLeft } from '@tabler/icons-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useParams, useNavigate } from '@tanstack/react-router';
import { templateService } from '../features/templates/services/template';
import { TemplateForm } from '../features/templates/components/TemplateForm';

export const TemplateEditorPage: React.FC = () => {
  const { id } = useParams({ strict: false }) as { id?: string };
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const isEdit = !!id;

  const { data: template, isLoading } = useQuery({
    queryKey: ['template', id],
    queryFn: () => templateService.getTemplate(id!),
    enabled: isEdit,
  });

  const createMutation = useMutation({
    mutationFn: (values: any) => templateService.createTemplate(values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['templates'] });
      notifications.show({ title: 'Success', message: 'Template created successfully', color: 'green' });
      navigate({ to: '/templates' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to create template', color: 'red' });
    },
  });

  const updateMutation = useMutation({
    mutationFn: (values: any) => templateService.updateTemplate(id!, values),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['templates'] });
      notifications.show({ title: 'Success', message: 'Template updated successfully', color: 'green' });
      navigate({ to: '/templates' });
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to update template', color: 'red' });
    },
  });

  const handleSubmit = (values: any) => {
    if (isEdit) {
      updateMutation.mutate(values);
    } else {
      createMutation.mutate(values);
    }
  };

  if (isEdit && isLoading) {
    return (
      <Center h="100vh">
        <Loader size="xl" />
      </Center>
    );
  }

  return (
    <Box>
      <Box p="md" bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))" style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))', position: 'sticky', top: 0, zIndex: 100 }}>
        <Container size="xl">
          <Group justify="space-between">
            <Group gap="lg">
              <ActionIcon variant="subtle" color="gray" onClick={() => navigate({ to: '/templates' })}>
                <IconArrowLeft size={20} />
              </ActionIcon>
              <Group gap="xs">
                <ThemeIcon variant="light" color="brand" size="md">
                  <IconTemplate size={18} />
                </ThemeIcon>
                <Title order={3} style={{ fontWeight: 800 }}>
                  {isEdit ? `Edit Template: ${template?.template?.name}` : 'Create New Template'}
                </Title>
              </Group>
            </Group>
          </Group>
        </Container>
      </Box>

      <Container size="xl" py="xl">
        <TemplateForm
          initialValues={template?.template}
          onSubmit={handleSubmit}
          loading={createMutation.isPending || updateMutation.isPending}
        />
      </Container>
    </Box>
  );
};
