import React, { useRef, useState } from 'react';
import { 
  TextInput, 
  Textarea, 
  Button, 
  Stack, 
  Group, 
  Tabs, 
  Text, 
  rem, 
  Box, 
  Paper, 
  Modal, 
  ThemeIcon,
  SimpleGrid,
  Grid,
  FileButton,
  Divider,
  SegmentedControl
} from '@mantine/core';
import { useForm } from '@mantine/form';
import { useDisclosure } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import { 
  IconAlignLeft, 
  IconLayout2, 
  IconEye, 
  IconEdit, 
  IconUpload, 
  IconFileCode,
  IconCheck,
  IconInfoCircle,
  IconCode,
  IconDeviceDesktop,
  IconDeviceMobile,
  IconTrash,
  IconX,
  IconSend
} from '@tabler/icons-react';
import { TemplateEditor, TemplateEditorHandle } from './TemplateEditor';
import { SendEmailModal } from '../../email-providers/components/SendEmailModal';
import { emailProviderService } from '../../email-providers/services/emailProvider';
import { useMutation } from '@tanstack/react-query';

interface TemplateFormProps {
  initialValues?: any;
  onSubmit: (values: any) => void;
  loading?: boolean;
}

export const TemplateForm: React.FC<TemplateFormProps> = ({ initialValues, onSubmit, loading }) => {
  const [opened, { open, close }] = useDisclosure(false);

  const processTemplateForPreview = (html: string) => {
    if (!html) return '';

    let processed = html;

    // 1. Handle Loops (repeat content 2 times for visualization)
    // Run multiple passes to handle nesting
    for (let i = 0; i < 3; i++) {
      // Handlebars: {{#each var}}...{{/each}}
      processed = processed.replace(/{{\s*#each\s+([a-zA-Z0-9_.-]+)\s*}}([\s\S]*?){{\s*\/each\s*}}/g, '$2$2');
      // Go: {{range .var}}...{{end}}
      processed = processed.replace(/{{\s*range\s+\.?([a-zA-Z0-9_.-]+)\s*}}([\s\S]*?){{\s*end\s*}}/g, '$2$2');
    }

    // 2. Handle Conditionals
    for (let i = 0; i < 3; i++) {
      // Handlebars: {{#if var}}...{{/if}}
      processed = processed.replace(/{{\s*#if\s+([a-zA-Z0-9_.-]+)\s*}}([\s\S]*?){{\s*\/if\s*}}/g, '$2');
      // Go: {{if .var}}...{{end}}
      processed = processed.replace(/{{\s*if\s+\.?([a-zA-Z0-9_.-]+)\s*}}([\s\S]*?){{\s*end\s*}}/g, '$2');
    }

    // 3. Handle else in conditionals
    processed = processed.replace(/{{\s*else\s*}}/g, '');

    // 4. Handle Variables: {{.var}}, {{var}}, {{this}}
    const keywords = ['if', 'else', 'each', 'range', 'end', 'with', 'unless', 'item', 'this'];
    processed = processed.replace(/{{\s*([#/^]?)?\s*\.?([a-zA-Z0-9_.-]+)\s*}}/g, (match, prefix, name) => {
      if (keywords.includes(name) || prefix) return '';
      return `[${name}]`;
    });

    return processed;
  };

  const ensureAccuratePreview = (html: string) => {
    if (!html) return '';
    
    // First, replace variables for a valid DOM structure
    let processedHtml = processTemplateForPreview(html);
    
    // Comprehensive CSS Reset for accurate email rendering in preview
    const cssReset = `
      <style type="text/css">
        /* Basic Resets */
        body, table, td, a { -webkit-text-size-adjust: 100%; -ms-text-size-adjust: 100%; }
        table, td { mso-table-lspace: 0pt; mso-table-rspace: 0pt; }
        img { -ms-interpolation-mode: bicubic; border: 0; height: auto; line-height: 100%; outline: none; text-decoration: none; display: block; max-width: 100%; }
        table { border-collapse: collapse !important; }
        
        /* Precision Layout */
        body { 
          height: 100% !important; 
          margin: 0 !important; 
          padding: 0 !important; 
          width: 100% !important; 
          -webkit-font-smoothing: antialiased; 
          -moz-osx-font-smoothing: grayscale; 
        }
        
        /* Fix for Gmail margin on divs */
        div[style*="margin: 16px 0;"] { margin: 0 !important; }
        
        /* Ensure responsive behavior */
        * { box-sizing: border-box; }
        
        /* Mobile Precision: Prevent auto-scaling of text and ensure fluid layout */
        @media only screen and (max-width: 480px) {
          body, table, td, p, a, li, blockquote {
            -webkit-text-size-adjust: none !important;
          }
          .full-width { width: 100% !important; height: auto !important; }
          .mobile-center { text-align: center !important; }
        }
        
        /* Outlook specific fixes for high DPI */
        @media screen and (min-width: 0\\0) {
          td { mso-line-height-rule: exactly; }
        }

        /* Custom scrollbar for a cleaner look */
        ::-webkit-scrollbar { width: 8px; }
        ::-webkit-scrollbar-track { background: transparent; }
        ::-webkit-scrollbar-thumb { background: rgba(0,0,0,0.1); border-radius: 4px; }
        ::-webkit-scrollbar-thumb:hover { background: rgba(0,0,0,0.2); }
      </style>
    `;

    const metaTags = `
      <meta name="viewport" content="width=device-width, initial-scale=1">
      <meta name="x-apple-disable-message-reformatting">
      <meta name="format-detection" content="telephone=no, date=no, address=no, email=no">
    `;

    // Inject Viewport and CSS Reset
    if (processedHtml.includes('<head>')) {
      // Inject inside head
      if (!processedHtml.includes('name="viewport"') && !processedHtml.includes("name='viewport'")) {
        processedHtml = processedHtml.replace('<head>', `<head>${metaTags}`);
      }
      processedHtml = processedHtml.replace('</head>', `${cssReset}</head>`);
    } else if (processedHtml.includes('<html')) {
      // Create head if missing
      processedHtml = processedHtml.replace(/<html[^>]*>/, `$&<head>${metaTags}${cssReset}</head>`);
    } else {
      // Fragment: wrap or prepend
      processedHtml = `${metaTags}${cssReset}${processedHtml}`;
    }
    
    return processedHtml;
  };

  const [isSendModalOpen, setIsSendModalOpen] = useState(false);
  const editorRef = useRef<TemplateEditorHandle>(null);
  const [previewDevice, setPreviewDevice] = useState<'desktop' | 'mobile'>('desktop');
  const [previewType, setPreviewType] = useState<'html' | 'text'>('html');

  const sendMutation = useMutation({
    mutationFn: (values: any) => emailProviderService.sendEmail(values),
    onSuccess: () => {
      notifications.show({ title: 'Success', message: 'Email sent successfully!', color: 'green' });
      setIsSendModalOpen(false);
    },
    onError: (error: any) => {
      notifications.show({ title: 'Error', message: error.message || 'Failed to send email', color: 'red' });
    },
  });

  const form = useForm({
    initialValues: {
      name: initialValues?.name || '',
      subject: initialValues?.subject || '',
      bodyHtml: initialValues?.bodyHtml || '',
      bodyText: initialValues?.bodyText || '',
      design: initialValues?.design || '',
    },
    validate: {
      name: (value) => (value.length < 2 ? 'Name must have at least 2 characters' : null),
      subject: (value) => (value.length < 2 ? 'Subject must have at least 2 characters' : null),
    },
  });

  const handleSaveBuilder = async () => {
    if (editorRef.current) {
        const { design, html } = await editorRef.current.exportHtml();
        form.setFieldValue('design', design);
        form.setFieldValue('bodyHtml', html);
        close();
        notifications.show({ title: 'Success', message: 'Design saved to template', color: 'blue' });
    }
  };

  const handleFileUpload = (file: File | null) => {
    if (!file) return;

    const reader = new FileReader();
    reader.onload = (e) => {
      const content = e.target?.result as string;
      form.setFieldValue('bodyHtml', content);
      form.setFieldValue('design', ''); // Clear visual design if uploading raw HTML
      notifications.show({ title: 'Success', message: 'HTML file uploaded successfully', color: 'green' });
    };
    reader.readAsText(file);
  };

  const handleSubmit = (values: any) => {
      onSubmit(values);
  };

  const isVisualBuilder = !!form.values.design;
  const hasContent = !!form.values.bodyHtml;

  return (
    <>
      <form onSubmit={form.onSubmit(handleSubmit)}>
        <Stack gap="xl">
          <Group justify="space-between" align="flex-end">
            <Stack gap={0}>
              <Text size="xl" fw={800} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">
                {initialValues ? 'Edit Email Template' : 'Create New Template'}
              </Text>
              <Text size="sm" c="dimmed">Configure your email appearance and content.</Text>
            </Stack>
            <Group>
              {initialValues?.id && (
                <Button 
                  variant="light" 
                  color="green" 
                  radius="md" 
                  size="md" 
                  leftSection={<IconSend size={18} />}
                  onClick={() => setIsSendModalOpen(true)}
                >
                  Test Send
                </Button>
              )}
              <Button type="submit" loading={loading} color="brand" radius="md" size="md" leftSection={<IconCheck size={18} />}>
                  {initialValues ? 'Update Template' : 'Save Template'}
              </Button>
            </Group>
          </Group>

          <Grid>
            <Grid.Col span={{ base: 12, lg: 6 }}>
              <Stack gap="lg">
                <Paper p="xl" withBorder radius="md">
                  <Stack gap="md">
                    <Group gap="xs">
                      <ThemeIcon color="brand" variant="light" size="sm">
                        <IconInfoCircle size={14} />
                      </ThemeIcon>
                      <Text fw={700} size="md">Template Details</Text>
                    </Group>
                    <SimpleGrid cols={2}>
                      <TextInput
                        label="Template Name"
                        placeholder="e.g. Welcome Email"
                        required
                        size="md"
                        radius="md"
                        {...form.getInputProps('name')}
                      />
                      <Stack gap={4}>
                        <TextInput
                          label="Subject Line"
                          placeholder="e.g. Welcome to {{company}}!"
                          required
                          size="md"
                          radius="md"
                          {...form.getInputProps('subject')}
                        />
                        <Group gap={4}>
                          {['{{name}}', '{{company}}', '{{email}}'].map(v => (
                            <Button
                              key={v}
                              variant="subtle"
                              size="compact-xs"
                              onClick={() => form.setFieldValue('subject', form.values.subject + v)}
                            >
                              +{v}
                            </Button>
                          ))}
                        </Group>
                      </Stack>
                    </SimpleGrid>
                  </Stack>
                </Paper>

                <Paper p="xl" withBorder radius="md">
                  <Stack gap="md">
                    <Group justify="space-between">
                      <Group gap="xs">
                        <ThemeIcon color="pink" variant="light" size="sm">
                          <IconLayout2 size={14} />
                        </ThemeIcon>
                        <Text fw={700} size="md">Content Source</Text>
                      </Group>
                      {hasContent && (
                        <Button
                          variant="subtle"
                          color="red"
                          size="xs"
                          leftSection={<IconTrash size={14} />}
                          onClick={() => {
                            form.setFieldValue('bodyHtml', '');
                            form.setFieldValue('design', '');
                          }}
                        >
                          Reset Content
                        </Button>
                      )}
                    </Group>

                    {!hasContent ? (
                      <SimpleGrid cols={2} spacing="md">
                        <Paper
                          withBorder
                          p="xl"
                          radius="md"
                          style={{ cursor: 'pointer', transition: 'transform 0.2s' }}
                          component="div"
                          onClick={open}
                          onMouseEnter={(e) => e.currentTarget.style.transform = 'translateY(-4px)'}
                          onMouseLeave={(e) => e.currentTarget.style.transform = 'translateY(0)'}
                        >
                          <Stack align="center" gap="xs">
                            <ThemeIcon size={48} radius="xl" color="brand" variant="light">
                              <IconLayout2 size={24} />
                            </ThemeIcon>
                            <Text fw={700}>Visual Builder</Text>
                            <Text size="xs" c="dimmed" ta="center">Drag and drop blocks to build your email.</Text>
                          </Stack>
                        </Paper>

                        <FileButton onChange={handleFileUpload} accept="text/html">
                          {(props) => (
                            <Paper
                              withBorder
                              p="xl"
                              radius="md"
                              style={{ cursor: 'pointer', transition: 'transform 0.2s' }}
                              onMouseEnter={(e) => e.currentTarget.style.transform = 'translateY(-4px)'}
                              onMouseLeave={(e) => e.currentTarget.style.transform = 'translateY(0)'}
                              {...props}
                            >
                              <Stack align="center" gap="xs">
                                <ThemeIcon size={48} radius="xl" color="indigo" variant="light">
                                  <IconUpload size={24} />
                                </ThemeIcon>
                                <Text fw={700}>Upload HTML</Text>
                                <Text size="xs" c="dimmed" ta="center">Upload an existing HTML template file.</Text>
                              </Stack>
                            </Paper>
                          )}
                        </FileButton>
                      </SimpleGrid>
                    ) : (
                      <Stack gap="md">
                        {isVisualBuilder ? (
                          <Paper p="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))" radius="md">
                            <Group justify="space-between">
                              <Group>
                                <ThemeIcon color="green" variant="light" size="lg" radius="xl">
                                  <IconCheck size={20} />
                                </ThemeIcon>
                                <Stack gap={0}>
                                  <Text fw={700} size="sm">Visual Builder Active</Text>
                                  <Text size="xs" c="dimmed">This template is managed via the visual editor.</Text>
                                </Stack>
                              </Group>
                              <Button
                                leftSection={<IconEdit size={16} />}
                                color="brand"
                                onClick={open}
                                size="sm"
                              >
                                Edit with Visual Builder
                              </Button>
                            </Group>
                          </Paper>
                        ) : (
                          <Stack gap="xs">
                            <Group justify="space-between">
                              <Group gap="xs">
                                <ThemeIcon color="indigo" variant="light" size="sm">
                                  <IconCode size={14} />
                                </ThemeIcon>
                                <Text fw={700} size="sm">HTML Editor</Text>
                              </Group>
                              <FileButton onChange={handleFileUpload} accept="text/html">
                                {(props) => (
                                  <Button {...props} variant="subtle" size="xs" leftSection={<IconUpload size={14} />}>
                                    Upload New Version
                                  </Button>
                                )}
                              </FileButton>
                            </Group>
                            <Textarea
                              placeholder="<html>...</html>"
                              minRows={15}
                              autosize
                              {...form.getInputProps('bodyHtml')}
                              styles={{ input: { fontFamily: 'monospace', fontSize: rem(12), backgroundColor: 'light-dark(#f8f9fa, #1A1B1E)' } }}
                            />
                            <Text size="xs" c="dimmed">Directly editing HTML will prevent using the Visual Builder for this template.</Text>
                          </Stack>
                        )}
                      </Stack>
                    )}
                  </Stack>
                </Paper>

                <Paper p="xl" withBorder radius="md">
                  <Stack gap="md">
                    <Group gap="xs">
                      <ThemeIcon color="indigo" variant="light" size="sm">
                        <IconAlignLeft size={14} />
                      </ThemeIcon>
                      <Text fw={700} size="md">Plain Text Version</Text>
                    </Group>
                    <Textarea
                        placeholder="Hello {{name}}, Welcome to our service!"
                        description="Sent to recipients who cannot view HTML emails."
                        minRows={3}
                        autosize
                        size="md"
                        radius="md"
                        {...form.getInputProps('bodyText')}
                    />
                  </Stack>
                </Paper>
              </Stack>
            </Grid.Col>

            <Grid.Col span={{ base: 12, lg: 6 }}>
              <Paper p="md" withBorder radius="md" style={{ position: 'sticky', top: 20 }}>
                <Stack gap="md">
                  <Group justify="space-between">
                    <Text fw={700} size="sm">Live Preview</Text>
                    <Group gap="xs">
                      <SegmentedControl
                        size="xs"
                        value={previewType}
                        onChange={(val: any) => setPreviewType(val)}
                        data={[
                          { label: 'HTML', value: 'html' },
                          { label: 'Text', value: 'text' },
                        ]}
                      />
                      <SegmentedControl
                        size="xs"
                        value={previewDevice}
                        onChange={(val: any) => setPreviewDevice(val)}
                        data={[
                          { label: <IconDeviceDesktop size={16} />, value: 'desktop' },
                          { label: <IconDeviceMobile size={16} />, value: 'mobile' },
                        ]}
                        disabled={previewType === 'text'}
                      />
                    </Group>
                  </Group>

                  <Box
                    style={{
                      height: 750,
                      backgroundColor: 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-8))',
                      borderRadius: rem(8),
                      border: '1px solid var(--mantine-color-gray-3)',
                      overflowY: 'auto',
                      overflowX: 'hidden',
                      display: 'flex',
                      justifyContent: 'center',
                      padding: rem(20),
                      backgroundImage: 'radial-gradient(var(--mantine-color-gray-3) 0.5px, transparent 0.5px)',
                      backgroundSize: '20px 20px',
                    }}
                  >
                    <Box
                      style={{
                        width: previewType === 'text' ? '100%' : (previewDevice === 'mobile' ? 375 : '100%'),
                        maxWidth: '100%',
                        height: previewDevice === 'mobile' && previewType === 'html' ? 667 : '100%',
                        minHeight: '100%',
                        backgroundColor: 'white',
                        transition: 'all 0.3s ease',
                        boxShadow: '0 20px 50px rgba(0,0,0,0.15)',
                        borderRadius: previewDevice === 'mobile' && previewType === 'html' ? rem(40) : rem(12),
                        border: previewDevice === 'mobile' && previewType === 'html' ? '12px solid #1a1a1a' : '1px solid var(--mantine-color-gray-3)',
                        position: 'relative',
                        overflow: 'hidden',
                        display: 'flex',
                        flexDirection: 'column'
                      }}
                    >
                      {previewType === 'html' && (
                        <Box style={{
                          height: previewDevice === 'mobile' ? 40 : 34,
                          backgroundColor: previewDevice === 'mobile' ? '#1a1a1a' : 'var(--mantine-color-gray-1)',
                          borderBottom: previewDevice === 'mobile' ? 'none' : '1px solid var(--mantine-color-gray-3)',
                          display: 'flex',
                          alignItems: 'center',
                          padding: '0 15px',
                          gap: 5
                        }}>
                          {previewDevice === 'desktop' ? (
                            <>
                              <Group gap={6}>
                                <Box w={8} h={8} style={{ borderRadius: '50%', backgroundColor: '#ff5f56' }} />
                                <Box w={8} h={8} style={{ borderRadius: '50%', backgroundColor: '#ffbd2e' }} />
                                <Box w={8} h={8} style={{ borderRadius: '50%', backgroundColor: '#27c93f' }} />
                              </Group>
                              <Box 
                                ml="md" 
                                style={{ 
                                  flex: 1, 
                                  height: 20, 
                                  backgroundColor: 'light-dark(white, var(--mantine-color-dark-6))', 
                                  borderRadius: 4,
                                  fontSize: 10,
                                  display: 'flex',
                                  alignItems: 'center',
                                  padding: '0 8px',
                                  color: 'var(--mantine-color-gray-5)',
                                  border: '1px solid var(--mantine-color-gray-2)',
                                  overflow: 'hidden',
                                  whiteSpace: 'nowrap'
                                }}
                              >
                                {form.values.subject || 'New Message'}
                              </Box>
                            </>
                          ) : (
                            <>
                              <Box style={{ 
                                height: 18, 
                                width: 60, 
                                backgroundColor: '#333', 
                                borderRadius: 10,
                                margin: '0 auto'
                              }} />
                              <Box style={{ 
                                height: 25, 
                                width: '40%', 
                                backgroundColor: '#1a1a1a', 
                                position: 'absolute', 
                                top: 0, 
                                left: '50%', 
                                transform: 'translateX(-50%)', 
                                borderBottomLeftRadius: 15, 
                                borderBottomRightRadius: 15, 
                              }} />
                            </>
                          )}
                        </Box>
                      )}

                      <Box style={{
                        flex: 1,
                        overflowY: 'auto',
                        padding: previewType === 'text' ? rem(20) : 0,
                        backgroundColor: 'white'
                      }}>
                        {previewType === 'text' ? (
                          <pre style={{
                            whiteSpace: 'pre-wrap',
                            wordBreak: 'break-word',
                            fontFamily: 'monospace',
                            margin: 0,
                            fontSize: rem(14),
                            color: '#333'
                          }}>
                            {form.values.bodyText || 'No plain text version provided.'}
                          </pre>
                        ) : hasContent ? (
                          <iframe
                            title="Preview"
                            srcDoc={ensureAccuratePreview(form.values.bodyHtml)}
                            style={{
                              width: '100%',
                              height: '100%',
                              border: 'none',
                              display: 'block'
                            }}
                          />
                        ) : (
                          <Stack align="center" justify="center" h="100%" c="dimmed" p="xl">
                            <IconEye size={48} stroke={1.5} />
                            <Text ta="center" size="sm">Your email preview will appear here once you add content.</Text>
                          </Stack>
                        )}
                      </Box>

                      {previewDevice === 'mobile' && previewType === 'html' && (
                        <Box style={{
                          height: 20,
                          backgroundColor: '#1a1a1a',
                          display: 'flex',
                          alignItems: 'center',
                          justifyContent: 'center'
                        }}>
                          <Box w={40} h={4} style={{ borderRadius: 2, backgroundColor: '#333' }} />
                        </Box>
                      )}
                    </Box>
                  </Box>
                </Stack>
              </Paper>
            </Grid.Col>
          </Grid>
        </Stack>
      </form>

      <Modal
        opened={opened}
        onClose={close}
        fullScreen
        title={
          <Group justify="space-between" w="100%" pr="xl">
            <Group gap="xs">
              <ThemeIcon variant="transparent" color="brand">
                <IconLayout2 size={24} />
              </ThemeIcon>
              <Text fw={800} size="lg">Visual Template Builder</Text>
            </Group>
            <Group>
              <Button variant="subtle" color="gray" onClick={close}>Cancel</Button>
              <Button color="brand" onClick={handleSaveBuilder} leftSection={<IconCheck size={18} />}>Save Design</Button>
            </Group>
          </Group>
        }
        styles={{
          header: {
            backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-8))',
            borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))',
            padding: '10px 20px'
          },
          body: { padding: 0, height: 'calc(100vh - 60px)' },
        }}
      >
        <TemplateEditor
          ref={editorRef}
          initialDesign={form.values.design}
          minHeight="100%"
        />
      </Modal>

      <SendEmailModal
        opened={isSendModalOpen}
        onClose={() => setIsSendModalOpen(false)}
        initialTemplateId={initialValues?.id || undefined}
        onSubmit={(values) => sendMutation.mutate(values)}
        loading={sendMutation.isPending}
      />
    </>
  );
};
