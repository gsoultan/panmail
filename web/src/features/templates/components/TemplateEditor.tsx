import React, { useRef, useImperativeHandle, forwardRef } from 'react';
import { Box, rem } from '@mantine/core';
import { CustomEmailBuilder, CustomEmailBuilderHandle } from './builder/CustomEmailBuilder';

interface TemplateEditorProps {
  initialDesign?: string;
  onReady?: () => void;
  minHeight?: string | number;
  /** Fires on every design change, for autosave or dirty tracking. */
  onChange?: (design: any) => void;
}

export interface TemplateEditorHandle {
  exportHtml: () => Promise<{ design: any; html: string }>;
}

export const TemplateEditor = forwardRef<TemplateEditorHandle, TemplateEditorProps>(({
  initialDesign,
  onReady,
  minHeight = '80vh',
  onChange
}, ref) => {
  const builderRef = useRef<CustomEmailBuilderHandle>(null);

  useImperativeHandle(ref, () => ({
    exportHtml: async () => {
      if (builderRef.current) {
        const { design, html } = builderRef.current.exportHtml();
        return { design: JSON.stringify(design), html };
      }
      return { design: '', html: '' };
    }
  }));

  return (
    <Box style={{ height: minHeight, position: 'relative' }}>
      <CustomEmailBuilder
        ref={builderRef}
        initialDesign={initialDesign}
        onChange={onChange}
      />
    </Box>
  );
});

TemplateEditor.displayName = 'TemplateEditor';
