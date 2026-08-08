import React, { useState, useImperativeHandle, forwardRef, useEffect, useRef, useMemo, useCallback } from 'react';
import {
  Box,
  Paper,
  Stack,
  Text,
  ActionIcon,
  Button,
  ScrollArea,
  Group,
  rem,
  Divider,
  Center,
  Tooltip,
  SimpleGrid,
  ColorInput,
  Select,
  Table,
  Modal,
  Code,
  Menu,
  TextInput,
  Drawer,
  SegmentedControl
} from '@mantine/core';
import { useDisclosure, useMediaQuery, useHotkeys } from '@mantine/hooks';
import {
  IconTypography,
  IconHandClick,
  IconPhoto,
  IconSeparator,
  IconSpace,
  IconTrash,
  IconCopy,
  IconGripVertical,
  IconPlus,
  IconDeviceMobile,
  IconDeviceDesktop,
  IconHeading,
  IconList,
  IconShare,
  IconVideo,
  IconTable,
  IconColumns,
  IconEye,
  IconChevronUp,
  IconChevronDown,
  IconCode,
  IconDeviceTablet,
  IconArrowBackUp,
  IconArrowForwardUp,
  IconPencil,
  IconAdjustments,
  IconLayoutGrid
} from '@tabler/icons-react';
import {
  DndContext,
  closestCenter,
  PointerSensor,
  TouchSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core';
import {
  SortableContext,
  sortableKeyboardCoordinates,
  verticalListSortingStrategy,
  useSortable,
} from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Block, BlockType, EmailDesign } from './types';
import { generateHTML, VIEWPORT_WIDTHS, type Viewport } from './htmlGenerator';
import { PropertyEditor } from './PropertyEditor';
import { sanitizeHtml } from './sanitize';
import { useDesignHistory } from './useDesignHistory';
import { reorderTopLevel, reorderWithinColumn, findContainer, sameContainer } from './reorder';

export interface CustomEmailBuilderHandle {
  exportHtml: () => { design: EmailDesign; html: string };
}

interface CustomEmailBuilderProps {
  initialDesign?: string;
  /**
   * Fired whenever the design changes, so the parent can autosave or track a
   * dirty state. Previously the only way out was the imperative `exportHtml`
   * ref, which meant a closed tab lost everything.
   */
  onChange?: (design: EmailDesign) => void;
}

const parseDesign = (raw: string | undefined): EmailDesign | null => {
  if (!raw) return null;
  try {
    const parsed = JSON.parse(raw);
    // A design missing its block list would crash the canvas on first render.
    if (!parsed || !Array.isArray(parsed.blocks)) return null;
    return { bodyStyle: {}, ...parsed } as EmailDesign;
  } catch (e) {
    console.error('Failed to parse initial design', e);
    return null;
  }
};

const DEFAULT_DESIGN: EmailDesign = {
  blocks: [
    {
      id: '1',
      type: 'text',
      content: { text: '<h1>Welcome to Panmail</h1><p>Start building your professional email here.</p>' },
      style: { textAlign: 'center' as any, color: '#333333' }
    }
  ],
  bodyStyle: {
    backgroundColor: '#f8f9fa',
    fontFamily: 'Inter, sans-serif',
    color: '#333333'
  }
};

const BLOCK_TYPES: { type: BlockType; label: string; icon: any }[] = [
  { type: 'heading', label: 'Heading', icon: IconHeading },
  { type: 'text', label: 'Text', icon: IconTypography },
  { type: 'button', label: 'Button', icon: IconHandClick },
  { type: 'image', label: 'Image', icon: IconPhoto },
  { type: 'divider', label: 'Divider', icon: IconSeparator },
  { type: 'spacer', label: 'Spacer', icon: IconSpace },
  { type: 'list', label: 'List', icon: IconList },
  { type: 'social', label: 'Social', icon: IconShare },
  { type: 'video', label: 'Video', icon: IconVideo },
  { type: 'table', label: 'Table', icon: IconTable },
  { type: 'columns', label: 'Columns', icon: IconColumns },
];

// Block ids key React lists and address blocks for edit and delete, so a
// collision silently edits the wrong block. crypto.randomUUID is available in
// every browser this app targets; the fallback keeps a non-secure context
// (plain http on a LAN, which is how the dev gateway is often reached) working.
const newBlockId = (): string =>
  typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `b-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 11)}`;

const BlockItem = ({ bt, onClick }: { bt: any; onClick: () => void }) => (
  <Paper
    withBorder
    p="xs"
    style={{
      cursor: 'pointer',
      textAlign: 'center',
      backgroundColor: 'light-dark(var(--mantine-color-white), var(--mantine-color-dark-6))',
      transition: 'transform 0.1s ease',
    }}
    onClick={onClick}
    component="div"
    onMouseEnter={(e: any) => e.currentTarget.style.transform = 'scale(1.02)'}
    onMouseLeave={(e: any) => e.currentTarget.style.transform = 'scale(1)'}
  >
    <bt.icon size={24} style={{ marginBottom: rem(4), color: 'var(--mantine-color-brand-6)' }} />
    <Text size="xs" fw={600}>{bt.label}</Text>
  </Paper>
);

const AddBlockMenu = ({ onAdd, label = "Add Block" }: { onAdd: (type: BlockType) => void; label?: string }) => (
  <Menu shadow="md" width={250} position="bottom">
    <Menu.Target>
      <Divider 
        label={
          <Button 
            variant="subtle" 
            size="compact-xs" 
            color="gray" 
            leftSection={<IconPlus size={10} />}
            styles={{ root: { backgroundColor: 'var(--mantine-color-white)', border: '1px solid var(--mantine-color-gray-3)' }}}
          >
            {label}
          </Button>
        } 
        labelPosition="center" 
        my="xs" 
        style={{ opacity: 0, transition: 'opacity 0.2s' }}
        onMouseEnter={(e: any) => e.currentTarget.style.opacity = '1'}
        onMouseLeave={(e: any) => e.currentTarget.style.opacity = '0'}
      />
    </Menu.Target>

    <Menu.Dropdown>
      <Menu.Label>Basic</Menu.Label>
      {BLOCK_TYPES.filter(bt => ['heading', 'text', 'button', 'divider', 'spacer'].includes(bt.type)).map((bt) => (
        <Menu.Item key={bt.type} leftSection={<bt.icon size={14} />} onClick={() => onAdd(bt.type)}>{bt.label}</Menu.Item>
      ))}
      <Menu.Divider />
      <Menu.Label>Layout & Media</Menu.Label>
      {BLOCK_TYPES.filter(bt => ['image', 'video', 'columns', 'table'].includes(bt.type)).map((bt) => (
        <Menu.Item key={bt.type} leftSection={<bt.icon size={14} />} onClick={() => onAdd(bt.type)}>{bt.label}</Menu.Item>
      ))}
    </Menu.Dropdown>
  </Menu>
);

export const CustomEmailBuilder = forwardRef<CustomEmailBuilderHandle, CustomEmailBuilderProps>(({ initialDesign, onChange }, ref) => {
  const { design, setDesign, reset, undo, redo, canUndo, canRedo } = useDesignHistory(
    parseDesign(initialDesign) ?? DEFAULT_DESIGN,
  );

  const [selectedBlockId, setSelectedBlockId] = useState<string | null>(null);
  const [previewMode, setPreviewMode] = useState<Viewport>('desktop');
  const [mode, setMode] = useState<'edit' | 'preview'>('edit');
  const [codeOpened, { open: openCode, close: closeCode }] = useDisclosure(false);

  // Below these widths the three-pane layout does not fit, so the side panels
  // become drawers rather than being squeezed into unusable slivers.
  const wideEnoughForBlocks = useMediaQuery('(min-width: 62em)');   // ~992px
  const wideEnoughForProps = useMediaQuery('(min-width: 75em)');    // ~1200px
  const [blocksDrawer, { open: openBlocks, close: closeBlocks }] = useDisclosure(false);
  const [propsDrawer, { open: openProps, close: closeProps }] = useDisclosure(false);

  // The design arrives from a form that may still be loading when the builder
  // mounts. Reading it once in a state initialiser silently ignored anything
  // that landed later, so opening a template could show an empty canvas.
  const appliedDesign = useRef(initialDesign);
  useEffect(() => {
    if (initialDesign === appliedDesign.current) return;
    appliedDesign.current = initialDesign;
    const parsed = parseDesign(initialDesign);
    if (parsed) {
      reset(parsed);
      setSelectedBlockId(null);
    }
  }, [initialDesign, reset]);

  // Skipped on mount so simply opening a template does not mark the form dirty.
  const notifiedOnce = useRef(false);
  useEffect(() => {
    if (!notifiedOnce.current) {
      notifiedOnce.current = true;
      return;
    }
    onChange?.(design);
  }, [design, onChange]);

  useHotkeys([
    ['mod+Z', () => undo()],
    ['mod+shift+Z', () => redo()],
    ['mod+Y', () => redo()],
  ]);

  useImperativeHandle(ref, () => ({
    exportHtml: () => {
      const html = generateHTML(design);
      return { design, html };
    }
  }));

  // Regenerating on every keystroke would re-parse a whole document into the
  // preview iframe; only do it when the preview is actually on screen.
  const previewHtml = useMemo(
    () => (mode === 'preview' || codeOpened ? generateHTML(design) : ''),
    [design, mode, codeOpened],
  );

  const selectBlock = useCallback(
    (id: string | null) => {
      setSelectedBlockId(id);
      // On a narrow screen the property panel is a drawer, so selecting a block
      // has to open it or the selection appears to do nothing.
      if (id && !wideEnoughForProps) openProps();
    },
    [wideEnoughForProps, openProps],
  );

  // Touch and keyboard alongside pointer, because the builder is used on a
  // tablet and because a reorder that only works with a mouse is not usable by
  // anyone navigating with a keyboard.
  //
  // The pointer sensor needs a small activation distance or a click that moves
  // a pixel is read as a drag, and selecting a block stops working.
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(TouchSensor, { activationConstraint: { delay: 200, tolerance: 6 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  );

  // One handler for both levels. Which list a drag belongs to is derived from
  // the design rather than from where the handler was registered, so nested
  // DndContexts — which would compete for the same pointer events — are not
  // needed.
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const { active, over } = event;
      if (!over || active.id === over.id) return;

      setDesign((prev) => {
        const from = findContainer(prev.blocks, String(active.id));
        const to = findContainer(prev.blocks, String(over.id));

        // Dragging between two different lists is refused rather than guessed
        // at: the indices are measured against different arrays, so acting on
        // them would reorder the wrong one.
        if (!sameContainer(from, to) || !from) return prev;

        if (from.kind === 'root') {
          return { ...prev, blocks: reorderTopLevel(prev.blocks, String(active.id), String(over.id)) };
        }
        return {
          ...prev,
          blocks: reorderWithinColumn(
            prev.blocks,
            from.columnsBlockId,
            from.columnId,
            String(active.id),
            String(over.id),
          ),
        };
      });
    },
    [setDesign],
  );

  const addBlock = (type: BlockType, index?: number) => {
    const newBlock: Block = {
      id: newBlockId(),
      type,
      content: getDefaults(type).content,
      style: getDefaults(type).style,
    };

    setDesign(prev => {
      const newBlocks = [...prev.blocks];
      if (typeof index === 'number') {
        newBlocks.splice(index, 0, newBlock);
      } else {
        newBlocks.push(newBlock);
      }
      return { ...prev, blocks: newBlocks };
    });

    setSelectedBlockId(newBlock.id);
  };

  const deleteBlock = (id: string) => {
    // Rebuilt rather than edited in place. The previous version assigned to
    // `b.content.columns` on a block belonging to the current state, so the
    // delete was applied to the old design object as well as the new one —
    // enough to make a re-render show a block that had already gone, and
    // enough to defeat any future undo stack.
    const deleteRecursive = (blocks: Block[]): Block[] =>
      blocks
        .filter((b) => b.id !== id)
        .map((b) =>
          b.type === 'columns' && b.content.columns
            ? {
                ...b,
                content: {
                  ...b.content,
                  columns: b.content.columns.map((col: any) => ({
                    ...col,
                    blocks: deleteRecursive(col.blocks || []),
                  })),
                },
              }
            : b,
        );

    setDesign(prev => ({ ...prev, blocks: deleteRecursive(prev.blocks) }));
    if (selectedBlockId === id) setSelectedBlockId(null);
  };

  const duplicateBlock = (block: Block) => {
    const reIDBlock = (b: Block): Block => {
      const newId = newBlockId();
      if (b.type === 'columns' && b.content.columns) {
        return {
          ...b,
          id: newId,
          content: {
            ...b.content,
            columns: b.content.columns.map((col: any) => ({
              ...col,
              id: newBlockId(),
              blocks: col.blocks.map(reIDBlock)
            }))
          }
        };
      }
      return { ...b, id: newId };
    };

    const newBlock = reIDBlock(block);
    
    const duplicateRecursive = (blocks: Block[]): Block[] => {
      const index = blocks.findIndex(b => b.id === block.id);
      if (index !== -1) {
        const newBlocks = [...blocks];
        newBlocks.splice(index + 1, 0, newBlock);
        return newBlocks;
      }
      
      return blocks.map(b => {
        if (b.type === 'columns' && b.content.columns) {
          return {
            ...b,
            content: {
              ...b.content,
              columns: b.content.columns.map((col: any) => ({
                ...col,
                blocks: duplicateRecursive(col.blocks)
              }))
            }
          };
        }
        return b;
      });
    };
    
    setDesign(prev => ({ ...prev, blocks: duplicateRecursive(prev.blocks) }));
    setSelectedBlockId(newBlock.id);
  };

  const moveBlock = (index: number, direction: 'up' | 'down') => {
    const newBlocks = [...design.blocks];
    const newIndex = direction === 'up' ? index - 1 : index + 1;
    if (newIndex < 0 || newIndex >= newBlocks.length) return;
    [newBlocks[index], newBlocks[newIndex]] = [newBlocks[newIndex], newBlocks[index]];
    setDesign(prev => ({ ...prev, blocks: newBlocks }));
  };

  const updateBlock = (id: string, updates: Partial<Block>) => {
    const updateRecursive = (blocks: Block[]): Block[] => {
      return blocks.map(b => {
        if (b.id === id) return { ...b, ...updates };
        if (b.type === 'columns' && b.content.columns) {
          return {
            ...b,
            content: {
              ...b.content,
              columns: b.content.columns.map((col: any) => ({
                ...col,
                blocks: updateRecursive(col.blocks)
              }))
            }
          };
        }
        return b;
      });
    };
    // Coalesced: a property edit is usually a keystroke, and one undo step per
    // character makes Ctrl-Z useless.
    setDesign(prev => ({ ...prev, blocks: updateRecursive(prev.blocks) }), { coalesce: true });
  };

  const findBlockRecursive = (blocks: Block[], id: string | null): Block | undefined => {
    if (!id) return undefined;
    for (const b of blocks) {
      if (b.id === id) return b;
      if (b.type === 'columns' && b.content.columns) {
        for (const col of b.content.columns) {
          const found = findBlockRecursive(col.blocks, id);
          if (found) return found;
        }
      }
    }
    return undefined;
  };

  const selectedBlock = findBlockRecursive(design.blocks, selectedBlockId);

  // Both panels are defined once and mounted either inline or inside a drawer,
  // so the narrow layout cannot drift away from the wide one.
  const PALETTE_GROUPS: { title: string; types: BlockType[] }[] = [
    { title: 'Basic', types: ['heading', 'text', 'button', 'divider', 'spacer'] },
    { title: 'Media', types: ['image', 'video'] },
    { title: 'Layout', types: ['columns', 'table'] },
    { title: 'Social', types: ['social', 'list'] },
  ];

  const renderBlockPalette = (onAdd: (type: BlockType) => void) => (
    <Stack gap="lg">
      {PALETTE_GROUPS.map((group) => (
        <Box key={group.title}>
          <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="xs">{group.title}</Text>
          <SimpleGrid cols={2} spacing="xs">
            {BLOCK_TYPES.filter((bt) => group.types.includes(bt.type)).map((bt) => (
              <BlockItem key={bt.type} bt={bt} onClick={() => onAdd(bt.type)} />
            ))}
          </SimpleGrid>
        </Box>
      ))}
    </Stack>
  );

  const renderPropertiesPanel = () =>
    selectedBlock ? (
      <PropertyEditor
        block={selectedBlock}
        onChange={(updates) => updateBlock(selectedBlock.id, updates)}
      />
    ) : (
      <Stack gap="md">
        <Text fw={700} size="sm">BODY SETTINGS</Text>
        <Divider />
        <TextInput
          label="Email Pre-header"
          description="Hidden text that appears after the subject line in many email clients"
          placeholder="e.g. Check out our latest updates!"
          value={design.preheader || ''}
          onChange={(e) => {
            const preheader = e.currentTarget.value;
            setDesign(prev => ({ ...prev, preheader }), { coalesce: true });
          }}
          mb="xs"
        />
        <ColorInput
          label="Background Color"
          value={design.bodyStyle.backgroundColor}
          onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, backgroundColor: val } }), { coalesce: true })}
        />
        <ColorInput
          label="Default Text Color"
          value={design.bodyStyle.color || '#333333'}
          onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, color: val } }), { coalesce: true })}
        />
        <Select
          label="Font Family"
          data={[
            { label: 'Sans Serif (Inter)', value: 'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif' },
            { label: 'Serif (Georgia)', value: 'Georgia, Times, "Times New Roman", serif' },
            { label: 'Monospace (Monaco)', value: 'Monaco, Consolas, "Courier New", monospace' },
            { label: 'Open Sans', value: '"Open Sans", sans-serif' },
            { label: 'Lato', value: '"Lato", sans-serif' },
            { label: 'Roboto', value: '"Roboto", sans-serif' },
            { label: 'Helvetica', value: 'Helvetica, Arial, sans-serif' },
            { label: 'Verdana', value: 'Verdana, Geneva, sans-serif' },
          ]}
          value={design.bodyStyle.fontFamily}
          onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, fontFamily: val || 'Inter, sans-serif' } }))}
        />
        <Select
          label="Content Width"
          description="Maximum width on desktop. Narrower screens always use the full width."
          data={[
            { label: '500px', value: '500px' },
            { label: '600px (Recommended)', value: '600px' },
            { label: '700px', value: '700px' },
            { label: '800px', value: '800px' },
          ]}
          value={design.bodyStyle.contentWidth || '600px'}
          onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, contentWidth: val || '600px' } }))}
        />
      </Stack>
    );

  return (
    <Box h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
      <Group justify="space-between" p="xs" wrap="wrap" gap="xs" style={{
        backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))',
        borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))'
      }}>
        <Group gap="xs" wrap="nowrap">
          {!wideEnoughForBlocks && (
            <Tooltip label="Blocks">
              <ActionIcon variant="light" color="brand" onClick={openBlocks} aria-label="Open blocks panel">
                <IconLayoutGrid size={18} />
              </ActionIcon>
            </Tooltip>
          )}
          <Tooltip label="Undo (Ctrl+Z)">
            <ActionIcon variant="light" color="brand" onClick={undo} disabled={!canUndo} aria-label="Undo">
              <IconArrowBackUp size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Redo (Ctrl+Shift+Z)">
            <ActionIcon variant="light" color="brand" onClick={redo} disabled={!canRedo} aria-label="Redo">
              <IconArrowForwardUp size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>

        <Group gap="xs" wrap="nowrap">
          {/* Widths match the media queries the generated email carries, so
              what these show is what the breakpoints actually do. */}
          <Tooltip label={`Mobile (${VIEWPORT_WIDTHS.mobile}px)`}>
            <ActionIcon
              variant={previewMode === 'mobile' ? 'filled' : 'light'}
              onClick={() => setPreviewMode('mobile')}
              color="brand"
              aria-label="Mobile viewport"
            >
              <IconDeviceMobile size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label={`Tablet (${VIEWPORT_WIDTHS.tablet}px)`}>
            <ActionIcon
              variant={previewMode === 'tablet' ? 'filled' : 'light'}
              onClick={() => setPreviewMode('tablet')}
              color="brand"
              aria-label="Tablet viewport"
            >
              <IconDeviceTablet size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Desktop">
            <ActionIcon
              variant={previewMode === 'desktop' ? 'filled' : 'light'}
              onClick={() => setPreviewMode('desktop')}
              color="brand"
              aria-label="Desktop viewport"
            >
              <IconDeviceDesktop size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>

        <Group gap="xs" wrap="nowrap">
          {/* Edit renders React components that only approximate the output.
              Preview renders the real generated HTML, which is the only view
              that can be trusted to match what a recipient sees. */}
          <SegmentedControl
            size="xs"
            value={mode}
            onChange={(v) => setMode(v as 'edit' | 'preview')}
            data={[
              { value: 'edit', label: (<Center style={{ gap: 6 }}><IconPencil size={14} /><Box visibleFrom="sm">Edit</Box></Center>) as any },
              { value: 'preview', label: (<Center style={{ gap: 6 }}><IconEye size={14} /><Box visibleFrom="sm">Preview</Box></Center>) as any },
            ]}
          />
          <Tooltip label="View HTML source">
            <ActionIcon variant="light" onClick={openCode} color="brand" aria-label="View HTML source">
              <IconCode size={18} />
            </ActionIcon>
          </Tooltip>
          {!wideEnoughForProps && (
            <Tooltip label="Properties">
              <ActionIcon variant="light" color="brand" onClick={openProps} aria-label="Open properties panel">
                <IconAdjustments size={18} />
              </ActionIcon>
            </Tooltip>
          )}
        </Group>
      </Group>

      <Modal opened={codeOpened} onClose={closeCode} title="HTML Preview" size="xl">
        <Stack>
          <Text size="sm">This is the generated HTML that will be sent to recipients.</Text>
          <ScrollArea h={500} offsetScrollbars>
            <Code block style={{ whiteSpace: 'pre-wrap' }}>{previewHtml}</Code>
          </ScrollArea>
          <Group justify="flex-end">
            <Button onClick={closeCode}>Close</Button>
          </Group>
        </Stack>
      </Modal>

      {/* Panels become drawers rather than shrinking, because a 250px palette
          and a 350px property sheet plus a canvas cannot coexist below ~1200px
          — the builder was simply unusable on a tablet or a phone. */}
      <Drawer opened={blocksDrawer && !wideEnoughForBlocks} onClose={closeBlocks} title="Blocks" size="xs" position="left">
        {renderBlockPalette((type) => { addBlock(type); closeBlocks(); })}
      </Drawer>
      <Drawer opened={propsDrawer && !wideEnoughForProps} onClose={closeProps} title={selectedBlock ? 'Block properties' : 'Body settings'} size="sm" position="right">
        {renderPropertiesPanel()}
      </Drawer>

      <Box style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Inline only when there is room; otherwise it lives in the drawer. */}
        {wideEnoughForBlocks && (
          <Box style={{ width: 250, flexShrink: 0, borderRight: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
            <ScrollArea h="100%">
              <Paper p="md" h="100%" radius={0} bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))">
                {renderBlockPalette((type) => addBlock(type))}
              </Paper>
            </ScrollArea>
          </Box>
        )}

        {/* Main Canvas */}
        <Box style={{ flex: 1, minWidth: 0, backgroundColor: 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-8))', overflow: 'hidden' }}>
          {mode === 'preview' ? (
            /*
             * The real generated HTML, in a fully sandboxed iframe.
             *
             * This is the only view that can be trusted: the edit canvas below
             * renders Mantine components that merely resemble the output, so a
             * table-layout or media-query problem is invisible there.
             *
             * `sandbox=""` grants nothing — no scripts, no forms, no same-origin
             * access — so authored template HTML cannot reach this session even
             * though it is being rendered as markup. The width is set on the
             * frame itself, which is what makes the email's own media queries
             * fire exactly as they will in a client of that size.
             */
            <Box h="100%" style={{ overflow: 'auto', padding: rem(24) }}>
              <Center>
                <Box
                  style={{
                    width: previewMode === 'desktop' ? '100%' : VIEWPORT_WIDTHS[previewMode],
                    maxWidth: '100%',
                    height: '80vh',
                    boxShadow: '0 20px 50px rgba(0,0,0,0.15)',
                    borderRadius: rem(12),
                    overflow: 'hidden',
                    border: '1px solid var(--mantine-color-gray-3)',
                    backgroundColor: '#ffffff',
                    transition: 'width 0.25s ease',
                  }}
                >
                  <iframe
                    title="Email preview"
                    sandbox=""
                    srcDoc={previewHtml}
                    style={{ width: '100%', height: '100%', border: 0, display: 'block' }}
                  />
                </Box>
              </Center>
            </Box>
          ) : (
          <ScrollArea h="100%" p="xl">
            <Center>
              <Box
                w={
                  previewMode === 'desktop'
                    ? parseInt(design.bodyStyle.contentWidth || '600', 10) + 80
                    : VIEWPORT_WIDTHS[previewMode]
                }
                maw="100%"
                style={{
                  transition: 'all 0.3s ease',
                  backgroundColor: design.bodyStyle.backgroundColor || '#f8f9fa',
                  boxShadow: '0 20px 50px rgba(0,0,0,0.15)',
                  borderRadius: previewMode === 'mobile' ? rem(40) : rem(12),
                  border: previewMode === 'mobile' ? '12px solid #1a1a1a' : '1px solid var(--mantine-color-gray-3)',
                  minHeight: '85vh',
                  position: 'relative',
                  overflow: 'hidden',
                  display: 'flex',
                  flexDirection: 'column'
                }}
              >
                <Box style={{ 
                  height: previewMode === 'mobile' ? 40 : 34, 
                  backgroundColor: previewMode === 'mobile' ? '#1a1a1a' : 'var(--mantine-color-gray-1)',
                  borderBottom: previewMode === 'mobile' ? 'none' : '1px solid var(--mantine-color-gray-3)',
                  display: 'flex',
                  alignItems: 'center',
                  padding: '0 15px',
                  gap: 5,
                  zIndex: 10,
                  position: 'relative'
                }}>
                  {previewMode === 'desktop' ? (
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
                        Email Preview
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

                <Box style={{
                  flex: 1,
                  overflowY: 'auto',
                  paddingTop: previewMode === 'mobile' ? 30 : 40,
                  paddingBottom: previewMode === 'mobile' ? 20 : 40,
                  paddingLeft: previewMode === 'mobile' ? 10 : 0,
                  paddingRight: previewMode === 'mobile' ? 10 : 0,
                  WebkitTextSizeAdjust: 'none',
                  ...design.bodyStyle,
                }}>
                  <Box style={{
                    maxWidth: design.bodyStyle.contentWidth || '600px',
                    margin: '0 auto',
                    backgroundColor: '#ffffff',
                    minHeight: '100%',
                    boxShadow: previewMode === 'desktop' ? '0 0 20px rgba(0,0,0,0.05)' : 'none',
                    position: 'relative',
                    transition: 'width 0.3s ease'
                  }}>
                    <DndContext
                      sensors={sensors}
                      collisionDetection={closestCenter}
                      onDragEnd={handleDragEnd}
                    >
                    <SortableContext items={design.blocks.map(b => b.id)} strategy={verticalListSortingStrategy}>
                    <Stack gap={0}>
                      {design.blocks.map((block, index) => (
                    <React.Fragment key={block.id}>
                      {index === 0 && <AddBlockMenu onAdd={(type) => addBlock(type, 0)} label="Insert Block" />}
                      <RenderBlockWrapper
                        block={block}
                        index={index}
                        totalBlocks={design.blocks.length}
                        selectedBlockId={selectedBlockId}
                        setSelectedBlockId={selectBlock}
                        duplicateBlock={duplicateBlock}
                        deleteBlock={deleteBlock}
                        updateBlock={updateBlock}
                        moveBlock={moveBlock}
                        previewMode={previewMode}
                      />
                      <AddBlockMenu onAdd={(type) => addBlock(type, index + 1)} label="Insert Block" />
                    </React.Fragment>
                  ))}

                  {design.blocks.length === 0 && (
                    <Center h={200} style={{ border: '2px dashed var(--mantine-color-gray-4)', borderRadius: rem(8) }}>
                      <Stack align="center" gap="xs">
                        <IconPlus size={32} color="var(--mantine-color-gray-5)" />
                        <Text c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Click on a block to start</Text>
                      </Stack>
                    </Center>
                  )}
                </Stack>
                    </SortableContext>
                    </DndContext>
              </Box>

              {previewMode === 'mobile' && (
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
        </Center>
          </ScrollArea>
          )}
        </Box>

        {/* Inline only when there is room; otherwise it lives in the drawer. */}
        {wideEnoughForProps && (
          <Box style={{ width: 350, flexShrink: 0, borderLeft: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
            <ScrollArea h="100%">
              <Paper p="md" h="100%" radius={0} bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))">
                {renderPropertiesPanel()}
              </Paper>
            </ScrollArea>
          </Box>
        )}
      </Box>
    </Box>
  );
});

interface RenderBlockWrapperProps {
  block: Block;
  index: number;
  totalBlocks: number;
  selectedBlockId: string | null;
  setSelectedBlockId: (id: string | null) => void;
  duplicateBlock: (block: Block) => void;
  deleteBlock: (id: string) => void;
  updateBlock: (id: string, updates: Partial<Block>) => void;
  moveBlock: (index: number, direction: 'up' | 'down') => void;
  nested?: boolean;
  previewMode: Viewport;
}

const RenderBlockWrapper = ({
  block,
  index,
  totalBlocks,
  selectedBlockId,
  setSelectedBlockId,
  duplicateBlock,
  deleteBlock,
  updateBlock,
  moveBlock,
  nested = false,
  previewMode
}: RenderBlockWrapperProps) => {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({
    id: block.id,
  });

  return (
    <Box
      ref={setNodeRef}
      style={{
        transform: CSS.Translate.toString(transform),
        // Lifted above its neighbours while dragging, and faded so the gap it
        // will land in stays readable.
        zIndex: isDragging ? 20 : undefined,
        opacity: isDragging ? 0.4 : 1,
        position: 'relative',
        padding: rem(8),
        border: selectedBlockId === block.id ? '2px solid var(--mantine-color-brand-6)' : (nested ? '1px dashed light-dark(var(--mantine-color-gray-3), var(--mantine-color-dark-4))' : '1px dashed transparent'),
        borderRadius: rem(4),
        cursor: 'pointer',
        transition: transition || 'all 0.2s',
        marginBottom: rem(selectedBlockId === block.id ? 8 : 4)
      }}
      onClick={(e) => {
        e.stopPropagation();
        setSelectedBlockId(block.id);
      }}
    >
      {/*
        A dedicated handle rather than making the whole block draggable: the
        block body has to stay clickable to select it, and on a touch screen a
        drag started anywhere would fight with scrolling the canvas.
      */}
      <ActionIcon
        {...attributes}
        {...listeners}
        size="sm"
        variant="subtle"
        color="gray"
        aria-label={`Reorder ${block.type} block`}
        onClick={(e) => e.stopPropagation()}
        style={{
          position: 'absolute',
          top: 2,
          left: -6,
          zIndex: 11,
          cursor: isDragging ? 'grabbing' : 'grab',
          touchAction: 'none',
          opacity: selectedBlockId === block.id ? 1 : 0.35,
        }}
      >
        <IconGripVertical size={14} />
      </ActionIcon>

      {/* Block Controls */}
      {selectedBlockId === block.id && (
        <Group
          gap={4}
          style={{
            position: 'absolute',
            top: -15,
            right: 10,
            zIndex: 10,
            backgroundColor: 'var(--mantine-color-brand-6)',
            padding: '2px 4px',
            borderRadius: '4px'
          }}
        >
          {!nested && (
            <>
              <Tooltip label="Move Up" position="top">
                <ActionIcon
                  size="xs"
                  variant="transparent"
                  color="white"
                  disabled={index === 0}
                  onClick={(e) => { e.stopPropagation(); moveBlock(index, 'up'); }}
                >
                  <IconChevronUp size={14} />
                </ActionIcon>
              </Tooltip>
              <Tooltip label="Move Down" position="top">
                <ActionIcon
                  size="xs"
                  variant="transparent"
                  color="white"
                  disabled={index === totalBlocks - 1}
                  onClick={(e) => { e.stopPropagation(); moveBlock(index, 'down'); }}
                >
                  <IconChevronDown size={14} />
                </ActionIcon>
              </Tooltip>
            </>
          )}
          <ActionIcon size="xs" variant="transparent" color="white" onClick={(e) => { e.stopPropagation(); duplicateBlock(block); }}>
            <IconCopy size={12} />
          </ActionIcon>
          <ActionIcon size="xs" variant="transparent" color="white" onClick={(e) => { e.stopPropagation(); deleteBlock(block.id); }}>
            <IconTrash size={12} />
          </ActionIcon>
        </Group>
      )}

      <Box style={block.style}>
        {renderBlock(block, {
            index,
            totalBlocks,
            selectedBlockId,
            setSelectedBlockId,
            duplicateBlock,
            deleteBlock,
            updateBlock,
            moveBlock,
            previewMode
        })}
      </Box>
    </Box>
  );
};

const renderBlock = (block: Block, props: Omit<RenderBlockWrapperProps, 'block'>) => {
  const styles = {
    ...block.style,
    // Ensure padding/margin are properly applied to the container if they exist
    // Box in RenderBlockWrapper already has block.style, but switch cases might need refinement
  };
  
  switch (block.type) {
    case 'heading':
      const HeadingTag = (block.content.level || 'h1') as any;
      return <HeadingTag style={styles}>{block.content.text}</HeadingTag>;
    case 'text':
      // Sanitised, not trusted. Templates are tenant-scoped and an Editor can
      // author them, so raw markup here would run in the session of whichever
      // Administrator opens the template next. Merge tags are plain text and
      // pass through untouched.
      return <div style={styles} dangerouslySetInnerHTML={{ __html: sanitizeHtml(block.content.text) }} />;
    case 'button':
      const alignment = styles.textAlign || 'center';
      const justify = alignment === 'left' ? 'flex-start' : (alignment === 'right' ? 'flex-end' : 'center');
      return (
        <Group justify={justify as any}>
          <Button 
            variant="filled" 
            style={{ 
              ...styles,
              backgroundColor: styles.backgroundColor || '#0073ea', 
              color: styles.color || '#ffffff',
              borderRadius: styles.borderRadius || rem(4),
              width: styles.width || 'auto',
              border: styles.borderStyle ? `${styles.borderWidth || '1px'} ${styles.borderStyle} ${styles.borderColor || 'transparent'}` : undefined
            }}
          >
            {block.content.label}
          </Button>
        </Group>
      );
    case 'image':
      return (
        <Box style={{ textAlign: styles.textAlign as any }}>
            <img 
            src={block.content.src || 'https://via.placeholder.com/600x200?text=Image+Placeholder'} 
            alt={block.content.alt} 
            style={{ 
                maxWidth: '100%', 
                borderRadius: styles.borderRadius || rem(4),
                ...styles 
            }} 
            />
        </Box>
      );
    case 'divider':
      const dCount = block.content.count || 1;
      const dSpacing = block.content.spacing || 5;
      return (
        <Stack gap={dSpacing} my="xl">
          {Array.from({ length: dCount }).map((_, i) => (
            <Divider
              key={i}
              style={{
                borderTopWidth: styles.borderTopWidth || rem(1),
                borderTopStyle: (styles.borderTopStyle as any) || 'solid',
                borderTopColor: styles.borderTopColor || '#eeeeee',
                ...styles,
                marginTop: 0,
                marginBottom: 0
              }}
            />
          ))}
        </Stack>
      );
    case 'spacer':
      return <Box h={block.content.height || 20} style={styles} />;
    case 'list':
      return (
        <ul style={styles}>
          {(block.content.items || []).map((item: string, i: number) => (
            <li key={i}>{item}</li>
          ))}
        </ul>
      );
    case 'social':
      return (
        <Group justify={styles.textAlign as any || 'center'} gap="xs" style={styles}>
          {(block.content.links || []).map((link: any, i: number) => (
            <ActionIcon key={i} variant="light" color="blue" size="lg" radius="xl">
               <img src={link.icon} width={20} height={20} />
            </ActionIcon>
          ))}
        </Group>
      );
    case 'video':
      const vAlignment = styles.textAlign || 'center';
      const vJustify = vAlignment === 'left' ? 'flex-start' : (vAlignment === 'right' ? 'flex-end' : 'center');
      return (
        <Group justify={vJustify as any}>
          <Box style={{ position: 'relative', width: '100%', maxWidth: rem(500), ...styles }}>
             <img src={block.content.thumbnail || 'https://via.placeholder.com/600x340?text=Video+Placeholder'} style={{ width: '100%', borderRadius: styles.borderRadius || rem(8) }} />
             <ActionIcon variant="filled" color="dark" size={64} radius="xl" style={{ position: 'absolute', top: '50%', left: '50%', transform: 'translate(-50%, -50%)', opacity: 0.8 }}>
                <IconVideo size={32} />
             </ActionIcon>
          </Box>
        </Group>
      );
    case 'table':
      return (
        <Table 
            withTableBorder={block.content.withTableBorder !== false} 
            withColumnBorders={block.content.withColumnBorders !== false} 
            style={styles}
        >
          <Table.Thead>
            <Table.Tr>
              {(block.content.headers || []).map((h: string, i: number) => <Table.Th key={i}>{h}</Table.Th>)}
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {(block.content.rows || []).map((row: string[], i: number) => (
              <Table.Tr key={i}>
                {row.map((cell: string, j: number) => <Table.Td key={j}>{cell}</Table.Td>)}
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      );
    case 'columns':
      return (
        <Group grow={props.previewMode === 'desktop'} align="stretch" gap="md" style={{ ...styles, flexDirection: props.previewMode === 'mobile' && block.content.stackOnMobile !== false ? 'column' : 'row' }}>
          {(block.content.columns || []).map((col: any) => (
            <Box key={col.id} style={{ flex: 1, ...col.style, width: props.previewMode === 'mobile' && block.content.stackOnMobile !== false ? '100%' : (col.width || '50%') }}>
              <Stack gap={0} style={{ minHeight: rem(60), border: '1px dashed light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-5))', borderRadius: rem(4), padding: rem(4) }}>
                <SortableContext
                  items={(col.blocks || []).map((x: Block) => x.id)}
                  strategy={verticalListSortingStrategy}
                >
                {(col.blocks || []).map((b: Block, i: number) => {
                  const { index: _, totalBlocks: __, ...rest } = props;
                  return (
                    <RenderBlockWrapper
                      key={b.id}
                      block={b}
                      index={i}
                      totalBlocks={col.blocks.length}
                      {...rest}
                      nested
                    />
                  );
                })}
                </SortableContext>
                <Center mt="auto">
                  <Menu shadow="md" width={200} position="bottom">
                    <Menu.Target>
                      <Button 
                        variant="subtle" 
                        size="compact-xs" 
                        color="gray"
                        leftSection={<IconPlus size={10} />}
                        onClick={(e) => e.stopPropagation()}
                      >
                        Add
                      </Button>
                    </Menu.Target>

                    <Menu.Dropdown onClick={(e) => e.stopPropagation()}>
                      <Menu.Label>Add Block to Column</Menu.Label>
                      {BLOCK_TYPES.filter(bt => bt.type !== 'columns').map((bt) => (
                        <Menu.Item 
                          key={bt.type}
                          leftSection={<bt.icon size={14} />}
                          onClick={(e) => {
                            e.stopPropagation();
                            const newBlock: Block = {
                              id: newBlockId(),
                              type: bt.type,
                              content: getDefaults(bt.type).content,
                              style: getDefaults(bt.type).style,
                            };
                            const newColumns = block.content.columns.map((c: any) => 
                              c.id === col.id ? { ...c, blocks: [...c.blocks, newBlock] } : c
                            );
                            props.updateBlock(block.id, { content: { ...block.content, columns: newColumns } });
                          }}
                        >
                          {bt.label}
                        </Menu.Item>
                      ))}
                    </Menu.Dropdown>
                  </Menu>
                </Center>
              </Stack>
            </Box>
          ))}
        </Group>
      );
    default:
      return null;
  }
};

const getDefaults = (type: BlockType) => {
  switch (type) {
    case 'heading':
      return {
        content: { text: 'Heading', level: 'h1' },
        style: { fontSize: '24px', fontWeight: '700', color: '#111111', textAlign: 'left' as any, marginBottom: '16px' }
      };
    case 'text':
      return {
        content: { text: '<p>Edit this text...</p>' },
        style: { fontSize: '16px', color: '#333333', lineHeight: '1.5' }
      };
    case 'button':
      return {
        content: { label: 'Click Me', url: 'https://' },
        style: { backgroundColor: '#0073ea', color: '#ffffff', borderRadius: '4px', textAlign: 'center' as any, padding: '12px 24px', fontWeight: '600' }
      };
    case 'image':
      return {
        content: { src: '', alt: '' },
        style: { borderRadius: '4px', width: '100%', textAlign: 'center' as any }
      };
    case 'divider':
      return {
        content: {},
        style: { borderTopWidth: '1px', borderTopStyle: 'solid' as any, borderTopColor: '#eeeeee', marginTop: '20px', marginBottom: '20px' }
      };
    case 'spacer':
      return {
        content: { height: 20 },
        style: {}
      };
    case 'list':
      return {
        content: { items: ['First item', 'Second item', 'Third item'] },
        style: { fontSize: '16px', color: '#333333', paddingLeft: '20px' }
      };
    case 'social':
      return {
        content: {
          links: [
            { platform: 'Facebook', url: '#', icon: 'https://cdn-icons-png.flaticon.com/512/124/124010.png' },
            { platform: 'Twitter', url: '#', icon: 'https://cdn-icons-png.flaticon.com/512/124/124021.png' },
            { platform: 'LinkedIn', url: '#', icon: 'https://cdn-icons-png.flaticon.com/512/174/174857.png' }
          ]
        },
        style: { textAlign: 'center' as any, marginTop: '20px', marginBottom: '20px' }
      };
    case 'video':
      return {
        content: { url: 'https://youtube.com', thumbnail: '' },
        style: { textAlign: 'center' as any, borderRadius: '8px', marginTop: '20px', marginBottom: '20px' }
      };
    case 'table':
      return {
        content: {
          headers: ['Product', 'Price', 'Quantity'],
          rows: [['Item 1', '$99', '1'], ['Item 2', '$29', '1']]
        },
        style: { color: '#333333', marginTop: '20px', marginBottom: '20px' }
      };
    case 'columns':
      return {
        content: {
          columns: [
            { id: 'c1', width: '50%', blocks: [], style: {} },
            { id: 'c2', width: '50%', blocks: [], style: {} }
          ],
          stackOnMobile: true
        },
        style: { marginTop: '10px', marginBottom: '10px' }
      };
    default:
      return { content: {}, style: {} };
  }
};

CustomEmailBuilder.displayName = 'CustomEmailBuilder';
