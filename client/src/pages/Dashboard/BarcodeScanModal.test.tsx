import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Food, User } from '@/types';
import { useAuthStore } from '@/stores/authStore';
import BarcodeScanModal from './BarcodeScanModal';

// The decoder needs a camera and canvas; every test here uses the manual tab.
vi.mock('@ericblade/quagga2', () => ({
  default: { init: vi.fn(), start: vi.fn(), stop: vi.fn(), onDetected: vi.fn(), offDetected: vi.fn(), decodeSingle: vi.fn() },
}));
vi.mock('@/api/barcode', () => ({ lookupBarcode: vi.fn() }));
import { lookupBarcode } from '@/api/barcode';

// Fiber is unknown: OFF is crowdsourced and often lacks a figure.
const skyr: Food = {
  id: 9, source: 'off', source_code: '4311501062449', name: 'Skyr Natur (Arla)', source_classification: null,
  calories_per_100g: 63, macros_per_100g: { protein_g: 11, carbs_g: 4, fat_g: 0.2, fiber_g: null, sugar_g: 4 },
};

function renderModal(onPick = vi.fn().mockResolvedValue(undefined)) {
  const onClose = vi.fn();
  render(<BarcodeScanModal isOpen onClose={onClose} onPick={onPick} pickLabel="Add" />);
  return { onPick, onClose };
}

async function scan() {
  await userEvent.click(screen.getByRole('button', { name: 'Manual' }));
  await userEvent.type(screen.getByPlaceholderText('Enter barcode number'), '4311501062449');
  await userEvent.click(screen.getByRole('button', { name: 'Look up' }));
}

describe('BarcodeScanModal', () => {
  beforeEach(() => {
    vi.mocked(lookupBarcode).mockReset();
    vi.mocked(lookupBarcode).mockResolvedValue({ ok: true, name: skyr.name, caloriesPer100g: 63, food: skyr, servingSize: '150 g', servingQuantity: 150 });
    useAuthStore.setState({ user: { id: 1, macrosEnabled: { protein: true, fiber: true } } as unknown as User });
  });

  it('hands the scanned catalog food and the grams to the caller, not a pre-filled entry', async () => {
    const { onPick } = renderModal();
    await scan();

    expect(await screen.findByTestId('barcode-product-name')).toHaveTextContent('Skyr Natur (Arla)');
    // The serving size is the starting amount.
    expect(screen.getByTestId('barcode-grams')).toHaveValue('150');

    await userEvent.clear(screen.getByTestId('barcode-grams'));
    await userEvent.type(screen.getByTestId('barcode-grams'), '2*45');
    expect(screen.getByTestId('barcode-kcal')).toHaveTextContent('57 cal');
    await userEvent.click(screen.getByTestId('barcode-pick'));

    expect(onPick).toHaveBeenCalledWith(skyr, 90);
  });

  it('shows an unknown figure as a dash, not as 0 g', async () => {
    renderModal();
    await scan();
    await screen.findByTestId('barcode-product-name');

    // Protein 11 g/100 g × 150 g, fiber unknown.
    expect(screen.getByText('16.5g')).toBeInTheDocument();
    expect(screen.getByText('–')).toBeInTheDocument();
  });

  it('refuses invalid grams', async () => {
    const { onPick } = renderModal();
    await scan();
    await userEvent.clear(await screen.findByTestId('barcode-grams'));
    await userEvent.type(screen.getByTestId('barcode-grams'), '0');

    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByTestId('barcode-pick')).toBeDisabled();
    expect(onPick).not.toHaveBeenCalled();
  });

  it('stays on the product when picking fails, so a retry is one tap', async () => {
    const onPick = vi.fn().mockRejectedValueOnce(new Error('boom')).mockResolvedValue(undefined);
    renderModal(onPick);
    await scan();
    await userEvent.click(await screen.findByTestId('barcode-pick'));

    await waitFor(() => expect(onPick).toHaveBeenCalledTimes(1));
    expect(screen.getByTestId('barcode-product-name')).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('barcode-pick'));
    await waitFor(() => expect(onPick).toHaveBeenCalledTimes(2));
  });

  it('reports a product without calories instead of offering it', async () => {
    vi.mocked(lookupBarcode).mockResolvedValue({ ok: true, name: 'Mystery', caloriesPer100g: null, note: 'No calorie data available for this product.' });
    renderModal();
    await scan();

    expect(await screen.findByText('No calorie data available for this product.')).toBeInTheDocument();
    expect(screen.queryByTestId('barcode-pick')).not.toBeInTheDocument();
  });
});
