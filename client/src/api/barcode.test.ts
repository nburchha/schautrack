import { describe, it, expect, vi, afterEach } from 'vitest';
import { lookupBarcode } from './barcode';

describe('lookupBarcode', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  // The lookup stores the product in the shared catalog, so the server takes it
  // as a CSRF-protected POST; a GET would be refused.
  it('POSTs with the CSRF token', async () => {
    const calls: { url: string; init?: RequestInit }[] = [];
    vi.stubGlobal(
      'fetch',
      // eslint-disable-next-line @typescript-eslint/require-await -- async without await is the plainest way for this mock to satisfy fetch's Promise<Response> contract
      vi.fn(async (url: string, init?: RequestInit) => {
        calls.push({ url, init });
        const body = url === '/api/csrf' ? { token: 'test-csrf' } : { ok: true };
        return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }),
    );

    await expect(lookupBarcode('4006381333931')).resolves.toEqual({ ok: true });

    const call = calls.find((c) => c.url === '/api/barcode/4006381333931');
    expect(call?.init?.method).toBe('POST');
    expect(new Headers(call?.init?.headers).get('X-CSRF-Token')).toBe('test-csrf');
  });
});
