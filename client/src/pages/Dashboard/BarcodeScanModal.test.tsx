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
  const view = render(<BarcodeScanModal isOpen onClose={onClose} onPick={onPick} pickLabel="Add" />);
  const setOpen = (isOpen: boolean) =>
    view.rerender(<BarcodeScanModal isOpen={isOpen} onClose={onClose} onPick={onPick} pickLabel="Add" />);
  return { onPick, onClose, setOpen };
}

/** A scan whose product has this serving; the serving button is named "portion". */
function mockServing(servingQuantity: number | null) {
  vi.mocked(lookupBarcode).mockResolvedValue({
    ok: true, name: skyr.name, caloriesPer100g: 63, food: skyr, servingSize: 'portion', servingQuantity,
  });
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

    // Protein 11 g/100 g × 150 g = 16.5, shown rounded like the saved component row.
    expect(screen.getByText('17g')).toBeInTheDocument();
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

  it('drops a lookup that lands after the modal was closed and reopened', async () => {
    let release: (v: Awaited<ReturnType<typeof lookupBarcode>>) => void = () => {};
    vi.mocked(lookupBarcode).mockImplementation(() => new Promise((resolve) => { release = resolve; }));
    const { setOpen } = renderModal();
    await scan();
    await screen.findByText(/Looking up/);

    setOpen(false);
    setOpen(true);
    release({ ok: true, name: skyr.name, caloriesPer100g: 63, food: skyr, servingSize: '150 g', servingQuantity: 150 });
    await new Promise((r) => setTimeout(r, 20));

    expect(screen.queryByTestId('barcode-product-name')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Manual' })).toBeInTheDocument();
  });

  it('drops a failing lookup that lands after the modal was closed', async () => {
    let fail: (e: Error) => void = () => {};
    vi.mocked(lookupBarcode).mockImplementation(() => new Promise((_, reject) => { fail = reject; }));
    const { setOpen } = renderModal();
    await scan();
    await screen.findByText(/Looking up/);

    setOpen(false);
    setOpen(true);
    fail(new Error('late'));
    await new Promise((r) => setTimeout(r, 20));

    expect(screen.queryByText('Lookup failed. Please try again.')).not.toBeInTheDocument();
  });

  describe('starting grams', () => {
    it.each([
      ['a serving under the smallest amount', 0.005],
      ['a serving over the largest amount', 25000],
      ['no serving', null],
    ])('falls back to 100 g for %s', async (_name, servingQuantity) => {
      mockServing(servingQuantity);
      renderModal();
      await scan();

      expect(await screen.findByTestId('barcode-grams')).toHaveValue('100');
      expect(screen.getByTestId('barcode-pick')).toBeEnabled();
      // Only the 100 g button: an unusable serving is not offered as a preset.
      expect(screen.queryByRole('button', { name: 'portion' })).not.toBeInTheDocument();
      expect(screen.getByRole('button', { name: '100g' })).toHaveClass('border-primary');
    });

    it('rounds a long serving to hundredths and highlights its button by that value', async () => {
      mockServing(33.333333);
      const { onPick } = renderModal();
      await scan();

      expect(await screen.findByTestId('barcode-grams')).toHaveValue('33.33');
      expect(screen.getByRole('button', { name: 'portion' })).toHaveClass('border-primary');
      expect(screen.getByRole('button', { name: '100g' })).not.toHaveClass('border-primary');

      await userEvent.click(screen.getByTestId('barcode-pick'));
      expect(onPick).toHaveBeenCalledWith(skyr, 33.33);
    });

    it('switches between the serving and 100 g', async () => {
      renderModal();
      await scan();
      expect(await screen.findByTestId('barcode-grams')).toHaveValue('150');

      await userEvent.click(screen.getByRole('button', { name: '100g' }));
      expect(screen.getByTestId('barcode-grams')).toHaveValue('100');
      await userEvent.click(screen.getByRole('button', { name: '150 g' }));
      expect(screen.getByTestId('barcode-grams')).toHaveValue('150');
    });
  });

  it('says so when the product comes from the catalog because Open Food Facts is down', async () => {
    vi.mocked(lookupBarcode).mockResolvedValue({
      ok: true, name: skyr.name, caloriesPer100g: 63, food: skyr, servingSize: null, servingQuantity: null,
      cached: true, note: 'Open Food Facts is unreachable; showing the stored values.',
    });
    renderModal();
    await scan();

    expect(await screen.findByTestId('barcode-cached-note')).toHaveTextContent('Open Food Facts is unreachable; showing the stored values.');
    expect(screen.getByTestId('barcode-pick')).toBeEnabled();
  });

  it('shows no note for a live lookup', async () => {
    renderModal();
    await scan();
    await screen.findByTestId('barcode-product-name');
    expect(screen.queryByTestId('barcode-cached-note')).not.toBeInTheDocument();
  });

  it('shows the server\'s reason when the calorie value is implausible', async () => {
    const note = 'Open Food Facts reports an implausible calorie value (5000 kcal per 100 g); it was not stored.';
    vi.mocked(lookupBarcode).mockResolvedValue({ ok: true, name: 'Odd', caloriesPer100g: null, note });
    renderModal();
    await scan();

    expect(await screen.findByText(note)).toBeInTheDocument();
    expect(screen.queryByTestId('barcode-pick')).not.toBeInTheDocument();
  });
});
