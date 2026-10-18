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
  note?: string;
  error?: string;
}

export function lookupBarcode(code: string) {
  return api<BarcodeResult>(`/api/barcode/${code}`);
}
