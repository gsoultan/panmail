import React from 'react';
import { Container, Stack, Tabs, Text, Title } from '@mantine/core';
import { IconFilter, IconShieldCheck } from '@tabler/icons-react';
import { ReviewQueue } from '../features/filters/components/ReviewQueue';
import { RuleList } from '../features/filters/components/RuleList';

export const FiltersPage: React.FC = () => (
  <Container size="xl" py="md">
    <Stack gap="lg">
      <Stack gap={2}>
        <Title order={3}>Filtering</Title>
        <Text size="sm" c="dimmed">
          Rules decide what happens to mail on the way in and on the way out. Anything held
          waits here until somebody releases or rejects it.
        </Text>
      </Stack>

      {/* The queue leads, because it is the tab with work waiting in it. */}
      <Tabs defaultValue="review" radius="md">
        <Tabs.List mb="md">
          <Tabs.Tab value="review" leftSection={<IconFilter size={16} />}>Review queue</Tabs.Tab>
          <Tabs.Tab value="rules" leftSection={<IconShieldCheck size={16} />}>Rules</Tabs.Tab>
        </Tabs.List>

        <Tabs.Panel value="review"><ReviewQueue /></Tabs.Panel>
        <Tabs.Panel value="rules"><RuleList /></Tabs.Panel>
      </Tabs>
    </Stack>
  </Container>
);
