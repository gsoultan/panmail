import React, { useState } from 'react';
import { 
  TextInput, 
  Textarea, 
  TagsInput,
  Button, 
  Stack, 
  Group, 
  Select, 
  Text, 
  JsonInput, 
  Tabs, 
  Paper, 
  rem, 
  ThemeIcon, 
  Code, 
  ActionIcon, 
  CopyButton, 
  Tooltip,
  SimpleGrid,
  Divider,
  Badge,
  FileButton,
  Box,
  CloseButton,
  ScrollArea,
  Alert
} from '@mantine/core';
import { useForm } from '@mantine/form';
import { useQuery } from '@tanstack/react-query';
import { templateService } from '../../templates/services/template';
import { emailProviderService } from '../services/emailProvider';
import {
  IconSend,
  IconCode,
  IconMail,
  IconCopy,
  IconCheck,
  IconInfoCircle,
  IconPaperclip,
  IconFile,
  IconTrash,
  IconUser,
  IconDeviceDesktop,
  IconTextSize,
  IconFileZip,
  IconPlus
} from '@tabler/icons-react';
import { Attachment } from '../../../api/panmail/v1/common_pb';

interface SendEmailFormProps {
  onSubmit: (values: any) => void;
  loading?: boolean;
  initialTemplateId?: string;
  initialProviderId?: string;
  title?: React.ReactNode;
}

export const SendEmailForm: React.FC<SendEmailFormProps> = ({
  onSubmit,
  loading,
  initialTemplateId,
  initialProviderId,
  title
}) => {
  const [activeTab, setActiveTab] = useState<string | null>('form');

  const { data: templatesData } = useQuery({
    queryKey: ['templates'],
    queryFn: () => templateService.listTemplates(100),
  });

  const { data: providersData } = useQuery({
    queryKey: ['emailProviders'],
    queryFn: () => emailProviderService.listProviders(100),
  });

  const templates = templatesData?.templates || [];
  const providers = providersData?.providers || [];

  const form = useForm({
    initialValues: {
      providerId: initialProviderId || '',
      from: '',
      to: [] as string[],
      cc: [] as string[],
      bcc: [] as string[],
      subject: 'Test Email from Panmail',
      bodyHtml: '<h1>Test Email</h1><p>This is a test email sent from Panmail Email Gateway.</p>',
      bodyText: 'This is a test email sent from Panmail Email Gateway.',
      templateId: initialTemplateId || '',
      templateData: '{}',
      attachments: [] as { filename: string; contentType: string; content: Uint8Array }[],
    },
    validate: {
      providerId: (value) => (value ? null : 'Email provider is required'),
      from: (value) => (value ? null : 'From address is required'),
      to: (value) => (value && value.length > 0 ? null : 'At least one recipient is required'),
      templateData: (value) => {
        if (!value) return null;
        try {
          JSON.parse(value);
          return null;
        } catch (e) {
          return 'Invalid JSON';
        }
      },
    },
  });

  const handleAddAttachment = (files: File[]) => {
    files.forEach(file => {
      const reader = new FileReader();
      reader.onload = (e) => {
        const result = e.target?.result;
        if (result instanceof ArrayBuffer) {
          const content = new Uint8Array(result);
          form.insertListItem('attachments', {
            filename: file.name,
            contentType: file.type || 'application/octet-stream',
            content: content
          });
        }
      };
      reader.readAsArrayBuffer(file);
    });
  };

  // Extract variables from template content
  const extractVariables = (template: any) => {
    if (!template) return {};
    const content = `${template.subject} ${template.bodyHtml} ${template.bodyText} ${template.design || ''}`;
    const data: any = {};

    // 1. Find all loops: {{#each var}} or {{range .var}}
    const loopRegex = /{{\s*(?:#each|range)\s+\s*\.?([a-zA-Z0-9_.-]+)\s*}}/g;
    let match;
    const loops = new Set<string>();
    while ((match = loopRegex.exec(content)) !== null) {
      const path = match[1];
      const parts = path.split('.').filter(p => p !== '');
      if (parts.length > 0) {
        loops.add(parts[0]);
      }
    }

    // 2. Find all variables: {{var}}, {{.var}}, {{#if var}}, etc.
    const varRegex = /{{\s*([#/^]?)?\s*\.?([a-zA-Z0-9_.-]+)\s*}}/g;
    const keywords = ['if', 'else', 'each', 'range', 'end', 'with', 'unless', 'item', 'this'];

    while ((match = varRegex.exec(content)) !== null) {
      const name = match[2];
      const parts = name.split('.').filter(p => p !== '');
      if (parts.length === 0) continue;

      const root = parts[0];

      if (root && !keywords.includes(root)) {
        if (loops.has(root)) {
          if (!data[root] || !Array.isArray(data[root])) {
            data[root] = [
              { id: 1, name: `Item 1`, description: `Sample description for ${root}` },
              { id: 2, name: `Item 2`, description: `Another description for ${root}` }
            ];
          }
        } else {
          // Handle nested paths like user.name
          let current = data;
          for (let i = 0; i < parts.length; i++) {
            const p = parts[i];
            if (i === parts.length - 1) {
              if (current[p] === undefined) {
                current[p] = `[${parts.join('.')}]`;
              }
            } else {
              if (current[p] === undefined || typeof current[p] !== 'object') {
                current[p] = {};
              }
              current = current[p];
            }
          }
        }
      }
    }

    return data;
  };

  // Reset form when initial values change
  React.useEffect(() => {
    const tId = initialTemplateId || '';
    form.setFieldValue('templateId', tId);
    form.setFieldValue('providerId', initialProviderId || '');
    
    if (tId && templates.length > 0) {
      const template = templates.find(t => t.id === tId);
      if (template) {
        const mockup = extractVariables(template);
        form.setFieldValue('templateData', JSON.stringify(mockup, null, 2));
      }
    } else {
      form.setFieldValue('templateData', '{}');
    }
  }, [initialTemplateId, initialProviderId, templates]);

  const handleSubmit = (values: typeof form.values) => {
    const data: any = {
      ...values,
      to: values.to,
    };

    if (values.templateId) {
      data.templateData = JSON.parse(values.templateData);
    } else {
      // Remove template fields if not used
      const { templateId, templateData, ...rest } = data;
      onSubmit(rest);
      return;
    }

    onSubmit(data);
  };

  const templateOptions = templates.map(t => ({ value: t.id, label: t.name }));
  const providerOptions = providers.map(p => ({ 
    value: p.id, 
    label: `${p.name} (${p.id.substring(0, 8)}...)` 
  }));

  const selectedProvider = providers.find(p => p.id === form.values.providerId);

  const generateJson = () => {
    const values = form.values;
    const data: any = {
      providerId: values.providerId || 'YOUR_PROVIDER_ID',
      from: values.from || 'sender@example.com',
      to: values.to.length > 0 ? values.to : ['recipient@example.com'],
    };

    if (values.cc && values.cc.length > 0) {
      data.cc = values.cc;
    }
    if (values.bcc && values.bcc.length > 0) {
      data.bcc = values.bcc;
    }

    if (values.templateId) {
      data.templateId = values.templateId;
      try {
        data.templateData = JSON.parse(values.templateData);
      } catch (e) {
        data.templateData = {};
      }
    } else {
      data.subject = values.subject;
      data.bodyHtml = values.bodyHtml;
      data.bodyText = values.bodyText;
    }

    if (values.attachments.length > 0) {
      data.attachments = values.attachments.map(a => {
        const att = new Attachment(a as any);
        return att.toJson();
      });
    }

    return JSON.stringify(data, null, 2);
  };

  const curlCommand = `curl -X POST "${window.location.origin}/panmail.v1.EmailService/SendEmail" \\
  -H "Content-Type: application/json" \\
  -H "X-API-Key: YOUR_API_KEY" \\
  -d '${generateJson().replace(/'/g, "'\\''")}'`;

  return (
    <Stack gap="md">
      {title && (
        <Paper withBorder p="md" radius="md">
          {title}
        </Paper>
      )}
      <Tabs value={activeTab} onChange={setActiveTab} radius="md">
        <Tabs.List mb="md">
          <Tabs.Tab value="form" leftSection={<IconMail size={16} />}>Test Form</Tabs.Tab>
          <Tabs.Tab value="api" leftSection={<IconCode size={16} />}>API Request</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="form">
          <form onSubmit={form.onSubmit(handleSubmit)}>
            <Stack gap="lg">
              <Paper withBorder p="md" radius="md">
                <Stack gap="md">
                  <Group gap="xs" mb={4}>
                    <ThemeIcon variant="light" size="sm" color="brand">
                      <IconUser size={14} />
                    </ThemeIcon>
                    <Text fw={700} size="sm">1. Sender & Recipients</Text>
                  </Group>
                  <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                    <Select
                      label="Email Provider"
                      placeholder="Select SMTP/API Provider"
                      data={providerOptions}
                      required
                      searchable
                      {...form.getInputProps('providerId')}
                      leftSection={<IconMail size={16} />}
                    />
                    <TextInput
                      label="From Email"
                      placeholder="sender@example.com"
                      {...form.getInputProps('from')}
                      required
                    />
                  </SimpleGrid>

                  {selectedProvider && (
                    <TextInput
                      label="Selected Provider ID"
                      value={selectedProvider.id}
                      readOnly
                      variant="filled"
                      size="sm"
                      radius="md"
                      rightSection={
                        <CopyButton value={selectedProvider.id}>
                          {({ copied, copy }) => (
                            <Tooltip label={copied ? 'Copied' : 'Copy ID'} withArrow position="right">
                              <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" onClick={copy}>
                                {copied ? <IconCheck size={14} stroke={2} /> : <IconCopy size={14} stroke={2} />}
                              </ActionIcon>
                            </Tooltip>
                          )}
                        </CopyButton>
                      }
                    />
                  )}

                  <TagsInput
                    label="To (Recipients)"
                    placeholder="Type email and press Enter"
                    {...form.getInputProps('to')}
                    required
                    clearable
                  />

                  <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="md">
                    <TagsInput
                      label="Cc"
                      placeholder="Type email and press Enter"
                      {...form.getInputProps('cc')}
                      clearable
                    />
                    <TagsInput
                      label="Bcc"
                      placeholder="Type email and press Enter"
                      {...form.getInputProps('bcc')}
                      clearable
                    />
                  </SimpleGrid>
                </Stack>
              </Paper>

              <Paper withBorder p="md" radius="md">
                <Stack gap="md">
                  <Group gap="xs" mb={4}>
                    <ThemeIcon variant="light" size="sm" color="brand">
                      <IconTextSize size={14} />
                    </ThemeIcon>
                    <Text fw={700} size="sm">2. Email Content</Text>
                  </Group>

                  <Select
                    label="Choose Template"
                    placeholder="Select a template or leave empty for custom content"
                    data={templateOptions}
                    clearable
                    {...form.getInputProps('templateId')}
                    description="Choosing a template will hide manual content fields"
                    onChange={(val) => {
                      form.setFieldValue('templateId', val || '');
                      if (val) {
                        const template = templates.find(t => t.id === val);
                        if (template) {
                          const mockup = extractVariables(template);
                          form.setFieldValue('templateData', JSON.stringify(mockup, null, 2));
                        }
                      } else {
                        form.setFieldValue('templateData', '{}');
                      }
                    }}
                  />

                  {!form.values.templateId ? (
                    <Stack gap="md">
                      <TextInput 
                        label="Subject" 
                        placeholder="Email Subject" 
                        {...form.getInputProps('subject')} 
                        required 
                      />
                      <Box>
                        <Text size="sm" fw={500} mb={4}>Message Body</Text>
                        <Tabs defaultValue="html" variant="outline" radius="md">
                          <Tabs.List>
                            <Tabs.Tab value="html" leftSection={<IconCode size={14} />}>HTML Body</Tabs.Tab>
                            <Tabs.Tab value="text" leftSection={<IconDeviceDesktop size={14} />}>Plain Text</Tabs.Tab>
                          </Tabs.List>
                          <Tabs.Panel value="html" pt="xs">
                            <Textarea 
                              placeholder="<h1>Hello</h1>" 
                              minRows={6} 
                              autosize 
                              {...form.getInputProps('bodyHtml')} 
                              styles={{ input: { fontFamily: 'monospace' } }}
                            />
                          </Tabs.Panel>
                          <Tabs.Panel value="text" pt="xs">
                            <Textarea 
                              placeholder="Hello there" 
                              minRows={6} 
                              autosize 
                              {...form.getInputProps('bodyText')} 
                            />
                          </Tabs.Panel>
                        </Tabs>
                      </Box>
                    </Stack>
                  ) : (
                    <Stack gap="xs">
                      <Group gap={4} justify="space-between">
                        <Group gap="xs">
                          <Text size="sm" fw={600}>Template Data (JSON)</Text>
                          <Badge variant="light" size="xs" color="blue">Supports Handlebars & Go-style</Badge>
                        </Group>
                        <Button 
                          variant="subtle" 
                          size="compact-xs" 
                          leftSection={<IconInfoCircle size={12} />}
                          onClick={() => {
                            const template = templates.find(t => t.id === form.values.templateId);
                            if (template) {
                              const mockup = extractVariables(template);
                              form.setFieldValue('templateData', JSON.stringify(mockup, null, 2));
                            }
                          }}
                        >
                          Auto-generate Mockup
                        </Button>
                      </Group>
                      <JsonInput
                        placeholder='{ "name": "John", "token": "12345" }'
                        validationError="Invalid JSON"
                        formatOnBlur
                        autosize
                        minRows={6}
                        {...form.getInputProps('templateData')}
                        styles={{ input: { fontFamily: 'monospace', fontSize: rem(12) } }}
                      />
                    </Stack>
                  )}
                </Stack>
              </Paper>

              <Paper withBorder p="md" radius="md">
                <Stack gap="md">
                  <Group justify="space-between">
                    <Group gap="xs">
                      <ThemeIcon variant="light" size="sm" color="brand">
                        <IconPaperclip size={14} />
                      </ThemeIcon>
                      <Text fw={700} size="sm">3. Attachments (Optional)</Text>
                    </Group>
                    <FileButton onChange={handleAddAttachment} accept="*" multiple>
                      {(props) => (
                        <Button {...props} variant="subtle" size="xs" leftSection={<IconPlus size={14} />}>
                          Add Files
                        </Button>
                      )}
                    </FileButton>
                  </Group>

                  {form.values.attachments.length > 0 ? (
                    <Box style={{ maxHeight: rem(200), overflowY: 'auto' }}>
                      <Stack gap="xs">
                        {form.values.attachments.map((file, index) => (
                          <Paper key={index} withBorder p="xs" radius="md">
                            <Group justify="space-between">
                              <Group gap="xs">
                                <ThemeIcon variant="light" color="gray" size="md">
                                  {file.contentType.includes('image') ? <IconFile size={16} /> : 
                                   file.contentType.includes('zip') ? <IconFileZip size={16} /> :
                                   <IconFile size={16} />}
                                </ThemeIcon>
                                <Box>
                                  <Text size="xs" fw={700} truncate maw={200}>{file.filename}</Text>
                                  <Text size="px" c="dimmed">{file.contentType}</Text>
                                </Box>
                              </Group>
                              <ActionIcon variant="subtle" color="red" onClick={() => form.removeListItem('attachments', index)}>
                                <IconTrash size={14} />
                              </ActionIcon>
                            </Group>
                          </Paper>
                        ))}
                      </Stack>
                    </Box>
                  ) : (
                    <Text size="xs" c="dimmed" ta="center" py="sm" style={{ border: '1px dashed var(--mantine-color-gray-3)', borderRadius: rem(8) }}>
                      No attachments added yet.
                    </Text>
                  )}
                </Stack>
              </Paper>

              <Button 
                type="submit" 
                loading={loading} 
                fullWidth 
                size="md" 
                mt="md" 
                radius="md" 
                leftSection={<IconSend size={18} />}
                color="brand"
              >
                Send Test Email Now
              </Button>
            </Stack>
          </form>
        </Tabs.Panel>

        <Tabs.Panel value="api">
          <Stack gap="md">
            <Paper p="md" withBorder radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
              <Stack gap="xs">
                <Group justify="space-between">
                  <Group gap="xs">
                    <ThemeIcon variant="light" color="brand" size="md">
                      <IconCode size={16} />
                    </ThemeIcon>
                    <Text fw={700} size="sm">cURL Command</Text>
                  </Group>
                  <CopyButton value={curlCommand}>
                    {({ copied, copy }) => (
                      <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" onClick={copy}>
                        {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                      </ActionIcon>
                    )}
                  </CopyButton>
                </Group>
                <ScrollArea h={rem(120)}>
                  <Code block style={{ whiteSpace: 'pre-wrap', fontSize: rem(11) }}>
                    {curlCommand}
                  </Code>
                </ScrollArea>
              </Stack>
            </Paper>

            <Paper p="md" withBorder radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
              <Stack gap="xs">
                <Group justify="space-between">
                  <Group gap="xs">
                    <ThemeIcon variant="light" color="brand" size="md">
                      <IconCode size={16} />
                    </ThemeIcon>
                    <Text fw={700} size="sm">JSON Data Structure</Text>
                  </Group>
                  <CopyButton value={generateJson()}>
                    {({ copied, copy }) => (
                      <ActionIcon color={copied ? 'teal' : 'gray'} variant="subtle" onClick={copy}>
                        {copied ? <IconCheck size={16} /> : <IconCopy size={16} />}
                      </ActionIcon>
                    )}
                  </CopyButton>
                </Group>
                <ScrollArea h={rem(200)}>
                  <Code block style={{ fontSize: rem(11) }}>
                    {generateJson()}
                  </Code>
                </ScrollArea>
              </Stack>
            </Paper>

            <Alert icon={<IconInfoCircle size={16} />} color="blue" radius="md">
              <Text size="xs">
                Make sure to replace <code>YOUR_API_KEY</code> with a valid API key generated in the <b>API Keys</b> section.
                For production use, send requests to <code>/panmail.v1.EmailService/SendEmail</code> using POST.
                Attachments are sent as an array of objects with <code>content</code> as a base64 string.
              </Text>
            </Alert>
          </Stack>
        </Tabs.Panel>
      </Tabs>
    </Stack>
  );
};
