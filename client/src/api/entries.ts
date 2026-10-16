import { api } from './client';
import type { DashboardData, Entry, EntryDetail } from '@/types';

export function getDashboard(params: { range?: number; start?: string; end?: string; day?: string }) {
  const qs = new URLSearchParams();
  if (params.range) qs.set('range', String(params.range));
  if (params.start) qs.set('start', params.start);
  if (params.end) qs.set('end', params.end);
  if (params.day) qs.set('day', params.day);
  return api<DashboardData>(`/api/dashboard?${qs}`);
}

export function getDayEntries(userId: number, date: string) {
  return api<{ ok: boolean; date: string; entries: Entry[] }>(`/entries/day?date=${date}&user=${userId}`, {
    headers: { Accept: 'application/json' },
  });
}

/** One component of a new entry: a catalog food, or an ad-hoc food with its values per 100 g. */
export type NewComponent =
  | { food_id: number; grams: number }
  | {
      name: string;
      grams: number;
      calories_per_100g: number;
      protein_g?: number | null;
      carbs_g?: number | null;
      fat_g?: number | null;
      fiber_g?: number | null;
      sugar_g?: number | null;
    };

export function createEntry(data: {
  amount?: number;
  entry_name?: string;
  entry_date?: string;
  /** When it was eaten (RFC 3339 with offset); the day follows it. */
  eaten_at?: string;
  /** Replaces amount and macros: the entry's sum is computed from these. */
  components?: NewComponent[];
  weight?: number;
  protein_g?: number;
  carbs_g?: number;
  fat_g?: number;
  fiber_g?: number;
  sugar_g?: number;
}) {
  return api<{ ok: boolean; id?: number }>('/entries', {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export function updateEntry(id: number, data: Record<string, unknown>) {
  return api<{ ok: boolean; entry?: Entry }>(`/entries/${id}/update`, {
    method: 'POST',
    body: JSON.stringify(data),
  });
}

export function deleteEntry(id: number) {
  return api<{ ok: boolean }>(`/entries/${id}/delete`, { method: 'POST' });
}

export interface EntryDetailResponse {
  ok: boolean;
  entry: EntryDetail;
  enabledMacros: string[];
  caloriesEnabled: boolean;
}

/** One entry with its components. userId reads a linked account's entry. */
export function getEntry(id: number, userId?: number) {
  const qs = userId ? `?user=${userId}` : '';
  return api<EntryDetailResponse>(`/api/entries/${id}${qs}`);
}

export function updateComponentGrams(entryId: number, componentId: number, grams: number) {
  return api<EntryDetailResponse>(`/api/entries/${entryId}/components/${componentId}/update`, {
    method: 'POST',
    body: JSON.stringify({ grams }),
  });
}

export function deleteComponent(entryId: number, componentId: number) {
  return api<EntryDetailResponse>(`/api/entries/${entryId}/components/${componentId}/delete`, { method: 'POST' });
}

/** Adds a catalog food, in grams, to an entry. The entry's sum becomes the sum of its components. */
export function addComponent(entryId: number, input: { food_id: number; grams: number }) {
  return api<EntryDetailResponse>(`/api/entries/${entryId}/components`, {
    method: 'POST',
    body: JSON.stringify(input),
  });
}

/**
 * Moves an entry to the moment it was eaten (RFC 3339 with offset), which also
 * sets its day. null falls back to the time the entry was created.
 */
export function setEatenAt(entryId: number, eatenAt: string | null) {
  return updateEntry(entryId, { eaten_at: eatenAt });
}
