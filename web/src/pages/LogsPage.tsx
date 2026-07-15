import React, { useState, useEffect } from 'react';
import { Container, Title, Table, ScrollArea, Badge, Text, Group, Box, rem, ThemeIcon, Stack, Paper, useComputedColorScheme, Switch, ActionIcon, Button, Select } from '@mantine/core';
import { IconHistory, IconBroadcast, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { logClient } from '../services/client';
import { LogEntry } from '../api/panmail/v1/log_pb';

export const LogsPage: React.FC = () => {
  const [isLive, setIsLive] = useState(false);
  const [liveLogs, setLiveLogs] = useState<LogEntry[]>([]);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('100');

  const { data: logsData, isLoading, refetch } = useQuery({
    queryKey: ['logs', pageToken, pageSize],
    queryFn: async () => {
      const response = await logClient.listLogs({
        pageSize: Number(pageSize),
        pageToken,
      });
      return response;
    },
    enabled: !isLive,
  });

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const logs = logsData?.logs || [];
  const nextPageToken = logsData?.nextPageToken;

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

  useEffect(() => {
    if (!isLive) {
      setLiveLogs([]);
      return;
    }

    const controller = new AbortController();
    const stream = async () => {
      try {
        for await (const log of logClient.streamLogs({}, { signal: controller.signal })) {
          setLiveLogs(prev => [log, ...prev].slice(0, 500));
        }
      } catch (err: any) {
        if (err.name !== 'AbortError' && err.code !== 'canceled') {
          console.error('Stream error:', err);
        }
      }
    };

    stream();
    return () => controller.abort();
  }, [isLive]);

  const displayLogs = isLive ? liveLogs : logs;

  const getLevelColor = (level: string) => {
    switch (level.toUpperCase()) {
      case 'ERROR': return 'red';
      case 'WARN': return 'yellow';
      case 'INFO': return 'indigo';
      case 'DEBUG': return 'gray';
      default: return 'gray';
    }
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Group gap="xs" mb={4}>
              <ThemeIcon variant="light" color="brand" size="md">
                <IconHistory size={18} />
              </ThemeIcon>
              <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Activity Logs</Title>
            </Group>
            <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Monitor system events and email delivery status in real-time.</Text>
          </Box>
          <Group align="flex-end" gap="sm">
            {!isLive && (
              <Select
                label="Page Size"
                size="xs"
                data={['20', '50', '100', '200']}
                value={pageSize}
                onChange={setPageSize}
                style={{ width: rem(80) }}
              />
            )}
            <Paper withBorder p="xs" radius="md" style={{ backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))' }}>
            <Group gap="sm">
              <Badge
                variant="dot"
                color={isLive ? 'red' : 'gray'}
                size="lg"
                styles={{ root: { textTransform: 'none' } }}
              >
                {isLive ? 'Live Streaming' : 'Static View'}
              </Badge>
              <Switch
                label="Real-time"
                checked={isLive}
                onChange={(event) => setIsLive(event.currentTarget.checked)}
                size="sm"
                onLabel={<IconBroadcast size={14} />}
                offLabel={<IconBroadcast size={14} />}
              />
            </Group>
          </Paper>
        </Group>
      </Group>

      <Paper withBorder radius="md" style={{ overflow: 'hidden', backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))', borderColor: 'light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
          <ScrollArea h={700} offsetScrollbars>
            <Table verticalSpacing="sm" horizontalSpacing="md">
              <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))', position: 'sticky', top: 0, zIndex: 10 }}>
                <Table.Tr>
                  <Table.Th style={{ width: rem(200) }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Timestamp</Text></Table.Th>
                  <Table.Th style={{ width: rem(100) }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Level</Text></Table.Th>
                  <Table.Th style={{ width: rem(150) }}><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Service</Text></Table.Th>
                  <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Message</Text></Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {isLoading && !isLive ? (
                  <Table.Tr>
                    <Table.Td colSpan={4}>
                      <Text ta="center" py="xl" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Loading logs...</Text>
                    </Table.Td>
                  </Table.Tr>
                ) : displayLogs?.length === 0 ? (
                  <Table.Tr>
                    <Table.Td colSpan={4}>
                      <Text ta="center" py="xl" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">
                        {isLive ? 'Waiting for logs...' : 'No logs found'}
                      </Text>
                    </Table.Td>
                  </Table.Tr>
                ) : displayLogs?.map((log) => (
                  <Table.Tr key={log.id} style={{ borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
                    <Table.Td>
                      <Text size="sm" fw={500} c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">{log.timestamp?.toDate().toLocaleString()}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge color={getLevelColor(log.level)} variant="light" size="sm" radius="sm">
                        {log.level}
                      </Badge>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" fw={700} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{log.service}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm" c="light-dark(var(--mantine-color-gray-9), var(--mantine-color-white))">{log.message}</Text>
                      {log.metadata && Object.keys(log.metadata).length > 0 && (
                         <Box mt={4}>
                           <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" style={{ fontFamily: 'monospace', wordBreak: 'break-all' }}>
                             {JSON.stringify(log.metadata)}
                           </Text>
                         </Box>
                      )}
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </ScrollArea>
        </Paper>

        {!isLive && (nextPageToken || history.length > 0) && (
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
    </Container>
  );
};
