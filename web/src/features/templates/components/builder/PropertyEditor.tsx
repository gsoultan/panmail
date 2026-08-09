import React, { useMemo, useState } from 'react';
import { 
  Stack, 
  Text, 
  TextInput, 
  Textarea, 
  ColorInput, 
  NumberInput, 
  Group, 
  Divider,
  Select,
  Box,
  Button,
  Paper,
  ActionIcon,
  SimpleGrid,
  Checkbox,
  Tabs
} from '@mantine/core';
import { IconPlus, IconTrash, IconVariable, IconSettings, IconPalette, IconForms, IconRefresh } from '@tabler/icons-react';
import { Block } from './types';
import { VariableInput } from './VariableInput';

interface PropertyEditorProps {
  block: Block;
  onChange: (updates: Partial<Block>) => void;
  /**
   * Every variable the design already refers to, so the second use of a
   * project's own name is offered rather than retyped from memory — which is
   * how {{order_id}} and {{orderId}} end up in the same template.
   */
  designVariables?: string[];
}

const BUILTIN_VARS = [
  '{{name}}',
  '{{email}}',
  '{{company}}',
  '{{unsubscribe_url}}',
  '{{current_date}}',
  '{{current_year}}',
  '{{logo_url}}',
  '{{verification_link}}',
];

// Variables the author added themselves are kept across reloads. They were
// component state, so every one had to be retyped after a refresh — which for a
// list whose whole purpose is saving typing made the feature close to useless.
//
// localStorage rather than the server: this is a per-person convenience list,
// not part of the template, and it must not become another thing that can fail
// to save.
const CUSTOM_VARS_KEY = 'panmail.builder.customVariables';

const loadCustomVars = (): string[] => {
  try {
    const raw = localStorage.getItem(CUSTOM_VARS_KEY);
    if (!raw) return [];
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v) => typeof v === 'string') : [];
  } catch {
    // Private browsing, a disabled store or corrupt JSON: the built-ins still
    // work, and losing a convenience list is not worth breaking the editor.
    return [];
  }
};

export const PropertyEditor: React.FC<PropertyEditorProps> = ({ block, onChange, designVariables = [] }) => {
  const [varAssistantOpened, setVarAssistantOpened] = useState(false);
  const [userVars, setUserVars] = useState<string[]>(loadCustomVars);
  const [newVar, setNewVar] = useState('');

  const customVars = [...BUILTIN_VARS, ...userVars.filter((v) => !BUILTIN_VARS.includes(v))];

  // The autocomplete offers bare names; the assistant list above is braced.
  const suggestions = useMemo(() => {
    const bare = customVars.map((v) => v.replace(/^\{\{|\}\}$/g, ''));
    return [...new Set([...designVariables, ...bare])];
  }, [customVars, designVariables]);

  const updateContent = (key: string, value: any) => {
    onChange({ content: { ...block.content, [key]: value } });
  };

  const updateStyle = (key: string, value: any) => {
    onChange({ style: { ...block.style, [key]: value } });
  };

  const addVariable = () => {
    // Accept either `foo` or `{{foo}}`, since both are natural to type.
    const name = newVar.trim().replace(/^\{+|\}+$/g, '').trim();
    if (!name) return;

    const token = `{{${name}}}`;
    if (customVars.includes(token)) {
      setNewVar('');
      return;
    }

    const next = [...userVars, token];
    setUserVars(next);
    setNewVar('');
    try {
      localStorage.setItem(CUSTOM_VARS_KEY, JSON.stringify(next));
    } catch {
      // Kept in memory for this session even if it cannot be persisted.
    }
  };

  const removeVariable = (token: string) => {
    const next = userVars.filter((v) => v !== token);
    setUserVars(next);
    try {
      localStorage.setItem(CUSTOM_VARS_KEY, JSON.stringify(next));
    } catch {
      /* nothing to do */
    }
  };

  const insertVariable = (v: string) => {
    if (block.type === 'text') {
      updateContent('text', (block.content.text || '') + v);
    } else if (block.type === 'heading') {
      updateContent('text', (block.content.text || '') + v);
    } else if (block.type === 'button') {
      updateContent('label', (block.content.label || '') + v);
    } else if (block.type === 'list') {
      // The button is offered for lists (see the block-type filter below) but
      // used to do nothing at all, because this chain had no branch for them.
      const items = block.content.items || [];
      const next = items.length ? [...items] : [''];
      next[next.length - 1] = (next[next.length - 1] || '') + v;
      updateContent('items', next);
    } else if (block.type === 'table') {
        if (block.content.rows && block.content.rows[0]) {
            const newRows = block.content.rows.map((row: string[], i: number) =>
              i === 0 ? row.map((cell, j) => (j === 0 ? (cell || '') + v : cell)) : row,
            );
            updateContent('rows', newRows);
        }
    }
  };

  return (
    <Tabs defaultValue="content" variant="pills" radius="md">
      <Tabs.List grow mb="md">
        <Tabs.Tab value="content" leftSection={<IconForms size={14} />}>Content</Tabs.Tab>
        <Tabs.Tab value="style" leftSection={<IconPalette size={14} />}>Style</Tabs.Tab>
        <Tabs.Tab value="advanced" leftSection={<IconSettings size={14} />}>Advanced</Tabs.Tab>
      </Tabs.List>

      <Tabs.Panel value="content">
        <Stack gap="md">
          <Text fw={700} size="sm" tt="uppercase">{block.type} Content</Text>
          <Divider />
          
          {['text', 'heading', 'button', 'table', 'list'].includes(block.type) && (
            <Box>
                <Button 
                    variant="light" 
                    size="compact-xs" 
                    leftSection={<IconVariable size={12} />}
                    onClick={() => setVarAssistantOpened(v => !v)}
                    fullWidth
                    mb="xs"
                >
                    {varAssistantOpened ? 'Hide Variable Assistant' : 'Show Variable Assistant'}
                </Button>
                
                {varAssistantOpened && (
                    <Paper withBorder p="xs" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-6))" radius="md" mb="md">
                    <Stack gap="xs">
                        <Text size="xs" c="dimmed">Click to insert a variable:</Text>
                        <Group gap={4} wrap="wrap">
                        {customVars.map(v => {
                            const isCustom = !BUILTIN_VARS.includes(v);
                            return (
                            <Button
                            key={v}
                            variant="light"
                            size="compact-xs"
                            onClick={() => insertVariable(v)}
                            // Right-click removes a variable the author added.
                            // Built-ins have no remove path, so they cannot be
                            // lost by accident.
                            onContextMenu={isCustom ? (e) => { e.preventDefault(); removeVariable(v); } : undefined}
                            title={isCustom ? `${v} — right-click to remove` : v}
                            color={isCustom ? 'grape' : 'indigo'}
                            radius="xs"
                            >
                            {v}
                            </Button>
                            );
                        })}
                        </Group>

                        <Divider />
                        <Group gap="xs">
                        <TextInput
                            placeholder="New variable..."
                            size="xs"
                            value={newVar}
                            onChange={(e) => setNewVar(e.currentTarget.value)}
                            style={{ flex: 1 }}
                        />
                        <ActionIcon size="sm" color="indigo" onClick={addVariable} disabled={!newVar} variant="filled">
                            <IconPlus size={14} />
                        </ActionIcon>
                        </Group>
                    </Stack>
                    </Paper>
                )}
            </Box>
          )}

          {block.type === 'heading' && (
            <>
              <VariableInput
                label="Heading Text"
                size="sm"
                value={block.content.text || ''}
                onChange={(v) => updateContent('text', v)}
                variables={suggestions}
              />
              <Select
                label="Level"
                data={['h1', 'h2', 'h3', 'h4', 'h5', 'h6']}
                value={block.content.level}
                onChange={(val) => updateContent('level', val)}
              />
            </>
          )}

          {block.type === 'text' && (
            <VariableInput
                label="Text (HTML)"
                multiline
                minRows={8}
                autosize
                size="sm"
                value={block.content.text || ''}
                onChange={(v) => updateContent('text', v)}
                variables={suggestions}
            />
          )}

          {block.type === 'button' && (
            <>
              <VariableInput
                label="Label"
                size="sm"
                value={block.content.label || ''}
                onChange={(v) => updateContent('label', v)}
                variables={suggestions}
              />
              {/* URLs carry variables too — a per-recipient tracking or
                  verification link is the usual case. */}
              <VariableInput
                label="URL"
                size="sm"
                value={block.content.url || ''}
                onChange={(v) => updateContent('url', v)}
                variables={suggestions}
              />
            </>
          )}

          {block.type === 'image' && (
            <>
              <TextInput
                label="Image URL"
                value={block.content.src}
                onChange={(e) => updateContent('src', e.currentTarget.value)}
                placeholder="https://example.com/image.png"
              />
              <TextInput
                label="Alt Text"
                value={block.content.alt}
                onChange={(e) => updateContent('alt', e.currentTarget.value)}
              />
              <TextInput
                label="Link URL (Optional)"
                value={block.content.linkUrl || ''}
                onChange={(e) => updateContent('linkUrl', e.currentTarget.value)}
              />
            </>
          )}

          {block.type === 'list' && (
              <Stack gap="xs">
                  <Text size="sm">List Items (one per line)</Text>
                  <Textarea
                      value={(block.content.items || []).join('\n')}
                      onChange={(e) => updateContent('items', e.currentTarget.value.split('\n'))}
                      minRows={5}
                  />
              </Stack>
          )}

          {block.type === 'spacer' && (
            <NumberInput
              label="Height (px)"
              value={block.content.height}
              onChange={(val) => updateContent('height', val)}
            />
          )}

          {block.type === 'divider' && (
            <Stack gap="xs">
              <NumberInput
                label="Number of Lines"
                value={block.content.count || 1}
                onChange={(val) => updateContent('count', val)}
                min={1}
                max={10}
              />
              {(block.content.count || 1) > 1 && (
                <NumberInput
                  label="Spacing (px)"
                  value={block.content.spacing || 5}
                  onChange={(val) => updateContent('spacing', val)}
                  min={0}
                />
              )}
            </Stack>
          )}

          {block.type === 'social' && (
              <Stack gap="xs">
                  <Text size="sm" fw={600}>Social Channels</Text>
                  {(block.content.links || []).map((link: any, index: number) => (
                      <Paper key={index} withBorder p="xs">
                          <Stack gap={4}>
                              <Group justify="space-between">
                                <Select
                                  size="xs"
                                  data={['Facebook', 'Twitter', 'LinkedIn', 'Instagram', 'YouTube', 'GitHub', 'Custom']}
                                  value={['Facebook', 'Twitter', 'LinkedIn', 'Instagram', 'YouTube', 'GitHub'].includes(link.platform) ? link.platform : 'Custom'}
                                  onChange={(val) => {
                                    const icons: any = {
                                      'Facebook': 'https://cdn-icons-png.flaticon.com/512/124/124010.png',
                                      'Twitter': 'https://cdn-icons-png.flaticon.com/512/124/124021.png',
                                      'LinkedIn': 'https://cdn-icons-png.flaticon.com/512/174/174857.png',
                                      'Instagram': 'https://cdn-icons-png.flaticon.com/512/2111/2111463.png',
                                      'YouTube': 'https://cdn-icons-png.flaticon.com/512/1384/1384060.png',
                                      'GitHub': 'https://cdn-icons-png.flaticon.com/512/733/733553.png',
                                      'Custom': link.icon
                                    };
                                    const platform = val || 'Custom';
                                    // Replaced, not assigned into: spreading the
                                    // array still shares the link objects with
                                    // the current state, so writing a field here
                                    // edited the design React was rendering from.
                                    const newLinks = block.content.links.map((l: any, i: number) =>
                                      i === index ? { ...l, platform, icon: icons[platform] } : l,
                                    );
                                    updateContent('links', newLinks);
                                  }}
                                  style={{ flex: 1 }}
                                />
                                <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                                    const newLinks = block.content.links.filter((_: any, idx: number) => idx !== index);
                                    updateContent('links', newLinks);
                                }}>
                                    <IconTrash size={12} />
                                </ActionIcon>
                              </Group>
                              {link.platform === 'Custom' && (
                                <TextInput
                                  label="Icon URL"
                                  size="xs"
                                  value={link.icon}
                                  onChange={(e) => {
                                    const icon = e.currentTarget.value;
                                    const newLinks = block.content.links.map((l: any, i: number) =>
                                      i === index ? { ...l, icon } : l,
                                    );
                                    updateContent('links', newLinks);
                                  }}
                                />
                              )}
                              <TextInput
                                  label="URL"
                                  size="xs"
                                  value={link.url}
                                  onChange={(e) => {
                                      const url = e.currentTarget.value;
                                      const newLinks = block.content.links.map((l: any, i: number) =>
                                        i === index ? { ...l, url } : l,
                                      );
                                      updateContent('links', newLinks);
                                  }}
                              />
                          </Stack>
                      </Paper>
                  ))}
                  <Button size="compact-xs" variant="light" leftSection={<IconPlus size={12} />} onClick={() => {
                      const newLink = { platform: 'Facebook', url: '#', icon: 'https://cdn-icons-png.flaticon.com/512/124/124010.png' };
                      updateContent('links', [...block.content.links, newLink]);
                  }}>Add Social Link</Button>
              </Stack>
          )}

          {block.type === 'video' && (
            <>
              <TextInput
                label="Video URL"
                value={block.content.url}
                onChange={(e) => updateContent('url', e.currentTarget.value)}
              />
              <TextInput
                label="Thumbnail URL"
                value={block.content.thumbnail}
                onChange={(e) => updateContent('thumbnail', e.currentTarget.value)}
              />
            </>
          )}

          {block.type === 'columns' && (
            <Stack gap="xs">
              <Paper withBorder p="xs" radius="md" mb="xs">
                <Stack gap="xs">
                  <Text size="xs" fw={700}>Background image</Text>
                  <TextInput
                    placeholder="https://example.com/hero.jpg"
                    size="xs"
                    value={block.content.backgroundImage || ''}
                    onChange={(e) => updateContent('backgroundImage', e.currentTarget.value)}
                  />
                  {block.content.backgroundImage && (
                    <>
                      <NumberInput
                        label="Height in Outlook"
                        description="Outlook cannot size a background to its content, so it needs a number. Everything else grows to fit."
                        size="xs"
                        min={40}
                        value={block.content.backgroundHeight || 300}
                        onChange={(v) => updateContent('backgroundHeight', Number(v) || 300)}
                      />
                      <Text size="xs" c="dimmed">
                        Set a background colour in the Style tab as well. Images are
                        blocked by default in most clients, so the colour is what most
                        recipients see first — light text on no background arrives blank.
                      </Text>
                    </>
                  )}
                </Stack>
              </Paper>
              <Text size="sm" fw={600}>Columns Layout</Text>
              <Group grow>
                <Button size="compact-xs" variant="outline" onClick={() => updateContent('columns', [{ id: 'c1', width: '100%', blocks: [], style: {} }])}>1 Col</Button>
                <Button size="compact-xs" variant="outline" onClick={() => updateContent('columns', [{ id: 'c1', width: '50%', blocks: [], style: {} }, { id: 'c2', width: '50%', blocks: [], style: {} }])}>2 Col</Button>
                <Button size="compact-xs" variant="outline" onClick={() => updateContent('columns', [{ id: 'c1', width: '33.33%', blocks: [], style: {} }, { id: 'c2', width: '33.33%', blocks: [], style: {} }, { id: 'c3', width: '33.33%', blocks: [], style: {} }])}>3 Col</Button>
              </Group>
              <Divider />
              {(block.content.columns || []).map((col: any, i: number) => (
                <Paper key={col.id} withBorder p="xs">
                  <Stack gap="xs">
                    <Group justify="space-between">
                      <Text size="xs" fw={700}>Column {i + 1}</Text>
                      <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                        const newCols = block.content.columns.filter((_: any, idx: number) => idx !== i);
                        updateContent('columns', newCols);
                      }}>
                        <IconTrash size={12} />
                      </ActionIcon>
                    </Group>
                    <TextInput
                      label="Width"
                      size="xs"
                      value={col.width}
                      onChange={(e) => {
                        const newCols = [...block.content.columns];
                        newCols[i] = { ...newCols[i], width: e.currentTarget.value };
                        updateContent('columns', newCols);
                      }}
                    />
                  </Stack>
                </Paper>
              ))}
            </Stack>
          )}

          {block.type === 'table' && (
            <Stack gap="xs">
              <Text size="xs" fw={700}>Table Headers</Text>
              <Group gap="xs">
                {(block.content.headers || []).map((h: string, i: number) => (
                  <Group key={i} gap={4}>
                    <TextInput
                      size="xs"
                      value={h}
                      onChange={(e) => {
                        const newHeaders = [...block.content.headers];
                        newHeaders[i] = e.currentTarget.value;
                        updateContent('headers', newHeaders);
                      }}
                      style={{ width: 80 }}
                    />
                    <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                      const newHeaders = block.content.headers.filter((_: any, idx: number) => idx !== i);
                      const newRows = block.content.rows.map((row: any) => row.filter((_: any, idx: number) => idx !== i));
                      onChange({ content: { ...block.content, headers: newHeaders, rows: newRows } });
                    }}>
                      <IconTrash size={12} />
                    </ActionIcon>
                  </Group>
                ))}
                <Button size="compact-xs" leftSection={<IconPlus size={12} />} onClick={() => {
                  const newHeaders = [...block.content.headers, 'New'];
                  const newRows = block.content.rows.map((row: any) => [...row, '']);
                  onChange({ content: { ...block.content, headers: newHeaders, rows: newRows } });
                }}>Add Col</Button>
              </Group>

              <Divider />
              <Text size="xs" fw={700}>{block.content.loopVariable ? 'Row Template' : 'Rows'}</Text>
              <Stack gap={4}>
                {(block.content.rows || []).map((row: string[], i: number) => {
                  if (block.content.loopVariable && i > 0) return null;
                  return (
                    <Paper key={i} withBorder p="xs" radius="xs">
                      <Group gap="xs">
                        {row.map((cell: string, j: number) => (
                          <TextInput
                            key={j}
                            size="xs"
                            value={cell}
                            onChange={(e) => {
                              // The outer array was copied but the row was not,
                              // so `newRows[i][j] = ...` wrote through into the
                              // row object still held by the current state.
                              const value = e.currentTarget.value;
                              const newRows = block.content.rows.map((row: string[], ri: number) =>
                                ri === i ? row.map((cell, ci) => (ci === j ? value : cell)) : row,
                              );
                              updateContent('rows', newRows);
                            }}
                            placeholder={block.content.headers[j]}
                            style={{ width: 80 }}
                          />
                        ))}
                        {!block.content.loopVariable && (
                          <ActionIcon size="xs" color="red" variant="subtle" onClick={() => {
                            const newRows = block.content.rows.filter((_: any, idx: number) => idx !== i);
                            updateContent('rows', newRows);
                          }}>
                            <IconTrash size={12} />
                          </ActionIcon>
                        )}
                      </Group>
                    </Paper>
                  );
                })}
                {!block.content.loopVariable && (
                  <Button size="compact-xs" leftSection={<IconPlus size={12} />} variant="light" onClick={() => {
                    const newRow = new Array(block.content.headers.length).fill('');
                    updateContent('rows', [...block.content.rows, newRow]);
                  }}>Add Row</Button>
                )}
              </Stack>
            </Stack>
          )}
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="style">
        <Stack gap="sm">
          <Text fw={700} size="sm" tt="uppercase">{block.type} Appearance</Text>
          <Divider />
          
          <SimpleGrid cols={2} spacing="xs">
              {['text', 'button', 'heading', 'list', 'table'].includes(block.type) && (
                <ColorInput
                  label="Text Color"
                  size="xs"
                  value={block.style.color as string}
                  onChange={(val) => updateStyle('color', val)}
                />
              )}

              {['text', 'heading', 'button', 'list', 'table', 'columns', 'spacer'].includes(block.type) && (
                <ColorInput
                  label="Background"
                  size="xs"
                  value={block.style.backgroundColor as string}
                  onChange={(val) => updateStyle('backgroundColor', val)}
                />
              )}

              {['text', 'heading', 'button', 'social', 'columns', 'table', 'image'].includes(block.type) && (
                <Select
                  label="Alignment"
                  size="xs"
                  data={['left', 'center', 'right', 'justify']}
                  value={block.style.textAlign as string}
                  onChange={(val) => updateStyle('textAlign', val)}
                />
              )}
          </SimpleGrid>

          {['text', 'heading', 'list', 'button'].includes(block.type) && (
            <SimpleGrid cols={2} spacing="xs">
              <NumberInput
                  label="Font Size (px)"
                  size="xs"
                  value={parseInt(block.style.fontSize as string) || 16}
                  onChange={(val) => updateStyle('fontSize', `${val}px`)}
              />
              <Select
                  label="Font Weight"
                  size="xs"
                  data={['300', '400', '500', '600', '700', '800']}
                  value={(block.style.fontWeight as string) || '400'}
                  onChange={(val) => updateStyle('fontWeight', val)}
              />
              <NumberInput
                  label="Line Height"
                  size="xs"
                  step={0.1}
                  decimalScale={1}
                  value={parseFloat(block.style.lineHeight as string) || 1.5}
                  onChange={(val) => updateStyle('lineHeight', val)}
              />
              {block.type === 'button' && (
                <NumberInput
                  label="Letter Spacing"
                  size="xs"
                  step={0.5}
                  value={parseFloat(block.style.letterSpacing as string) || 0}
                  onChange={(val) => updateStyle('letterSpacing', `${val}px`)}
                />
              )}
            </SimpleGrid>
          )}

          {['button', 'image', 'text', 'heading', 'table', 'video'].includes(block.type) && (
              <SimpleGrid cols={2} spacing="xs">
                <NumberInput
                    label="Border Radius"
                    size="xs"
                    value={parseInt(block.style.borderRadius as string) || 0}
                    onChange={(val) => updateStyle('borderRadius', `${val}px`)}
                />
                <Select
                  label="Border Style"
                  size="xs"
                  data={['none', 'solid', 'dashed', 'dotted']}
                  value={(block.style.borderStyle as string) || 'none'}
                  onChange={(val) => updateStyle('borderStyle', val)}
                />
                {(block.style.borderStyle && block.style.borderStyle !== 'none') && (
                  <>
                    <NumberInput
                        label="Border Width"
                        size="xs"
                        value={parseInt(block.style.borderWidth as string) || 1}
                        onChange={(val) => updateStyle('borderWidth', `${val}px`)}
                    />
                    <ColorInput
                        label="Border Color"
                        size="xs"
                        value={block.style.borderColor as string}
                        onChange={(val) => updateStyle('borderColor', val)}
                    />
                  </>
                )}
              </SimpleGrid>
          )}

          <Divider label="Spacing" labelPosition="center" size="xs" />
          
          <Box>
            <Text size="xs" fw={700} mb={4}>Margin (Outer Space)</Text>
            <SimpleGrid cols={2} spacing="xs">
                <NumberInput label="Top" size="xs" value={parseInt(block.style.marginTop as string) || 0} onChange={(val) => updateStyle('marginTop', `${val}px`)} />
                <NumberInput label="Bottom" size="xs" value={parseInt(block.style.marginBottom as string) || 0} onChange={(val) => updateStyle('marginBottom', `${val}px`)} />
            </SimpleGrid>
          </Box>

          <Box>
            <Text size="xs" fw={700} mb={4}>Padding (Inner Space)</Text>
            <SimpleGrid cols={2} spacing="xs">
                <NumberInput label="Top" size="xs" value={parseInt(block.style.paddingTop as string) || 0} onChange={(val) => updateStyle('paddingTop', `${val}px`)} />
                <NumberInput label="Bottom" size="xs" value={parseInt(block.style.paddingBottom as string) || 0} onChange={(val) => updateStyle('paddingBottom', `${val}px`)} />
                <NumberInput label="Left" size="xs" value={parseInt(block.style.paddingLeft as string) || 0} onChange={(val) => updateStyle('paddingLeft', `${val}px`)} />
                <NumberInput label="Right" size="xs" value={parseInt(block.style.paddingRight as string) || 0} onChange={(val) => updateStyle('paddingRight', `${val}px`)} />
            </SimpleGrid>
          </Box>

          {['button', 'image', 'table'].includes(block.type) && (
              <Select
                  label="Width Mode"
                  size="xs"
                  data={[
                      { label: 'Auto', value: 'auto' },
                      { label: 'Full Width (100%)', value: '100%' },
                      { label: 'Half Width (50%)', value: '50%' },
                  ]}
                  value={(block.style.width as string) || 'auto'}
                  onChange={(val) => updateStyle('width', val)}
              />
          )}
        </Stack>
      </Tabs.Panel>

      <Tabs.Panel value="advanced">
        <Stack gap="md">
          <Text fw={700} size="sm" tt="uppercase">Advanced Settings</Text>
          <Divider />

          {/* Offered on every block. The old list left out heading, divider,
              spacer, social and video for no reason anyone recorded, so a
              whole discount section could be conditional except its heading. */}
          <Paper withBorder p="md" bg="light-dark(var(--mantine-color-blue-0), var(--mantine-color-blue-9))" radius="md">
            <Stack gap="xs">
              <Group gap="xs">
                <IconSettings size={16} />
                <Text size="sm" fw={700}>Show conditionally</Text>
              </Group>
              <TextInput
                label="Only show when this has a value"
                placeholder="e.g. has_discount"
                value={block.content.ifVariable || ''}
                onChange={(e) => updateContent('ifVariable', e.currentTarget.value)}
                size="xs"
              />
              <Text size="xs" c="dimmed">
                Wrapped in <code>{"{{#if variable}}...{{/if}}"}</code>. An empty
                list counts as no value, so a section keyed on one disappears
                when it has nothing to show.
              </Text>
            </Stack>
          </Paper>

          {/* Also every block. Looping used to be list and table only, which
              meant a line item could be a row of text and nothing else — a
              cart row is an image beside a name beside a price, and that is a
              columns block. */}
          <Paper withBorder p="md" bg="light-dark(var(--mantine-color-orange-0), var(--mantine-color-orange-9))" radius="md">
            <Stack gap="xs">
              <Group gap="xs">
                <IconRefresh size={16} />
                <Text size="sm" fw={700}>Repeat for each item</Text>
              </Group>
              <TextInput
                label="List variable"
                placeholder="e.g. items"
                value={block.content.loopVariable || ''}
                onChange={(e) => updateContent('loopVariable', e.currentTarget.value)}
                size="xs"
              />

              {['list', 'table'].includes(block.type) && (
                <TextInput
                  label="Item name"
                  placeholder="item"
                  value={block.content.loopItemVariable || 'item'}
                  onChange={(e) => updateContent('loopItemVariable', e.currentTarget.value)}
                  size="xs"
                />
              )}

              {block.content.loopVariable && (
                <TextInput
                  label="When the list is empty"
                  placeholder="e.g. Your cart is empty."
                  value={block.content.emptyText || ''}
                  onChange={(e) => updateContent('emptyText', e.currentTarget.value)}
                  size="xs"
                />
              )}

              <Text size="xs" c="dimmed">
                {block.content.loopVariable
                  ? <>Inside the loop, refer to each item's fields directly — <code>{"{{name}}"}</code>, <code>{"{{price}}"}</code>.</>
                  : <>Repeats this block once per entry, so one row can be built and reused for every line item.</>}
              </Text>
            </Stack>
          </Paper>

          {block.type === 'columns' && (
             <Paper withBorder p="md" radius="md">
               <Stack gap="xs">
                 <Text size="xs" fw={700}>Responsive Layout</Text>
                 <Checkbox 
                  label="Stack on mobile" 
                  checked={block.content.stackOnMobile !== false}
                  onChange={(e) => updateContent('stackOnMobile', e.currentTarget.checked)}
                 />
               </Stack>
             </Paper>
          )}
        </Stack>
      </Tabs.Panel>
    </Tabs>
  );
};
