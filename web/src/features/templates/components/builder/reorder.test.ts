import { test, expect, describe } from 'bun:test';
import { moveItem, reorderTopLevel, reorderWithinColumn, findContainer, sameContainer } from './reorder';
import type { Block } from './types';

const b = (id: string): Block => ({ id, type: 'text', content: { text: id }, style: {} });

const columns = (id: string, cols: { id: string; blocks: Block[] }[]): Block => ({
  id,
  type: 'columns',
  content: { columns: cols.map((c) => ({ ...c, width: '50%', style: {} })) },
  style: {},
});

const ids = (blocks: Block[]) => blocks.map((x) => x.id);

describe('moveItem', () => {
  test('moves forwards and backwards', () => {
    expect(moveItem(['a', 'b', 'c', 'd'], 0, 2)).toEqual(['b', 'c', 'a', 'd']);
    expect(moveItem(['a', 'b', 'c', 'd'], 3, 1)).toEqual(['a', 'd', 'b', 'c']);
  });

  test('a move onto itself is a no-op and keeps the same array', () => {
    const items = ['a', 'b'];
    expect(moveItem(items, 1, 1)).toBe(items);
  });

  test('out-of-range indices are refused rather than corrupting the list', () => {
    const items = ['a', 'b'];
    expect(moveItem(items, -1, 0)).toBe(items);
    expect(moveItem(items, 0, 5)).toBe(items);
    expect(moveItem(items, 9, 0)).toBe(items);
  });

  test('the input array is not mutated', () => {
    const items = ['a', 'b', 'c'];
    moveItem(items, 0, 2);
    expect(items).toEqual(['a', 'b', 'c']);
  });
});

describe('reorderTopLevel', () => {
  test('reorders by id', () => {
    const blocks = [b('a'), b('b'), b('c')];
    expect(ids(reorderTopLevel(blocks, 'a', 'c'))).toEqual(['b', 'c', 'a']);
  });

  test('an unknown id leaves the list alone', () => {
    const blocks = [b('a'), b('b')];
    expect(reorderTopLevel(blocks, 'a', 'nope')).toBe(blocks);
    expect(reorderTopLevel(blocks, 'nope', 'b')).toBe(blocks);
  });
});

describe('reorderWithinColumn', () => {
  // Nested blocks could not be reordered at all before this existed.
  test('reorders inside the named column', () => {
    const blocks = [columns('cols', [{ id: 'c1', blocks: [b('x'), b('y'), b('z')] }])];
    const out = reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'z');
    expect(ids((out[0] as any).content.columns[0].blocks)).toEqual(['y', 'z', 'x']);
  });

  test('the sibling column is untouched and keeps its identity', () => {
    const blocks = [
      columns('cols', [
        { id: 'c1', blocks: [b('x'), b('y')] },
        { id: 'c2', blocks: [b('p'), b('q')] },
      ]),
    ];
    const before = (blocks[0] as any).content.columns[1];
    const out = reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'y');
    const after = (out[0] as any).content.columns[1];

    expect(ids(after.blocks)).toEqual(['p', 'q']);
    expect(after).toBe(before);
  });

  test('a block outside the columns block is returned by identity', () => {
    const plain = b('top');
    const blocks = [plain, columns('cols', [{ id: 'c1', blocks: [b('x'), b('y')] }])];
    const out = reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'y');
    expect(out[0]).toBe(plain);
  });

  test('reaches a column nested inside another columns block', () => {
    const inner = columns('inner', [{ id: 'i1', blocks: [b('x'), b('y'), b('z')] }]);
    const outer = columns('outer', [{ id: 'o1', blocks: [inner] }]);
    const out = reorderWithinColumn([outer], 'inner', 'i1', 'z', 'x');

    const nested = (out[0] as any).content.columns[0].blocks[0];
    expect(ids(nested.content.columns[0].blocks)).toEqual(['z', 'x', 'y']);
  });

  test('an unknown column or id changes nothing', () => {
    const blocks = [columns('cols', [{ id: 'c1', blocks: [b('x'), b('y')] }])];
    const sameColumn = reorderWithinColumn(blocks, 'cols', 'nope', 'x', 'y');
    expect(ids((sameColumn[0] as any).content.columns[0].blocks)).toEqual(['x', 'y']);

    const sameId = reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'nope');
    expect(ids((sameId[0] as any).content.columns[0].blocks)).toEqual(['x', 'y']);
  });

  test('the original tree is not mutated', () => {
    const blocks = [columns('cols', [{ id: 'c1', blocks: [b('x'), b('y'), b('z')] }])];
    reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'z');
    expect(ids((blocks[0] as any).content.columns[0].blocks)).toEqual(['x', 'y', 'z']);
  });

  test('a column with no blocks array does not throw', () => {
    const blocks = [{ id: 'cols', type: 'columns', content: { columns: [{ id: 'c1' }] }, style: {} } as any];
    expect(() => reorderWithinColumn(blocks, 'cols', 'c1', 'x', 'y')).not.toThrow();
  });
});

describe('findContainer', () => {
  test('identifies a top-level block', () => {
    const blocks = [b('a'), columns('cols', [{ id: 'c1', blocks: [b('x')] }])];
    expect(findContainer(blocks, 'a')).toEqual({ kind: 'root' });
    expect(findContainer(blocks, 'cols')).toEqual({ kind: 'root' });
  });

  test('identifies the column a nested block sits in', () => {
    const blocks = [
      columns('cols', [
        { id: 'c1', blocks: [b('x')] },
        { id: 'c2', blocks: [b('y')] },
      ]),
    ];
    expect(findContainer(blocks, 'y')).toEqual({
      kind: 'column',
      columnsBlockId: 'cols',
      columnId: 'c2',
    });
  });

  test('reaches a column nested inside another columns block', () => {
    const inner = columns('inner', [{ id: 'i1', blocks: [b('deep')] }]);
    const outer = columns('outer', [{ id: 'o1', blocks: [inner] }]);
    expect(findContainer([outer], 'deep')).toEqual({
      kind: 'column',
      columnsBlockId: 'inner',
      columnId: 'i1',
    });
  });

  test('an unknown id has no container', () => {
    expect(findContainer([b('a')], 'nope')).toBeNull();
  });
});

describe('sameContainer', () => {
  // A drag that crosses lists must be refused rather than silently reordering
  // the wrong one — the containers differ, so the indices mean nothing.
  test('two different columns are not the same container', () => {
    const a = { kind: 'column', columnsBlockId: 'cols', columnId: 'c1' } as const;
    const b2 = { kind: 'column', columnsBlockId: 'cols', columnId: 'c2' } as const;
    expect(sameContainer(a, b2)).toBe(false);
  });

  test('the same column matches', () => {
    const a = { kind: 'column', columnsBlockId: 'cols', columnId: 'c1' } as const;
    expect(sameContainer(a, { ...a })).toBe(true);
  });

  test('root matches root, and never a column', () => {
    expect(sameContainer({ kind: 'root' }, { kind: 'root' })).toBe(true);
    expect(sameContainer({ kind: 'root' }, { kind: 'column', columnsBlockId: 'x', columnId: 'y' })).toBe(false);
  });

  test('a null container never matches', () => {
    expect(sameContainer(null, { kind: 'root' })).toBe(false);
    expect(sameContainer({ kind: 'root' }, null)).toBe(false);
  });
});
