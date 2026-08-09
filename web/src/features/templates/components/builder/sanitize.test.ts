import { test, expect, describe } from 'bun:test';
import { sanitizeHtml } from './sanitize';

/**
 * The threat model here is not the recipient — mail clients strip scripts.
 * It is the next person to open the template. Templates are tenant-scoped and
 * an Editor can create them, so markup that survives this function runs in the
 * session of whichever Administrator opens that template next.
 */

const clean = (s: string) => sanitizeHtml(s).toLowerCase();

describe('script execution is removed', () => {
  test('script tags do not survive', () => {
    expect(clean('<script>alert(1)</script>')).not.toContain('<script');
    expect(clean('<p>ok</p><script>alert(1)</script>')).toContain('<p>ok</p>');
  });

  test('inline event handlers are stripped', () => {
    for (const dirty of [
      '<img src=x onerror=alert(1)>',
      '<div onclick="alert(1)">x</div>',
      '<body onload=alert(1)>',
      '<p onmouseover="alert(1)">hover</p>',
      '<svg onload=alert(1)>',
    ]) {
      const out = clean(dirty);
      expect(out).not.toContain('onerror');
      expect(out).not.toContain('onclick');
      expect(out).not.toContain('onload');
      expect(out).not.toContain('onmouseover');
    }
  });

  test('javascript: urls are rejected in href and src', () => {
    expect(clean('<a href="javascript:alert(1)">x</a>')).not.toContain('javascript:');
    expect(clean('<img src="javascript:alert(1)">')).not.toContain('javascript:');
    // Entity and whitespace obfuscation is the parser's problem, not a regex's,
    // which is the whole reason for using a DOM-based sanitiser.
    expect(clean('<a href="java&#115;cript:alert(1)">x</a>')).not.toContain('alert');
    expect(clean('<a href=" \tjavascript:alert(1)">x</a>')).not.toContain('javascript:');
  });

  test('embedding elements are removed', () => {
    for (const dirty of [
      '<iframe src="https://evil.test"></iframe>',
      '<object data="x"></object>',
      '<embed src="x">',
      '<form action="https://evil.test"><input name="a"></form>',
    ]) {
      const out = clean(dirty);
      expect(out).not.toContain('<iframe');
      expect(out).not.toContain('<object');
      expect(out).not.toContain('<embed');
      expect(out).not.toContain('<form');
      expect(out).not.toContain('<input');
    }
  });

  test('style elements are removed', () => {
    expect(clean('<style>body{background:url(javascript:alert(1))}</style>')).not.toContain('<style');
  });
});

describe('legitimate email markup survives', () => {
  test('formatting and structure are kept', () => {
    const out = sanitizeHtml(
      '<p>Hello <b>world</b> and <i>others</i></p><ul><li>one</li></ul><h2>Title</h2>',
    );
    expect(out).toContain('<b>world</b>');
    expect(out).toContain('<i>others</i>');
    expect(out).toContain('<li>one</li>');
    expect(out).toContain('<h2>Title</h2>');
  });

  test('links, images and inline styles are kept', () => {
    const out = sanitizeHtml(
      '<a href="https://x.test" target="_blank" style="color:red">go</a>' +
        '<img src="https://x.test/a.png" alt="a" width="100">',
    );
    expect(out).toContain('href="https://x.test"');
    expect(out).toContain('target="_blank"');
    expect(out).toContain('style="color:red"');
    expect(out).toContain('src="https://x.test/a.png"');
    expect(out).toContain('alt="a"');
  });

  test('tables survive, since email layout is tables', () => {
    const out = sanitizeHtml(
      '<table cellpadding="0"><tbody><tr><td align="center">cell</td></tr></tbody></table>',
    );
    expect(out).toContain('<table');
    expect(out).toContain('<td align="center">cell</td>');
  });

  // A template author must never lose personalisation by being sanitised.
  test('merge tags pass through untouched', () => {
    expect(sanitizeHtml('Hi {{name}}, order {{order_id}}')).toContain('Hi {{name}}, order {{order_id}}');
    expect(sanitizeHtml('<p>{{#if vip}}VIP{{/if}}</p>')).toContain('{{#if vip}}');
    expect(sanitizeHtml('<a href="{{unsubscribe_url}}">Unsubscribe</a>')).toContain('{{unsubscribe_url}}');
  });

  test('empty and non-string input is handled', () => {
    expect(sanitizeHtml('')).toBe('');
    expect(sanitizeHtml(undefined)).toBe('');
    expect(sanitizeHtml(null)).toBe('');
  });
});
