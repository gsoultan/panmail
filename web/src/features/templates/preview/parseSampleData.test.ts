import { describe, expect, test } from 'bun:test';
import { parseSampleData } from './parseSampleData';

// It had no test where it used to live, so moving it would otherwise have been
// checked by the type checker alone.
describe('parseSampleData', () => {
  test('an object is the sample data', () => {
    expect(parseSampleData('{"name":"Alice","vip":true}')).toEqual({ name: 'Alice', vip: true });
  });

  // An empty panel means "use the placeholders", not an error.
  test('blank text is no data', () => {
    expect(parseSampleData('')).toBeUndefined();
    expect(parseSampleData('   \n  ')).toBeUndefined();
  });

  // The preview re-renders on every keystroke, so a half-typed draft must fall
  // back to placeholders rather than blank the preview or throw.
  test('an unfinished draft falls back rather than throwing', () => {
    expect(parseSampleData('{"name": "Ali')).toBeUndefined();
  });

  // Template data is keyed by name; a top-level array or scalar has no names
  // for a template to reference.
  test('only an object counts as sample data', () => {
    expect(parseSampleData('[1, 2, 3]')).toBeUndefined();
    expect(parseSampleData('"just a string"')).toBeUndefined();
    expect(parseSampleData('42')).toBeUndefined();
    expect(parseSampleData('null')).toBeUndefined();
  });
});
