import React, { useState, useImperativeHandle, forwardRef } from 'react';
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
  TextInput
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
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
  IconCode
} from '@tabler/icons-react';
import { Block, BlockType, EmailDesign } from './types';
import { generateHTML } from './htmlGenerator';
import { PropertyEditor } from './PropertyEditor';

export interface CustomEmailBuilderHandle {
  exportHtml: () => { design: EmailDesign; html: string };
}

interface CustomEmailBuilderProps {
  initialDesign?: string;
}

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

export const CustomEmailBuilder = forwardRef<CustomEmailBuilderHandle, CustomEmailBuilderProps>(({ initialDesign }, ref) => {
  const [design, setDesign] = useState<EmailDesign>(() => {
    if (initialDesign) {
      try {
        return JSON.parse(initialDesign);
      } catch (e) {
        console.error('Failed to parse initial design', e);
      }
    }
    return DEFAULT_DESIGN;
  });

  const [selectedBlockId, setSelectedBlockId] = useState<string | null>(null);
  const [previewMode, setPreviewMode] = useState<'desktop' | 'mobile'>('desktop');
  const [codeOpened, { open: openCode, close: closeCode }] = useDisclosure(false);

  useImperativeHandle(ref, () => ({
    exportHtml: () => {
      const html = generateHTML(design);
      return { design, html };
    }
  }));

  const addBlock = (type: BlockType, index?: number) => {
    const newBlock: Block = {
      id: Math.random().toString(36).substr(2, 9),
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
    const deleteRecursive = (blocks: Block[]): Block[] => {
      return blocks.filter(b => {
        if (b.id === id) return false;
        if (b.type === 'columns' && b.content.columns) {
          b.content.columns = b.content.columns.map((col: any) => ({
            ...col,
            blocks: deleteRecursive(col.blocks)
          }));
        }
        return true;
      });
    };
    setDesign(prev => ({ ...prev, blocks: deleteRecursive(prev.blocks) }));
    if (selectedBlockId === id) setSelectedBlockId(null);
  };

  const duplicateBlock = (block: Block) => {
    const reIDBlock = (b: Block): Block => {
      const newId = Math.random().toString(36).substr(2, 9);
      if (b.type === 'columns' && b.content.columns) {
        return {
          ...b,
          id: newId,
          content: {
            ...b.content,
            columns: b.content.columns.map((col: any) => ({
              ...col,
              id: Math.random().toString(36).substr(2, 9),
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
    setDesign(prev => ({ ...prev, blocks: updateRecursive(prev.blocks) }));
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

  return (
    <Box h="100%" style={{ display: 'flex', flexDirection: 'column' }}>
      <Group justify="space-between" p="xs" style={{
        backgroundColor: 'light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))',
        borderBottom: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))'
      }}>
        <Group gap="xs">
          <Tooltip label="Desktop View">
            <ActionIcon
              variant={previewMode === 'desktop' ? 'filled' : 'light'}
              onClick={() => setPreviewMode('desktop')}
              color="brand"
            >
              <IconDeviceDesktop size={18} />
            </ActionIcon>
          </Tooltip>
          <Tooltip label="Mobile View">
            <ActionIcon
              variant={previewMode === 'mobile' ? 'filled' : 'light'}
              onClick={() => setPreviewMode('mobile')}
              color="brand"
            >
              <IconDeviceMobile size={18} />
            </ActionIcon>
          </Tooltip>
          <Divider orientation="vertical" />
          <Tooltip label="Preview HTML Code">
            <ActionIcon
              variant="light"
              onClick={openCode}
              color="brand"
            >
              <IconCode size={18} />
            </ActionIcon>
          </Tooltip>
        </Group>
        <Text size="sm" fw={600} c="light-dark(var(--mantine-color-gray-8), var(--mantine-color-dark-2))">Visual Email Builder</Text>
        <Box w={100} />
      </Group>

      <Modal opened={codeOpened} onClose={closeCode} title="HTML Preview" size="xl">
        <Stack>
          <Text size="sm">This is the generated HTML that will be sent to recipients.</Text>
          <ScrollArea h={500} offsetScrollbars>
            <Code block style={{ whiteSpace: 'pre-wrap' }}>{generateHTML(design)}</Code>
          </ScrollArea>
          <Group justify="flex-end">
            <Button onClick={closeCode}>Close</Button>
          </Group>
        </Stack>
      </Modal>

      <Box style={{ flex: 1, display: 'flex', overflow: 'hidden' }}>
        {/* Sidebar: Blocks */}
        <Box style={{ width: 250, borderRight: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
          <ScrollArea h="100%">
            <Paper p="md" h="100%" radius={0} bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))">
              <Stack gap="lg">
                <Box>
                  <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="xs">Basic</Text>
                  <SimpleGrid cols={2} spacing="xs">
                    {BLOCK_TYPES.filter(bt => ['heading', 'text', 'button', 'divider', 'spacer'].includes(bt.type)).map(bt => (
                      <BlockItem key={bt.type} bt={bt} onClick={() => addBlock(bt.type)} />
                    ))}
                  </SimpleGrid>
                </Box>

                <Box>
                  <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="xs">Media</Text>
                  <SimpleGrid cols={2} spacing="xs">
                    {BLOCK_TYPES.filter(bt => ['image', 'video'].includes(bt.type)).map(bt => (
                      <BlockItem key={bt.type} bt={bt} onClick={() => addBlock(bt.type)} />
                    ))}
                  </SimpleGrid>
                </Box>

                <Box>
                  <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="xs">Layout</Text>
                  <SimpleGrid cols={2} spacing="xs">
                    {BLOCK_TYPES.filter(bt => ['columns', 'table'].includes(bt.type)).map(bt => (
                      <BlockItem key={bt.type} bt={bt} onClick={() => addBlock(bt.type)} />
                    ))}
                  </SimpleGrid>
                </Box>

                <Box>
                  <Text size="xs" fw={700} tt="uppercase" c="dimmed" mb="xs">Social</Text>
                  <SimpleGrid cols={2} spacing="xs">
                    {BLOCK_TYPES.filter(bt => ['social'].includes(bt.type)).map(bt => (
                      <BlockItem key={bt.type} bt={bt} onClick={() => addBlock(bt.type)} />
                    ))}
                  </SimpleGrid>
                </Box>
              </Stack>
            </Paper>
          </ScrollArea>
        </Box>

        {/* Main Canvas */}
        <Box style={{ flex: 1, backgroundColor: 'light-dark(var(--mantine-color-gray-1), var(--mantine-color-dark-8))', overflow: 'hidden' }}>
          <ScrollArea h="100%" p="xl">
            <Center>
              <Box
                w={previewMode === 'desktop' ? (parseInt(design.bodyStyle.contentWidth || '600') + 80) : 375}
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
`,oldText:                    <Stack gap={0}>
                      {design.blocks.map((block, index) => (
                    <React.Fragment key={block.id}>
                      {index === 0 && <AddBlockMenu onAdd={(type) => addBlock(type, 0)} label="Insert Block" />}
                      <RenderBlockWrapper
                        block={block}
                        index={index}
                        totalBlocks={design.blocks.length}
                        selectedBlockId={selectedBlockId}
                        setSelectedBlockId={setSelectedBlockId}
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
        </Box>

        {/* Sidebar: Properties */}
        <Box style={{ width: 350, borderLeft: '1px solid light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-4))' }}>
          <ScrollArea h="100%">
            <Paper p="md" h="100%" radius={0} bg="light-dark(var(--mantine-color-white), var(--mantine-color-dark-7))">
              {selectedBlock ? (
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
                    onChange={(e) => setDesign(prev => ({ ...prev, preheader: e.currentTarget.value }))}
                    mb="xs"
                  />
                  <ColorInput
                    label="Background Color"
                    value={design.bodyStyle.backgroundColor}
                    onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, backgroundColor: val } }))}
                  />
                  <ColorInput
                    label="Default Text Color"
                    value={design.bodyStyle.color || '#333333'}
                    onChange={(val) => setDesign(prev => ({ ...prev, bodyStyle: { ...prev.bodyStyle, color: val } }))}
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
              )}
            </Paper>
          </ScrollArea>
        </Box>
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
  previewMode: 'desktop' | 'mobile';
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
  return (
    <Box
      style={{
        position: 'relative',
        padding: rem(8),
        border: selectedBlockId === block.id ? '2px solid var(--mantine-color-brand-6)' : (nested ? '1px dashed light-dark(var(--mantine-color-gray-3), var(--mantine-color-dark-4))' : '1px dashed transparent'),
        borderRadius: rem(4),
        cursor: 'pointer',
        transition: 'all 0.2s',
        marginBottom: rem(selectedBlockId === block.id ? 8 : 4)
      }}
      onClick={(e) => {
        e.stopPropagation();
        setSelectedBlockId(block.id);
      }}
    >
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
      return <div style={styles} dangerouslySetInnerHTML={{ __html: block.content.text }} />;
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
                {col.blocks.map((b: Block, i: number) => {
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
                              id: Math.random().toString(36).substr(2, 9),
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
