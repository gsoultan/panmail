import React, { useState, useEffect } from 'react';
import { Container, Title, Group, Stack, Box, Text, rem, ThemeIcon, Table, Badge, Paper, TextInput, Select, ActionIcon, Button, Pagination } from '@mantine/core';
import { DatePickerInput } from '@mantine/dates';
import { IconChartBar, IconMail, IconClick, IconEye, IconBan, IconAlertCircle, IconSend, IconCornerUpLeft, IconX, IconClock, IconSearch, IconFilter, IconCalendar, IconRotate, IconExternalLink, IconArchive, IconChevronLeft, IconChevronRight } from '@tabler/icons-react';
import { useQuery } from '@tanstack/react-query';
import { useDebouncedValue, useDisclosure } from '@mantine/hooks';
import { useNavigate } from '@tanstack/react-router';
import { analyticsService } from '../features/analytics/services/analytics';
import { EmailEventType } from '../api/panmail/v1/event_pb';
import { EventDetailModal } from '../features/analytics/components/EventDetailModal';

const eventTypeConfig: Record<EmailEventType, { label: string, color: string, icon: any }> = {
  [EmailEventType.UNSPECIFIED]: { label: 'Unknown', color: 'gray', icon: IconAlertCircle },
  [EmailEventType.SENT]: { label: 'Sent', color: 'blue', icon: IconSend },
  [EmailEventType.DELIVERED]: { label: 'Delivered', color: 'green', icon: IconMail },
  [EmailEventType.OPENED]: { label: 'Opened', color: 'grape', icon: IconEye },
  [EmailEventType.CLICKED]: { label: 'Clicked', color: 'cyan', icon: IconClick },
  [EmailEventType.BOUNCED]: { label: 'Bounced', color: 'red', icon: IconBan },
  [EmailEventType.HARD_BOUNCE]: { label: 'Hard Bounce', color: 'red.9', icon: IconX },
  [EmailEventType.SOFT_BOUNCE]: { label: 'Soft Bounce', color: 'orange.6', icon: IconCornerUpLeft },
  [EmailEventType.SPAM_REPORT]: { label: 'Spam Report', color: 'orange', icon: IconBan },
  [EmailEventType.UNSUBSCRIBED]: { label: 'Unsubscribed', color: 'yellow', icon: IconBan },
  [EmailEventType.DROPPED]: { label: 'Dropped', color: 'pink', icon: IconBan },
  [EmailEventType.REJECTED]: { label: 'Rejected', color: 'red.8', icon: IconX },
  [EmailEventType.PENDING]: { label: 'Pending', color: 'yellow', icon: IconClock },
  [EmailEventType.DEFERRED]: { label: 'Deferred', color: 'orange', icon: IconAlertCircle },
  [EmailEventType.COMPLAINED]: { label: 'Complained', color: 'red.5', icon: IconBan },
};

const eventTypeOptions = [
  { label: 'Sent', value: EmailEventType.SENT.toString() },
  { label: 'Delivered', value: EmailEventType.DELIVERED.toString() },
  { label: 'Opened', value: EmailEventType.OPENED.toString() },
  { label: 'Clicked', value: EmailEventType.CLICKED.toString() },
  { label: 'Bounced', value: EmailEventType.BOUNCED.toString() },
  { label: 'Hard Bounce', value: EmailEventType.HARD_BOUNCE.toString() },
  { label: 'Soft Bounce', value: EmailEventType.SOFT_BOUNCE.toString() },
  { label: 'Spam Report', value: EmailEventType.SPAM_REPORT.toString() },
  { label: 'Unsubscribed', value: EmailEventType.UNSUBSCRIBED.toString() },
  { label: 'Rejected', value: EmailEventType.REJECTED.toString() },
];

export const AnalyticsPage: React.FC = () => {
  const [recipient, setRecipient] = useState('');
  const [debouncedRecipient] = useDebouncedValue(recipient, 300);
  const [subject, setSubject] = useState('');
  const [debouncedSubject] = useDebouncedValue(subject, 300);
  const [eventType, setEventType] = useState<string | null>(null);
  const [dateRange, setDateRange] = useState<[Date | null, Date | null]>([null, null]);
  const [pageToken, setPageToken] = useState<string | undefined>(undefined);
  const [history, setHistory] = useState<string[]>([]);
  const [pageSize, setPageSize] = useState<string | null>('50');

  useEffect(() => {
    setPageToken(undefined);
    setHistory([]);
  }, [debouncedRecipient, debouncedSubject, eventType, dateRange, pageSize]);

  const [selectedEventId, setSelectedEventId] = useState<string | null>(null);
  const [detailOpened, { open: openDetail, close: closeDetail }] = useDisclosure(false);
  const navigate = useNavigate();

  const { data, isLoading } = useQuery({
    queryKey: ['analyticsEvents', debouncedRecipient, debouncedSubject, eventType, dateRange, pageToken, pageSize],
    queryFn: () => analyticsService.listEvents(
      Number(pageSize),
      pageToken || '',
      debouncedRecipient,
      eventType ? Number(eventType) : 0,
      dateRange[0] || undefined,
      dateRange[1] || undefined,
      undefined, // messageId
      true, // latestOnly
      false, // recipientExact
      debouncedSubject
    ),
    refetchInterval: 5000,
  });

  const handleNext = () => {
    if (data?.nextPageToken) {
      setHistory([...history, pageToken ?? '']);
      setPageToken(data.nextPageToken);
    }
  };

  const handlePrev = () => {
    const newHistory = [...history];
    const prev = newHistory.pop();
    setHistory(newHistory);
    setPageToken(prev === '' ? undefined : prev);
  };

  const { data: metricsData } = useQuery({
    queryKey: ['analyticsMetrics'],
    queryFn: () => analyticsService.getMetrics(),
    refetchInterval: 5000,
  });

  const metrics = metricsData?.metrics || {};
  const totalDelivered = Number(metrics['DELIVERED'] || 0);
  const totalBounced = Number(metrics['BOUNCED'] || 0) + Number(metrics['HARD_BOUNCE'] || 0) + Number(metrics['SOFT_BOUNCE'] || 0);
  const totalSent = Number(metrics['SENT'] || 0);

  const resetFilters = () => {
    setRecipient('');
    setSubject('');
    setEventType(null);
    setDateRange([null, null]);
  };

  const events = data?.events || [];

  return (
    <Container size="xl" py="xl">
      <Stack gap="xl">
        <Box>
          <Group gap="xs" mb={4}>
            <ThemeIcon variant="light" color="brand" size="md">
              <IconChartBar size={18} />
            </ThemeIcon>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Delivery Analytics</Title>
            <Button 
              variant="subtle" 
              size="xs" 
              ml="auto" 
              leftSection={<IconArchive size={14} />}
              onClick={() => navigate({ to: '/archives' })}
            >
              View Archives
            </Button>
          </Group>
          <Text c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))" fw={500}>Unified tracking of email events across all your providers.</Text>
        </Box>

        <Paper p="md" withBorder radius="md">
          <Group align="flex-end" gap="md">
            <TextInput
              label="Recipient"
              placeholder="Email..."
              leftSection={<IconSearch size={16} />}
              value={recipient}
              onChange={(e) => setRecipient(e.currentTarget.value)}
              style={{ width: rem(200) }}
            />
            <TextInput
              label="Subject"
              placeholder="Search by subject..."
              leftSection={<IconSearch size={16} />}
              value={subject}
              onChange={(e) => setSubject(e.currentTarget.value)}
              style={{ flex: 1 }}
            />
            <Select
              label="Event Type"
              placeholder="Filter by type"
              leftSection={<IconFilter size={16} />}
              data={eventTypeOptions}
              value={eventType}
              onChange={setEventType}
              clearable
              style={{ width: rem(180) }}
            />
            <DatePickerInput
              type="range"
              label="Date Range"
              placeholder="Filter by date"
              leftSection={<IconCalendar size={16} />}
              value={dateRange}
              onChange={(val) => setDateRange(val as [Date | null, Date | null])}
              clearable
              style={{ width: rem(250) }}
            />
            <Select
              label="Page Size"
              placeholder="Size"
              data={['10', '20', '50', '100']}
              value={pageSize}
              onChange={setPageSize}
              style={{ width: rem(80) }}
            />
            <ActionIcon
              variant="light"
              color="gray"
              size="lg"
              onClick={resetFilters}
              title="Reset Filters"
              mb={2}
            >
              <IconRotate size={20} />
            </ActionIcon>
          </Group>
        </Paper>

        <Group grow>
          <Paper p="md" withBorder radius="md">
            <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" fw={700} tt="uppercase">Total Sent</Text>
            <Text size="xl" fw={800} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{totalSent}</Text>
          </Paper>
          <Paper p="md" withBorder radius="md">
            <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" fw={700} tt="uppercase">Total Delivered</Text>
            <Text size="xl" fw={800} c="green.6">
              {totalDelivered}
            </Text>
          </Paper>
          <Paper p="md" withBorder radius="md">
            <Text size="xs" c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-1))" fw={700} tt="uppercase">Total Bounces</Text>
            <Text size="xl" fw={800} c="red.6">
              {totalBounced}
            </Text>
          </Paper>
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
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Timestamp</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Recipient</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Event Type</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Provider Name</Text></Table.Th>
                <Table.Th><Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">Subject</Text></Table.Th>
              </Table.Tr>
            </Table.Thead>
            <Table.Tbody>
              {events.length === 0 ? (
                <Table.Tr>
                  <Table.Td colSpan={5} style={{ textAlign: 'center', padding: rem(40) }}>
                    <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))" fw={500}>No events tracked yet. Configure webhooks in your providers to see data here.</Text>
                  </Table.Td>
                </Table.Tr>
              ) : (
                events.map((event) => {
                  const config = eventTypeConfig[event.type] || eventTypeConfig[EmailEventType.UNSPECIFIED];
                  const Icon = config.icon;
                  return (
                    <Table.Tr 
                      key={event.id} 
                      onClick={() => {
                        setSelectedEventId(event.id);
                        openDetail();
                      }}
                      style={{ 
                        borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))',
                        cursor: 'pointer'
                      }}
                    >
                      <Table.Td>
                        <Text size="sm" c="light-dark(var(--mantine-color-gray-7), var(--mantine-color-dark-1))">
                          {event.timestamp ? new Date(Number(event.timestamp.seconds) * 1000).toLocaleString() : '-'}
                        </Text>
                      </Table.Td>
                      <Table.Td>
                        <Text fw={700} size="sm" c="light-dark(var(--mantine-color-black), var(--mantine-color-white))">{event.recipient}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Badge
                          variant="light"
                          color={config.color}
                          leftSection={<Icon size={12} />}
                          radius="sm"
                        >
                          {config.label}
                        </Badge>
                      </Table.Td>
                      <Table.Td>
                        <Text size="sm" fw={500} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">{event.providerName || event.providerId}</Text>
                      </Table.Td>
                      <Table.Td>
                        <Group justify="space-between" wrap="nowrap">
                          <Text size="sm" fw={600} c="light-dark(var(--mantine-color-black), var(--mantine-color-white))" truncate style={{ maxWidth: rem(200) }}>
                            {event.subject || event.messageId}
                          </Text>
                          <ActionIcon variant="subtle" size="sm" color="gray">
                            <IconExternalLink size={14} />
                          </ActionIcon>
                        </Group>
                      </Table.Td>
                    </Table.Tr>
                  );
                })
              )}
            </Table.Tbody>
          </Table>
        </Box>

        {(data?.nextPageToken || history.length > 0) && (
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
                disabled={!data?.nextPageToken}
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

      <EventDetailModal
        eventId={selectedEventId}
        opened={detailOpened}
        onClose={closeDetail}
        eventTypeConfig={eventTypeConfig}
      />
    </Container>
  );
};
