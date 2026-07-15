import React, { useState, useEffect } from 'react';
import { Container, Title, Text, Group, Stack, Box, Paper, rem, ThemeIcon, Table, Button, Badge, Select } from '@mantine/core';
import { IconArchive, IconDownload, IconFileText, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { analyticsService } from '../features/analytics/services/analytics';
import { notifications } from '@mantine/notifications';

export const ArchivesPage: React.FC = () => {
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('50');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [pageSize]);

  const { data, isLoading } = useQuery({
    queryKey: ['archives', pageToken, pageSize],
    queryFn: () => analyticsService.listArchives(Number(pageSize), pageToken),
  });

  const archives = data?.archives || [];
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

  const handleDownload = async (id: string, filename: string) => {
    try {
      const res = await analyticsService.downloadArchive(id);
      const blob = new Blob([res.content], { type: 'application/json' });
      const url = window.URL.createObjectURL(blob);
      const a = document.createElement('a');
      a.href = url;
      a.download = filename;
      document.body.appendChild(a);
      a.click();
      window.URL.revokeObjectURL(url);
      document.body.removeChild(a);
      
      notifications.show({
        title: 'Success',
        message: 'Archive downloaded successfully',
        color: 'green',
      });
    } catch (err: any) {
      notifications.show({
        title: 'Error',
        message: err.message || 'Failed to download archive',
        color: 'red',
      });
    }
  };

  const formatSize = (bytes: bigint | number) => {
    const b = Number(bytes);
    if (b === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB'];
    const i = Math.floor(Math.log(b) / Math.log(k));
    return parseFloat((b / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
  };

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="indigo" size="md">
              <IconArchive size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Event Log Archives</Title>
          </Group>
          <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>
            Access historical delivery event logs that have been moved out of the main database.
          </Text>
        </Box>

        <Group justify="flex-end">
           <Select
              label="Page Size"
              size="xs"
              data={['10', '20', '50', '100']}
              value={pageSize}
              onChange={setPageSize}
              style={{ width: rem(80) }}
            />
        </Group>

        <Paper withBorder radius="md" style={{ overflow: 'hidden' }}>
          <Table verticalSpacing="md" horizontalSpacing="lg">
            <Table.Thead style={{ backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-7))' }}>
              <Table.Tr>
                <Table.Th><Text fw={700} size="sm">Filename</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm">Created At</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm">Size</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" style={{ textAlign: 'right' }}>Actions</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {isLoading ? (
                <Table.Tr>
                  <Table.Td colSpan={4} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Text c="dimmed">Loading archives...</Text>
                  </Table.Td>
                </Table.Tr>
              ) : archives.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={4} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Stack align="center" gap="xs">
                      <IconFileText size={48} color="gray" opacity={0.5} />
                      <Text c="dimmed" fw={500}>No archives found yet.</Text>
                      <Text size="xs" c="dimmed">Archives are automatically created during the daily cleanup process.</Text>
                    </Stack>
                  </Table.Td>
                </Table.Tr>
              ) : (
                archives.map((archive) => (
                  <Table.Tr key={archive.id}>
                    <Table.Td>
                      <Group gap="xs">
                        <IconFileText size={16} color="indigo" />
                        <Text size="sm" fw={600}>{archive.filename}</Text>
                      </Group>
                    </Table.Td>
                    <Table.Td>
                      <Text size="sm">{archive.createdAt?.toDate().toLocaleString()}</Text>
                    </Table.Td>
                    <Table.Td>
                      <Badge variant="light" color="gray" radius="sm">
                        {formatSize(archive.size)}
                      </Badge>
                    </Table.Td>
                    <Table.Td style={{ textAlign: 'right' }}>
                      <Button 
                        variant="light" 
                        size="xs" 
                        leftSection={<IconDownload size={14} />}
                        onClick={() => handleDownload(archive.id, archive.filename)}
                      >
                        Download
                      </Button>
                    </Table.Td>
                  </Table.Tr>
                ))
              )}
            </Table.Tbody>
          </Table>
        </Paper>

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
    </Container>
  );
};
