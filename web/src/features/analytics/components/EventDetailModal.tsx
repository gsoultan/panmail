import React, { useState } from 'react';
import { Modal, Stack, Group, Text, Badge, Divider, Box, Paper, ScrollArea, Tabs, ThemeIcon, Table, Button, Grid, rem, Alert, SegmentedControl, Timeline } from '@mantine/core';
import { IconMail, IconUser, IconCalendar, IconPaperclip, IconCode, IconBrowser, IconInfoCircle, IconDownload, IconAlertCircle, IconHistory } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { analyticsService } from '../services/analytics';
import { EmailEventType } from '../../../api/panmail/v1/event_pb';

interface EventDetailModalProps {
  eventId: string | null;
  opened: boolean;
  onClose: () => void;
  eventTypeConfig: Record<EmailEventType, { label: string, color: string, icon: any }>;
}

export const EventDetailModal: React.FC<EventDetailModalProps> = ({ eventId, opened, onClose, eventTypeConfig }) => {
  const [contentType, setContentType] = useState<'html' | 'text'>('html');
  const { data, isLoading } = useQuery({
    queryKey: ['eventDetail', eventId],
    queryFn: () => eventId ? analyticsService.getEvent(eventId) : null,
    enabled: !!eventId,
  });

  const event = data?.event;
  const message = data?.message;

  const { data: timelineData } = useQuery({
    queryKey: ['eventTimeline', event?.messageId, event?.recipient],
    queryFn: () => event ? analyticsService.listEvents(100, '', event.recipient, 0, undefined, undefined, event.messageId) : null,
    enabled: !!event,
  });

  if (!eventId) return null;

  const config = event?.type !== undefined ? eventTypeConfig[event.type] : eventTypeConfig[EmailEventType.UNSPECIFIED];
  const StatusIcon = config.icon;

  const downloadAttachment = (filename: string, contentType: string, content: Uint8Array) => {
    const blob = new Blob([content as any], { type: contentType });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    a.click();
    URL.revokeObjectURL(url);
  };

  const timelineEvents = [...(timelineData?.events || [])].sort((a, b) =>
    Number(a.timestamp?.seconds || 0) - Number(b.timestamp?.seconds || 0)
  );

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      title={
        <Group gap="xs">
          <ThemeIcon variant="light" color={config.color} size="md">
            <StatusIcon size={18} />
          </ThemeIcon>
          <Text fw={700}>Email Delivery Details</Text>
          <Badge variant="light" color={config.color}>{config.label}</Badge>
        </Group>
      }
      size="xl"
      radius="md"
    >
      <Stack gap="md">
        {isLoading ? (
          <Text ta="center" py="xl">Loading details...</Text>
        ) : !event ? (
          <Text ta="center" py="xl">Event not found.</Text>
        ) : (
          <>
            {event.errorMessage && (
              <Alert icon={<IconAlertCircle size={16} />} title="Delivery Error" color="red" variant="light" radius="md">
                <Text size="sm">{event.errorMessage}</Text>
              </Alert>
            )}

            <Paper withBorder p="md" radius="md">
              <Grid>
                <Grid.Col span={6}>
                  <Stack gap={4}>
                    <Text size="xs" c="dimmed" fw={700} tt="uppercase">Recipient</Text>
                    <Group gap="xs">
                      <IconUser size={14} />
                      <Text fw={600} truncate>{event.recipient}</Text>
                    </Group>
                  </Stack>
                </Grid.Col>
                <Grid.Col span={6}>
                  <Stack gap={4}>
                    <Text size="xs" c="dimmed" fw={700} tt="uppercase">Timestamp</Text>
                    <Group gap="xs">
                      <IconCalendar size={14} />
                      <Text fw={600}>
                        {event.timestamp ? new Date(Number(event.timestamp.seconds) * 1000).toLocaleString() : '-'}
                      </Text>
                    </Group>
                  </Stack>
                </Grid.Col>
                <Grid.Col span={12}>
                  <Divider />
                </Grid.Col>
                <Grid.Col span={12}>
                  <Stack gap={4}>
                    <Text size="xs" c="dimmed" fw={700} tt="uppercase">Subject</Text>
                    <Text fw={600} size="md" c="brand">{event.subject || 'No Subject'}</Text>
                  </Stack>
                </Grid.Col>
                <Grid.Col span={6}>
                  <Stack gap={4}>
                    <Text size="xs" c="dimmed" fw={700} tt="uppercase">Provider Name</Text>
                    <Text fw={600}>{event.providerName || event.providerId || 'N/A'}</Text>
                  </Stack>
                </Grid.Col>
                <Grid.Col span={6}>
                  <Stack gap={4}>
                    <Text size="xs" c="dimmed" fw={700} tt="uppercase">Message ID</Text>
                    <Text size="xs" style={{ fontFamily: 'monospace' }} c="dimmed">{event.messageId}</Text>
                  </Stack>
                </Grid.Col>
              </Grid>
            </Paper>

            <Tabs defaultValue="timeline">
              <Tabs.List>
                <Tabs.Tab value="timeline" leftSection={<IconHistory size={14} />}>Timeline</Tabs.Tab>
                <Tabs.Tab value="content" leftSection={<IconBrowser size={14} />}>Content</Tabs.Tab>
                <Tabs.Tab value="attachments" leftSection={<IconPaperclip size={14} />}>
                  Attachments {message?.attachments && message.attachments.length > 0 && `(${message.attachments.length})`}
                </Tabs.Tab>
                <Tabs.Tab value="metadata" leftSection={<IconInfoCircle size={14} />}>Metadata</Tabs.Tab>
              </Tabs.List>

              <Tabs.Panel value="timeline" pt="md">
                <Box py="md" px="lg">
                  {timelineEvents.length > 0 ? (
                    <Timeline active={timelineEvents.length - 1} bulletSize={24} lineWidth={2}>
                      {timelineEvents.map((te, index) => {
                        const teConfig = eventTypeConfig[te.type] || eventTypeConfig[EmailEventType.UNSPECIFIED];
                        const TeIcon = teConfig.icon;
                        return (
                          <Timeline.Item
                            key={te.id}
                            bullet={<TeIcon size={12} />}
                            title={teConfig.label}
                            color={teConfig.color}
                          >
                            <Text size="xs" c="dimmed" mt={4}>
                              {te.timestamp ? new Date(Number(te.timestamp.seconds) * 1000).toLocaleString() : '-'}
                            </Text>
                            {te.errorMessage && (
                              <Text size="xs" color="red" mt={4} style={{ fontStyle: 'italic' }}>
                                {te.errorMessage}
                              </Text>
                            )}
                          </Timeline.Item>
                        );
                      })}
                    </Timeline>
                  ) : (
                    <Text ta="center" py="xl" c="dimmed">No timeline data available.</Text>
                  )}
                </Box>
              </Tabs.Panel>

              <Tabs.Panel value="content" pt="md">
                {message ? (
                  <Stack gap="xs">
                    <Group justify="flex-end">
                      <SegmentedControl
                        size="xs"
                        value={contentType}
                        onChange={(val: any) => setContentType(val)}
                        data={[
                          { label: 'HTML View', value: 'html' },
                          { label: 'Text View', value: 'text' },
                        ]}
                      />
                    </Group>

                    {contentType === 'html' ? (
                      <Paper withBorder radius="md" p={0} style={{ overflow: 'hidden' }}>
                        <Box style={{ height: '400px', backgroundColor: '#fff' }}>
                          {message.bodyHtml ? (
                            <iframe
                              title="Email Preview"
                              srcDoc={message.bodyHtml}
                              style={{ border: 'none', width: '100%', height: '100%' }}
                            />
                          ) : (
                            <Box p="md">
                              <Text c="dimmed" fs="italic">No HTML content available.</Text>
                            </Box>
                          )}
                        </Box>
                      </Paper>
                    ) : (
                      <Paper withBorder p="md" radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
                        <ScrollArea.Autosize mah={400} type="always">
                          <pre style={{ margin: 0, whiteSpace: 'pre-wrap', fontSize: '13px', fontFamily: 'monospace' }}>
                            {message.bodyText || 'No plain text content available.'}
                          </pre>
                        </ScrollArea.Autosize>
                      </Paper>
                    )}
                  </Stack>
                ) : (
                  <Text ta="center" py="xl" c="dimmed">Message content not found.</Text>
                )}
              </Tabs.Panel>

              <Tabs.Panel value="attachments" pt="md">
                {message?.attachments && message.attachments.length > 0 ? (
                  <Table withTableBorder withColumnBorders>
                    <Table.Thead>
                      <Table.Tr>
                        <Table.Th>Filename</Table.Th>
                        <Table.Th>Content Type</Table.Th>
                        <Table.Th>Action</Table.Th>
                      </Table.Tr>
                    </Table.Thead>
                    <Table.Tbody>
                      {message.attachments.map((att, index) => (
                        <Table.Tr key={index}>
                          <Table.Td>{att.filename}</Table.Td>
                          <Table.Td>{att.contentType}</Table.Td>
                          <Table.Td>
                            <Button
                              variant="light"
                              size="xs"
                              leftSection={<IconDownload size={14} />}
                              onClick={() => downloadAttachment(att.filename, att.contentType, att.content)}
                            >
                              Download
                            </Button>
                          </Table.Td>
                        </Table.Tr>
                      ))}
                    </Table.Tbody>
                  </Table>
                ) : (
                  <Text ta="center" c="dimmed" py="xl">No attachments.</Text>
                )}
              </Tabs.Panel>

              <Tabs.Panel value="metadata" pt="md">
                 <Paper withBorder p="md" radius="md">
                  <ScrollArea.Autosize mah={400} type="always">
                    <pre style={{ margin: 0, fontSize: '12px' }}>
                      {JSON.stringify(event.metadata?.fields || {}, null, 2)}
                    </pre>
                  </ScrollArea.Autosize>
                </Paper>
              </Tabs.Panel>
            </Tabs>
          </>
        )}
      </Stack>
    </Modal>
  );
};
