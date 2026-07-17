import React, { useState, useMemo } from 'react';
import { Container, Grid, Paper, Text, Group, ThemeIcon, Title, Stack, rem, Box, Tabs, SegmentedControl, Tooltip, SimpleGrid, RingProgress, ActionIcon, Badge, Select, Modal, Button } from '@mantine/core';
import { DatePickerInput } from '@mantine/dates';
import { IconSend, IconMail, IconBan, IconClick, IconEye, IconAlertCircle, IconClock, IconActivity, IconCpu, IconServer, IconInfoCircle, IconCalendar, IconDatabase, IconChartBar, IconDownload, IconUpload, IconChevronDown, IconAdjustmentsHorizontal } from '@tabler/icons-react';
import { AreaChart, DonutChart, BarChart } from '@mantine/charts';
import { useQuery } from '@tanstack/react-query';
import { analyticsService } from '../features/analytics/services/analytics';
import { EmailEventType } from '../api/panmail/v1/event_pb';

interface StatCardProps {
  title: string;
  value: number | string;
  icon: React.FC<any>;
  color: string;
  description: string;
}

const StatCard = ({ title, value, icon: Icon, color, description }: StatCardProps) => (
  <Paper p="md" radius="md" withBorder>
    <Group justify="space-between" wrap="nowrap">
      <div>
        <Group gap={4} align="center">
          <Text size="xs" c="dimmed" fw={700} tt="uppercase">
            {title}
          </Text>
          <Tooltip label={description} multiline w={220} withArrow transitionProps={{ transition: 'pop', duration: 300 }}>
            <ActionIcon variant="subtle" color="gray" size="xs">
              <IconInfoCircle size={14} />
            </ActionIcon>
          </Tooltip>
        </Group>
        <Text size="xl" fw={800} mt={4}>
          {value}
        </Text>
      </div>
      <ThemeIcon color={color} variant="light" size={42} radius="md">
        <Icon size={22} stroke={1.5} />
      </ThemeIcon>
    </Group>
  </Paper>
);

const timeRangeOptions = [
  { label: 'Last 30m', value: '30m', minutes: 30, granularity: 'minute' },
  { label: 'Last 1h', value: '1h', minutes: 60, granularity: 'minute' },
  { label: 'Last 3h', value: '3h', minutes: 180, granularity: 'minute' },
  { label: 'Last 6h', value: '6h', minutes: 360, granularity: 'minute' },
  { label: 'Last 24h', value: '24h', minutes: 1440, granularity: 'hour' },
  { label: 'Last 3d', value: '3d', minutes: 4320, granularity: 'hour' },
  { label: 'Last 7d', value: '7d', minutes: 10080, granularity: 'day' },
  { label: 'Last 14d', value: '14d', minutes: 20160, granularity: 'day' },
  { label: 'Last 1mo', value: '1mo', minutes: 43200, granularity: 'day' },
  { label: 'Last 3mo', value: '3mo', minutes: 129600, granularity: 'day' },
  { label: 'Last 6mo', value: '6mo', minutes: 259200, granularity: 'day' },
  { label: 'Last 1y', value: '1y', minutes: 525600, granularity: 'day' },
  { label: 'All Time', value: 'all', minutes: 0, granularity: 'day' },
  { label: 'Custom Range', value: 'custom', minutes: 0, granularity: 'day' },
];

export const DashboardPage: React.FC = () => {
  const [timeRange, setTimeRange] = useState('30m');
  const [activeTab, setActiveTab] = useState<string | null>('delivery');
  const [customRange, setCustomRange] = useState<[Date | null, Date | null]>([null, null]);
  const [customModalOpened, setCustomModalOpened] = useState(false);

  const { startTime, endTime, granularity } = useMemo(() => {
    if (timeRange === 'custom') {
      return {
        startTime: customRange[0] || undefined,
        endTime: customRange[1] || undefined,
        granularity: 'day'
      };
    }
    const option = timeRangeOptions.find(o => o.value === timeRange);
    if (!option || option.value === 'all') {
      return { startTime: undefined, endTime: undefined, granularity: 'day' };
    }
    const end = new Date();
    const start = new Date(end.getTime() - option.minutes * 60 * 1000);
    return { startTime: start, endTime: end, granularity: option.granularity };
  }, [timeRange, customRange]);

  const { data: metricsData } = useQuery({
    queryKey: ['dashboardMetrics', startTime?.getTime(), endTime?.getTime()],
    queryFn: () => analyticsService.getMetrics(startTime, endTime),
    refetchInterval: 2000,
  });

  const { data: tsData } = useQuery({
    queryKey: ['timeSeriesMetrics', startTime?.getTime(), endTime?.getTime(), granularity],
    queryFn: () => analyticsService.getTimeSeriesMetrics(startTime, endTime, granularity),
    refetchInterval: 2000,
  });

  const { data: perfData } = useQuery({
    queryKey: ['performanceMetrics'],
    queryFn: () => analyticsService.getPerformanceMetrics(),
    refetchInterval: 2000,
  });

  const metrics = metricsData?.metrics || {};
  const extendedMetrics = metricsData?.extendedMetrics || [];

  const getMetric = (type: EmailEventType) => {
    const key = EmailEventType[type].replace('EMAIL_EVENT_TYPE_', '');
    return Number(metrics[key] || 0);
  };

  const sent = getMetric(EmailEventType.SENT);
  const delivered = getMetric(EmailEventType.DELIVERED);
  const opened = getMetric(EmailEventType.OPENED);
  const clicked = getMetric(EmailEventType.CLICKED);
  const bounced = getMetric(EmailEventType.BOUNCED) + getMetric(EmailEventType.HARD_BOUNCE) + getMetric(EmailEventType.SOFT_BOUNCE);
  const inbound = Number(metrics['INBOUND_RECEIVED'] || 0);

  const deliveryRate = sent > 0 ? (delivered / sent) * 100 : 0;
  const openRate = delivered > 0 ? (opened / delivered) * 100 : 0;
  const clickRate = opened > 0 ? (clicked / opened) * 100 : 0;

  const chartData = Object.entries(tsData?.data || {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([date, values]) => ({
      date: granularity === 'minute' ? date.split(' ')[1] : date,
      Sent: Number(values.metrics['SENT'] || 0),
      Delivered: Number(values.metrics['DELIVERED'] || 0),
      Opened: Number(values.metrics['OPENED'] || 0),
      Inbound: Number(values.metrics['INBOUND_RECEIVED'] || 0),
    }));

  const resourceChartData = useMemo(() => {
    return (perfData?.resourceHistory || [])
      .map(p => ({
        time: p.timestamp ? new Date(Number(p.timestamp.seconds) * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '',
        CPU: Number(p.cpuUsage.toFixed(1)),
        Memory: Number((Number(p.memoryUsage) / 1024 / 1024 / 1024).toFixed(2)), // GB
        Load: Number(p.systemLoad15.toFixed(2)),
        timestamp: Number(p.timestamp?.seconds || 0)
      }))
      .sort((a, b) => a.timestamp - b.timestamp);
  }, [perfData?.resourceHistory]);

  const outboundMetrics = extendedMetrics.filter(m => m.category === 'outbound');

  return (
    <Container size="xl" py="md">
      <Stack gap="md">
        <Group justify="space-between" align="flex-end">
          <Box>
            <Title order={2} style={{ fontWeight: 800, letterSpacing: rem(-0.5) }}>Dashboard</Title>
            <Text c="dimmed" size="sm" fw={500}>Real-time monitoring of your email gateway performance.</Text>
          </Box>
          <Paper withBorder p={4} radius="md">
            <Group gap="xs">
              <IconCalendar size={16} color="var(--mantine-color-dimmed)" />
              <Select
                size="xs"
                value={timeRange}
                onChange={(val) => {
                  if (val === 'custom') {
                    setCustomModalOpened(true);
                  }
                  setTimeRange(val || '30m');
                }}
                data={timeRangeOptions}
                rightSection={<IconChevronDown size={14} />}
                variant="unstyled"
                styles={{ input: { fontWeight: 700, width: 140 } }}
              />
            </Group>
          </Paper>
        </Group>

        <Modal
          opened={customModalOpened}
          onClose={() => setCustomModalOpened(false)}
          title="Select Custom Range"
          centered
        >
          <Stack>
            <DatePickerInput
              type="range"
              label="Time Range"
              placeholder="Pick dates range"
              value={customRange}
              onChange={(val: any) => setCustomRange(val)}
            />
            <Button onClick={() => setCustomModalOpened(false)} fullWidth>Apply Range</Button>
          </Stack>
        </Modal>

        <Tabs value={activeTab} onChange={setActiveTab} variant="pills" radius="md">
          <Tabs.List mb="md">
            <Tabs.Tab value="delivery" leftSection={<IconSend size={16} />}>Delivery</Tabs.Tab>
            <Tabs.Tab value="inbound" leftSection={<IconDownload size={16} />}>Inbound</Tabs.Tab>
            <Tabs.Tab value="system" leftSection={<IconActivity size={16} />}>System Health</Tabs.Tab>
          </Tabs.List>

          <Tabs.Panel value="delivery">
            <Stack gap="md">
              <SimpleGrid cols={{ base: 1, sm: 2, md: 5 }} spacing="md">
                {outboundMetrics.slice(0, 4).map(m => (
                  <StatCard
                    key={m.key}
                    title={m.label}
                    value={m.value.toString()}
                    icon={m.key === 'SENT' ? IconSend : m.key === 'DELIVERED' ? IconMail : m.key === 'OPENED' ? IconEye : IconClick}
                    color={m.key === 'SENT' ? 'blue' : m.key === 'DELIVERED' ? 'green' : m.key === 'OPENED' ? 'grape' : 'cyan'}
                    description={m.description}
                  />
                ))}
                <StatCard
                  title="Throughput"
                  value={`${perfData?.sentPerSecond?.toFixed(2) || '0.00'} /s`}
                  icon={IconActivity}
                  color="indigo"
                  description="Real-time emails sent per second."
                />
              </SimpleGrid>

              <Grid>
                <Grid.Col span={{ base: 12, md: 8 }}>
                  <Paper p="xl" radius="md" withBorder h={400}>
                    <Title order={4} mb="xl" fw={700}>Delivery Trends</Title>
                    {chartData.length > 0 ? (
                      <AreaChart
                        h={300}
                        data={chartData}
                        dataKey="date"
                        series={[
                          { name: 'Sent', color: 'blue.6' },
                          { name: 'Delivered', color: 'green.6' },
                          { name: 'Opened', color: 'grape.6' },
                        ]}
                        curveType="monotone"
                        tickLine="none"
                        gridAxis="xy"
                        withLegend
                      />
                    ) : (
                      <Box h={300} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                        <Text c="dimmed">No delivery data for this period.</Text>
                      </Box>
                    )}
                  </Paper>
                </Grid.Col>
                <Grid.Col span={{ base: 12, md: 4 }}>
                  <Paper p="xl" radius="md" withBorder h={400}>
                    <Title order={4} mb="xl" fw={700}>Conversion Funnel</Title>
                    <Stack align="center" gap="xl">
                      <Group gap={40}>
                        <Stack align="center" gap={4}>
                          <RingProgress
                            size={100}
                            thickness={8}
                            roundCaps
                            sections={[{ value: deliveryRate, color: 'green' }]}
                            label={<Text size="xs" ta="center" fw={700}>{deliveryRate.toFixed(1)}%</Text>}
                          />
                          <Text size="xs" fw={700}>Delivery</Text>
                        </Stack>
                        <Stack align="center" gap={4}>
                          <RingProgress
                            size={100}
                            thickness={8}
                            roundCaps
                            sections={[{ value: openRate, color: 'grape' }]}
                            label={<Text size="xs" ta="center" fw={700}>{openRate.toFixed(1)}%</Text>}
                          />
                          <Text size="xs" fw={700}>Open Rate</Text>
                        </Stack>
                      </Group>
                      <SimpleGrid cols={2} w="100%">
                         <Paper withBorder p="xs" radius="sm">
                            <Text size="xs" c="dimmed" fw={700}>BOUNCES</Text>
                            <Text fw={700}>{bounced}</Text>
                         </Paper>
                         <Paper withBorder p="xs" radius="sm">
                            <Text size="xs" c="dimmed" fw={700}>CLICK RATE</Text>
                            <Text fw={700}>{clickRate.toFixed(1)}%</Text>
                         </Paper>
                      </SimpleGrid>
                    </Stack>
                  </Paper>
                </Grid.Col>
              </Grid>
            </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="inbound">
             <Stack gap="md">
                <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="md">
                   <StatCard
                      title="Total Inbound"
                      value={inbound.toString()}
                      icon={IconDownload}
                      color="indigo"
                      description="Total emails received by the gateway."
                   />
                   <StatCard
                      title="Processed"
                      value={inbound.toString()}
                      icon={IconActivity}
                      color="teal"
                      description="Successfully processed inbound messages."
                   />
                   <StatCard
                      title="Spam / Rejected"
                      value="0"
                      icon={IconBan}
                      color="orange"
                      description="Messages rejected by filters (Coming soon)."
                   />
                </SimpleGrid>
                <Paper p="xl" radius="md" withBorder h={400}>
                   <Title order={4} mb="xl" fw={700}>Inbound Activity</Title>
                   {chartData.length > 0 ? (
                      <BarChart
                        h={300}
                        data={chartData}
                        dataKey="date"
                        series={[{ name: 'Inbound', color: 'indigo.6' }]}
                        tickLine="none"
                        gridAxis="xy"
                      />
                   ) : (
                      <Box h={300} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                         <Text c="dimmed">No inbound activity for this period.</Text>
                      </Box>
                   )}
                </Paper>
             </Stack>
          </Tabs.Panel>

          <Tabs.Panel value="system">
             <Stack gap="md">
                <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md">
                   <StatCard
                     title="CPU Usage"
                     value={`${perfData?.cpuUsage?.toFixed(1) || '0.0'}%`}
                     icon={IconCpu}
                     color="red"
                     description="Current overall CPU utilization across all cores."
                   />
                   <StatCard
                     title="System Load"
                     value={`${perfData?.cpuCores ? ((perfData.systemLoad15 / perfData.cpuCores) * 100).toFixed(1) : '0.0'}%`}
                     icon={IconActivity}
                     color="orange"
                     description="System load average (15m) relative to available CPU cores."
                   />
                   <StatCard
                     title="Memory Usage"
                     value={perfData?.memoryUsage ? `${(Number(perfData.memoryUsage) / 1024 / 1024 / 1024).toFixed(2)} GB` : '0 GB'}
                     icon={IconServer}
                     color="teal"
                     description="Current physical memory in use by the system."
                   />
                   <StatCard
                     title="Total Memory"
                     value={perfData?.totalMemory ? `${(Number(perfData.totalMemory) / 1024 / 1024 / 1024).toFixed(1)} GB` : '0 GB'}
                     icon={IconDatabase}
                     color="violet"
                     description="Total physical memory installed on the host."
                   />
                </SimpleGrid>

                <SimpleGrid cols={{ base: 1, sm: 2, md: 4 }} spacing="md">
                    <StatCard
                      title="Emails / Sec"
                      value={perfData?.sentPerSecond?.toFixed(2) || '0.00'}
                      icon={IconSend}
                      color="indigo"
                      description="Real-time outbound throughput."
                    />
                    <StatCard
                      title="CPU Cores"
                      value={perfData?.cpuCores || '0'}
                      icon={IconCpu}
                      color="blue"
                      description="Total number of logical CPU cores available."
                    />
                    <StatCard
                      title="Disk Usage"
                      value={perfData?.diskUsage ? `${(Number(perfData.diskUsage) / 1024 / 1024).toFixed(2)} MB` : '0 MB'}
                      icon={IconDatabase}
                      color="yellow"
                      description="Total size of all local Pebble databases."
                    />
                    <StatCard
                      title="Uptime"
                      value={perfData?.uptimeSeconds ? `${Math.floor(Number(perfData.uptimeSeconds) / 3600)}h ${Math.floor((Number(perfData.uptimeSeconds) % 3600) / 60)}m` : '0m'}
                      icon={IconClock}
                      color="gray"
                      description="Time elapsed since the last service restart."
                    />
                </SimpleGrid>
                
                <Paper p="xl" radius="md" withBorder>
                   <Title order={4} mb="xl" fw={700}>Resource Consumption Trends (24h)</Title>
                   {resourceChartData.length > 0 ? (
                      <AreaChart
                        h={300}
                        data={resourceChartData}
                        dataKey="time"
                        series={[
                          { name: 'CPU', color: 'red.6', label: 'CPU Usage (%)' },
                          { name: 'Load', color: 'orange.6', label: 'System Load (%)' },
                          { name: 'Memory', color: 'blue.6', label: 'Memory (GB)' },
                        ]}
                        curveType="monotone"
                        tickLine="none"
                        gridAxis="xy"
                        withLegend
                        valueFormatter={(value) => value.toString()}
                      />
                   ) : (
                      <Box h={300} style={{ display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                         <Stack align="center" gap="xs">
                            <ThemeIcon size="xl" radius="xl" color="gray" variant="light">
                               <IconChartBar size={24} />
                            </ThemeIcon>
                            <Text c="dimmed" size="sm">Collecting historical performance data points...</Text>
                         </Stack>
                      </Box>
                   )}
                </Paper>

                <Grid>
                   <Grid.Col span={{ base: 12, md: 6 }}>
                      <Paper p="xl" radius="md" withBorder h="100%">
                         <Title order={4} mb="lg" fw={700}>Runtime Metrics</Title>
                         <Stack gap="xs">
                            <Group justify="space-between">
                               <Text size="sm" fw={600}>Goroutines</Text>
                               <Badge variant="light" color="blue" radius="xl">{perfData?.goroutines || 0}</Badge>
                            </Group>
                            <Group justify="space-between">
                               <Text size="sm" fw={600}>Open File Handles</Text>
                               <Badge variant="light" color="cyan" radius="xl">{perfData?.openFiles || 0}</Badge>
                            </Group>
                            <Group justify="space-between">
                               <Text size="sm" fw={600}>Heap Allocated</Text>
                               <Badge variant="light" color="teal" radius="xl">{perfData?.memoryUsage ? `${(Number(perfData.memoryUsage) / 1024 / 1024).toFixed(2)} MB` : 'N/A'}</Badge>
                            </Group>
                         </Stack>
                      </Paper>
                   </Grid.Col>
                   <Grid.Col span={{ base: 12, md: 6 }}>
                      <Paper p="xl" radius="md" withBorder h="100%">
                         <Title order={4} mb="lg" fw={700}>System Health</Title>
                         <Group grow>
                            <Stack gap={4} align="center">
                               <ThemeIcon size="xl" radius="xl" color="green" variant="light">
                                  <IconServer size={24} />
                               </ThemeIcon>
                               <Text size="xs" ta="center" fw={700}>API Gateway</Text>
                               <Text size="xs" ta="center" c="green" fw={700}>OPERATIONAL</Text>
                            </Stack>
                            <Stack gap={4} align="center">
                               <ThemeIcon size="xl" radius="xl" color="green" variant="light">
                                  <IconDatabase size={24} />
                               </ThemeIcon>
                               <Text size="xs" ta="center" fw={700}>Database</Text>
                               <Text size="xs" ta="center" c="green" fw={700}>CONNECTED</Text>
                            </Stack>
                            <Stack gap={4} align="center">
                               <ThemeIcon size="xl" radius="xl" color="green" variant="light">
                                  <IconActivity size={24} />
                               </ThemeIcon>
                               <Text size="xs" ta="center" fw={700}>Outbox Worker</Text>
                               <Text size="xs" ta="center" c="green" fw={700}>ACTIVE</Text>
                            </Stack>
                         </Group>
                      </Paper>
                   </Grid.Col>
                </Grid>
             </Stack>
          </Tabs.Panel>
        </Tabs>
      </Stack>
    </Container>
  );
};
