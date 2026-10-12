import React from 'react';
import { ActionIcon, Box, Code, Tooltip, rem } from '@mantine/core';
import { CopyButton } from '../../../lib/CopyButton';
import { IconCheck, IconCopy } from '@tabler/icons-react';

// A read-only JSON body with a copy button, for sample and sent payloads.
export const JsonBlock: React.FC<{ value: string }> = ({ value }) => (
  <Box pos="relative">
    <Code block style={{ whiteSpace: 'pre', fontSize: rem(12), maxHeight: rem(360), overflow: 'auto' }}>
      {value}
    </Code>
    <CopyButton value={value}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? 'Copied' : 'Copy'}>
          <ActionIcon
            variant="subtle"
            color={copied ? 'teal' : 'gray'}
            onClick={copy}
            aria-label="Copy JSON"
            style={{ position: 'absolute', top: rem(6), right: rem(6) }}
          >
            {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          </ActionIcon>
        </Tooltip>
      )}
    </CopyButton>
  </Box>
);
