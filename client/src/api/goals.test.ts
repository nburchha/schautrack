import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { getRecentGoals, resetDayGoal, setDayGoal } from './goals';

/**
 * These assert the wire shape of the three goal calls: method, path and body.
 * The server gap-fills any key that is not sent, so a stray key is a silent
 * overwrite of the Basisziel value rather than an error.
 */

interface Sent { url: string; method: string; body: string | undefined }

function stubFetch(response: unknown): Sent[] {
  const sent: Sent[] = [];
  vi.stubGlobal(
    'fetch',
    // eslint-disable-next-line @typescript-eslint/require-await -- async without await is the plainest way for this mock to satisfy fetch's Promise<Response> contract
    vi.fn(async (url: string, init?: RequestInit) => {
      if (url === '/api/csrf') {
        return new Response(JSON.stringify({ token: 'test-csrf' }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        });
      }
      sent.push({ url, method: init?.method ?? 'GET', body: init?.body as string | undefined });
      return new Response(JSON.stringify(response), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      });
    }),
  );
  return sent;
}

describe('goals api', () => {
  let sent: Sent[];
  beforeEach(() => {
    sent = stubFetch({ ok: true, goal: {}, recent: [] });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('PUTs exactly the given keys to the day', async () => {
    await setDayGoal('2026-08-08', { calories: 3100, carbs: 480, reason: 'Lauf 25 km' });
    expect(sent).toHaveLength(1);
    expect(sent[0].url).toBe('/api/goals/2026-08-08');
    expect(sent[0].method).toBe('PUT');
    expect(JSON.parse(sent[0].body ?? '{}')).toEqual({ calories: 3100, carbs: 480, reason: 'Lauf 25 km' });
  });

  it('POSTs the reset without a body', async () => {
    await resetDayGoal('2026-08-08');
    expect(sent[0].url).toBe('/api/goals/2026-08-08/reset');
    expect(sent[0].method).toBe('POST');
    expect(sent[0].body).toBeUndefined();
  });

  it('GETs the recent adjustments', async () => {
    await getRecentGoals();
    expect(sent[0].url).toBe('/api/goals/recent');
    expect(sent[0].method).toBe('GET');
  });
});
