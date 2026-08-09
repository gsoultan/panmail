import { Block } from './types';

/**
 * Reordering, as pure functions over the design's block tree.
 *
 * Kept out of the component so the tree-walking can be tested directly. The
 * nested case is where this gets fiddly: a column's blocks live two levels down
 * inside `content.columns[].blocks`, and the previous implementation could not
 * reorder them at all.
 */

/**
 * Which list a block belongs to.
 *
 * Sorting uses a single DndContext with one SortableContext per list, rather
 * than nesting contexts — nested ones both see the same pointer events and
 * fight over them. That means the drag handler has to work out for itself which
 * list the dragged block came from, which is what this is for.
 */
export type Container =
  | { kind: 'root' }
  | { kind: 'column'; columnsBlockId: string; columnId: string };

export const findContainer = (blocks: Block[], id: string): Container | null => {
  for (const b of blocks) {
    if (b.id === id) return { kind: 'root' };
  }

  const search = (items: Block[]): Container | null => {
    for (const b of items) {
      if (b.type !== 'columns' || !b.content.columns) continue;
      for (const col of b.content.columns) {
        const colBlocks: Block[] = col?.blocks || [];
        if (colBlocks.some((x) => x.id === id)) {
          return { kind: 'column', columnsBlockId: b.id, columnId: col.id };
        }
        const deeper = search(colBlocks);
        if (deeper) return deeper;
      }
    }
    return null;
  };

  return search(blocks);
};

export const sameContainer = (a: Container | null, b: Container | null): boolean => {
  if (!a || !b || a.kind !== b.kind) return false;
  if (a.kind === 'root') return true;
  const bb = b as Extract<Container, { kind: 'column' }>;
  return a.columnsBlockId === bb.columnsBlockId && a.columnId === bb.columnId;
};

/** Moves an item within an array, returning a new array. */
export const moveItem = <T>(items: T[], from: number, to: number): T[] => {
  if (from === to) return items;
  if (from < 0 || from >= items.length) return items;
  if (to < 0 || to >= items.length) return items;

  const next = [...items];
  const [moved] = next.splice(from, 1);
  next.splice(to, 0, moved);
  return next;
};

/** Reorders the top-level blocks. */
export const reorderTopLevel = (blocks: Block[], activeId: string, overId: string): Block[] => {
  const from = blocks.findIndex((b) => b.id === activeId);
  const to = blocks.findIndex((b) => b.id === overId);
  if (from === -1 || to === -1) return blocks;
  return moveItem(blocks, from, to);
};

/**
 * Reorders blocks inside one column of one columns-block.
 *
 * Everything not on the path is returned by identity, so React only re-renders
 * the branch that actually changed.
 */
export const reorderWithinColumn = (
  blocks: Block[],
  columnsBlockId: string,
  columnId: string,
  activeId: string,
  overId: string,
): Block[] =>
  blocks.map((b) => {
    if (b.type !== 'columns' || !b.content.columns) return b;

    if (b.id !== columnsBlockId) {
      // A columns block can contain another columns block, so keep descending.
      return {
        ...b,
        content: {
          ...b.content,
          columns: b.content.columns.map((col: any) => ({
            ...col,
            blocks: reorderWithinColumn(col.blocks || [], columnsBlockId, columnId, activeId, overId),
          })),
        },
      };
    }

    return {
      ...b,
      content: {
        ...b.content,
        columns: b.content.columns.map((col: any) => {
          if (col.id !== columnId) return col;
          const items: Block[] = col.blocks || [];
          const from = items.findIndex((x) => x.id === activeId);
          const to = items.findIndex((x) => x.id === overId);
          if (from === -1 || to === -1) return col;
          return { ...col, blocks: moveItem(items, from, to) };
        }),
      },
    };
  });
