import { api } from './client';
import type { Food } from '@/types';

/** Catalog search: global foods plus the account's own, best match first. */
export function searchFoods(q: string, signal?: AbortSignal) {
  return api<{ ok: boolean; foods: Food[] }>(`/api/foods/search?q=${encodeURIComponent(q)}`, { signal });
}
