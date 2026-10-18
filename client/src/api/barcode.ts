import { api } from './client';
import type { Food } from '@/types';

export interface BarcodeResult {
  ok: boolean;
  name?: string;
  caloriesPer100g?: number | null;
  macrosPer100g?: Record<string, number>;
  servingSize?: string | null;
  servingQuantity?: number | null;
  /** The product as a catalog food. Present when it has calories. */
  food?: Food;
  /** True when Open Food Facts was unreachable and the stored product is shown. */
  cached?: boolean;
  note?: string;
  error?: string;
}

/** A POST because a lookup writes the product to the shared catalog. */
export function lookupBarcode(code: string) {
  return api<BarcodeResult>(`/api/barcode/${code}`, { method: 'POST' });
}
