import React, { useState, useEffect } from 'react';
import { Container, Title, Group, Stack, Box, Text, rem, ThemeIcon, Table, Modal, Badge, Paper, ActionIcon, Button, useComputedColorScheme, Select } from '@mantine/core';
import { IconDownload, IconEye, IconInbox, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { inboundService } from '../features/inbound/services/inbound';
import { InboundEmail } from '../api/panmail/v1/inbound_pb';

export const InboundEmailsPage: React.FC = () => {
  const [selectedEmail, setSelectedEmail] = useState<InboundEmail | null>(null);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('50');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['inboundEmails', pageToken, pageSize],
    queryFn: () => inboundService.listInboundEmails(Number(pageSize), pageToken),
  });

  const emails = data?.emails || [];
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

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconDownload size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Inbound Processing</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>View and process incoming emails synced from your IMAP/POP3 servers or received via inbound parsing.</Text>
          </Box>
          <Select
            label="Page Size"
            size="xs"
            data={['10', '20', '50', '100']}
            value={pageSize}
            onChange={setPageSize}
            style={{ width: rem(80) }}
          />
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
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Received At</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">From</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Subject</Text></Table.Th>
                <Table.Th style={{ textAlign: 'right' }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {emails.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={4} style={{ textAlign: 'center', padding: rem(40) }}>
                    <ThemeIcon variant="light" color="gray" size={60} radius="xl" mb="md">
                       <IconInbox size={30} />
                    </ThemeIcon>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No inbound emails found. Configure an IMAP or POP3 provider to start syncing.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                emails.map((email) => (
                  <Table.Tr key={email.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">
                        {email.timestamp ? new Date(Number(email.timestamp.seconds) * 1000).toLocaleString() : '-'}
                      </Text>
                    </Table.Td>
                    <Table.Td>
                      <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{email.from}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" fw={500} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{email.subject}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Group justify="flex-end">
                        <ActionIcon variant="light" color="brand" onClick={() => setSelectedEmail(email)}>
                          <IconEye size={16} />
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
          <Group justify="space-between" align="center">
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

      <Modal
        opened={!!selectedEmail}
        onClose={() => setSelectedEmail(null)}
        title={<Text fw={800} size="lg">Email Content</Text>}
        size="xl"
        radius="md"
      >
        {selectedEmail && (
          <Stack gap="md">
            <Group justify="space-between">
              <Box>
                <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={700} tt="uppercase">From</Text>
                <Text fw={700} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{selectedEmail.from}</Text>
              </Box>
              <Box style={{ textAlign: 'right' }}>
                <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={700} tt="uppercase">Received</Text>
                <Text size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">
                  {selectedEmail.timestamp ? new Date(Number(selectedEmail.timestamp.seconds) * 1000).toLocaleString() : '-'}
                </Text>
              </Box>
            </Group>
            
            <Box>
              <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={700} tt="uppercase">Subject</Text>
              <Text fw={700} size="lg" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{selectedEmail.subject}</Text>
            </Box>

            <Paper p="md" withBorder radius="md" style={{ backgroundColor: 'light-dark(#fcfcfc, var(--mantine-color-dark-8))', borderColor: 'light-dark(var(--mantine-color-gray-3), var(--mantine-color-dark-4))' }}>
              <div
                style={{ color: 'light-dark(var(--mantine-color-black), var(--mantine-color-white))' }}
                dangerouslySetInnerHTML={{ __html: selectedEmail.bodyHtml || `<pre style="white-space: pre-wrap; font-family: inherit;">${selectedEmail.bodyText}</pre>` }}
              />
            </Paper>

            {Object.keys(selectedEmail.headers).length > 0 && (
              <Box>
                 <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={700} tt="uppercase" mb="xs">Headers</Text>
                 <Paper p="xs" withBorder radius="sm" style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))', borderColor: 'light-dark(var(--mantine-color-gray-3), var(--mantine-color-dark-4))' }}>
                    {Object.entries(selectedEmail.headers).map(([k, v]) => (
                      <Group key={k} gap="xs">
                        <Text size="xs" fw={700} c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{k}:</Text>
                        <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">{v}</Text>
                      </Group>
                    ))}
                 </Paper>
              </Box>
            )}
          </Stack>
        )}
      </Modal>
    </Container>
  );
};
