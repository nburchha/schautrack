import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Food } from '@/types';
import { FoodSearch } from './FoodSearch';

vi.mock('@/api/foods', () => ({ searchFoods: vi.fn() }));
import { searchFoods } from '@/api/foods';

// The scanner itself is covered by BarcodeScanModal.test; here it only has to
// be wired to the same onPick as a search hit. Like the real one, it swallows
// a rejection (the caller reports it) and stays open.
vi.mock('@/pages/Dashboard/BarcodeScanModal', () => ({
  default: ({ isOpen, onPick, pickLabel }: { isOpen: boolean; onPick: (f: Food, g: number) => Promise<void> | void; pickLabel: string }) =>
    isOpen ? <button type="button" data-testid="mock-scanner" onClick={() => void Promise.resolve(onPick(scanned, 60)).catch(() => {})}>{pickLabel}</button> : null,
}));

const macros = { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 };
const oats: Food = { id: 1, source: 'bls', source_code: 'C1', name: 'Hafer Flocken', source_classification: 'C', calories_per_100g: 372, macros_per_100g: macros };
const noKcal: Food = { id: 2, source: 'off', source_code: '4001', name: 'Mystery Bar', source_classification: null, calories_per_100g: null, macros_per_100g: macros };

const scanned: Food = { id: 3, source: 'off', source_code: '4311501062449', name: 'Skyr', source_classification: null, calories_per_100g: 63, macros_per_100g: macros };

function renderSearch(onPick = vi.fn().mockResolvedValue(undefined), barcode = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <FoodSearch onPick={onPick} pickLabel="Add" barcode={barcode} />
    </QueryClientProvider>,
  );
  return onPick;
}

describe('FoodSearch', () => {
  beforeEach(() => vi.mocked(searchFoods).mockReset());

  it('waits for two characters before searching', async () => {
    renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'h');

    expect(screen.getByTestId('food-search-hint')).toBeInTheDocument();
    await new Promise((r) => setTimeout(r, 350));
    expect(searchFoods).not.toHaveBeenCalled();
  });

  it('searches the debounced term, not every keystroke, and lists the hits', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [oats] });
    renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'Haferflocken');

    expect(await screen.findByText('Hafer Flocken')).toBeInTheDocument();
    expect(searchFoods).toHaveBeenCalledTimes(1);
    expect(vi.mocked(searchFoods).mock.calls[0][0]).toBe('Haferflocken');
  });

  it('keeps the previous hits on screen while the next search runs', async () => {
    let release: (v: { ok: boolean; foods: Food[] }) => void = () => {};
    vi.mocked(searchFoods)
      .mockResolvedValueOnce({ ok: true, foods: [oats] })
      .mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
    renderSearch();
    const input = screen.getByTestId('food-search-input');
    await userEvent.type(input, 'Haf');
    await screen.findByText('Hafer Flocken');

    await userEvent.type(input, 'er');
    await waitFor(() => expect(searchFoods).toHaveBeenCalledTimes(2));
    expect(screen.getByText('Hafer Flocken')).toBeInTheDocument();
    expect(screen.getByTestId('food-search-results')).toHaveAttribute('aria-busy', 'true');
    release({ ok: true, foods: [oats] });
  });

  it('does not bring an earlier search\'s hits back under a new one', async () => {
    vi.mocked(searchFoods)
      .mockResolvedValueOnce({ ok: true, foods: [oats] })
      .mockImplementationOnce(() => new Promise(() => {})); // the second search never answers
    renderSearch();
    const input = screen.getByTestId('food-search-input');
    await userEvent.type(input, 'Haf');
    await screen.findByText('Hafer Flocken');

    await userEvent.clear(input);
    await waitFor(() => expect(screen.queryByText('Hafer Flocken')).not.toBeInTheDocument());

    await userEvent.type(input, 'Mil');
    await waitFor(() => expect(searchFoods).toHaveBeenCalledTimes(2));
    expect(screen.queryByText('Hafer Flocken')).not.toBeInTheDocument();
  });

  it('says so when nothing matches', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [] });
    renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'zzzz');

    expect(await screen.findByTestId('food-search-empty')).toHaveTextContent('zzzz');
  });

  it('shows a retry when the search fails', async () => {
    vi.mocked(searchFoods).mockRejectedValueOnce(new Error('boom')).mockResolvedValue({ ok: true, foods: [oats] });
    renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'Haf');

    const alert = await screen.findByTestId('food-search-error');
    await userEvent.click(alert.querySelector('button')!);
    expect(await screen.findByText('Hafer Flocken')).toBeInTheDocument();
  });

  it('disables foods without a calorie value', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [noKcal] });
    renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'Mys');

    expect(await screen.findByTestId('food-search-result')).toBeDisabled();
  });

  it('picks a food with grams, math allowed, and resets afterwards', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [oats] });
    const onPick = renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'Haf');
    await userEvent.click(await screen.findByTestId('food-search-result'));

    // Defaults to 100 g and previews the portion's kcal.
    expect(screen.getByTestId('food-search-grams')).toHaveValue('100');
    expect(screen.getByTestId('food-search-kcal')).toHaveTextContent('372');

    const grams = screen.getByTestId('food-search-grams');
    await userEvent.clear(grams);
    await userEvent.type(grams, '2*45');
    expect(screen.getByTestId('food-search-kcal')).toHaveTextContent('335');
    await userEvent.click(screen.getByTestId('food-search-add'));

    await waitFor(() => expect(onPick).toHaveBeenCalledWith(oats, 90));
    expect(await screen.findByTestId('food-search-input')).toHaveValue('');
  });

  it('refuses grams the server would reject', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [oats] });
    const onPick = renderSearch();
    await userEvent.type(screen.getByTestId('food-search-input'), 'Haf');
    await userEvent.click(await screen.findByTestId('food-search-result'));

    for (const bad of ['0', '-5', '20000', 'abc']) {
      const grams = screen.getByTestId('food-search-grams');
      await userEvent.clear(grams);
      await userEvent.type(grams, bad);
      expect(screen.getByTestId('food-search-add')).toBeDisabled();
    }
    expect(onPick).not.toHaveBeenCalled();
  });

  it('keeps the selection when adding fails, so a retry is one tap', async () => {
    vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [oats] });
    const onPick = renderSearch(vi.fn().mockRejectedValue(new Error('nope')));
    await userEvent.type(screen.getByTestId('food-search-input'), 'Haf');
    await userEvent.click(await screen.findByTestId('food-search-result'));
    await userEvent.click(screen.getByTestId('food-search-add'));

    await waitFor(() => expect(onPick).toHaveBeenCalled());
    expect(screen.getByTestId('food-search-selected')).toBeInTheDocument();
  });

  it('offers the scanner only when barcode lookup is on', () => {
    renderSearch();
    expect(screen.queryByTestId('food-search-scan')).not.toBeInTheDocument();
  });

  it('picks a scanned product like a search hit, and closes the scanner once it is added', async () => {
    const onPick = renderSearch(undefined, true);
    await userEvent.click(screen.getByTestId('food-search-scan'));
    await userEvent.click(await screen.findByTestId('mock-scanner'));

    expect(onPick).toHaveBeenCalledWith(scanned, 60);
    await waitFor(() => expect(screen.queryByTestId('mock-scanner')).not.toBeInTheDocument());
  });

  it('keeps the scanner open when the pick fails', async () => {
    const failing = vi.fn().mockRejectedValue(new Error('boom'));
    renderSearch(failing, true);
    await userEvent.click(screen.getByTestId('food-search-scan'));
    await userEvent.click(await screen.findByTestId('mock-scanner'));

    await waitFor(() => expect(failing).toHaveBeenCalled());
    expect(screen.getByTestId('mock-scanner')).toBeInTheDocument();
  });
});
