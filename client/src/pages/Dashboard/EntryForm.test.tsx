import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Food, User } from '@/types';
import { useAuthStore } from '@/stores/authStore';
import { todayIn } from '@/lib/eatenAt';
import EntryForm from './EntryForm';

vi.mock('@/api/entries', () => ({ createEntry: vi.fn() }));
vi.mock('@/api/savedFoods', () => ({ createSavedFood: vi.fn() }));
vi.mock('@/api/foods', () => ({ searchFoods: vi.fn() }));

import { createEntry } from '@/api/entries';
import { searchFoods } from '@/api/foods';

const TZ = 'Europe/Berlin';
const today = todayIn(TZ);
const PAST = '2026-01-15';

const oats: Food = {
  id: 5, source: 'bls', source_code: 'C1', name: 'Oats', source_classification: 'C', calories_per_100g: 372,
  macros_per_100g: { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 },
};
const milk: Food = {
  id: 6, source: 'bls', source_code: 'M1', name: 'Milk', source_classification: 'M', calories_per_100g: 64,
  macros_per_100g: { protein_g: 3.4, carbs_g: 5, fat_g: 3.5, fiber_g: null, sugar_g: 5 },
};

function renderForm(selectedDate = today, onSubmit = vi.fn()) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <EntryForm
        selectedDate={selectedDate}
        caloriesEnabled
        autoCalcCalories={false}
        enabledMacros={['protein', 'fiber']}
        hasAiEnabled={false}
        aiUsage={null}
        aiProviderName={null}
        barcodeEnabled
        onSubmit={onSubmit}
      />
    </QueryClientProvider>,
  );
  return onSubmit;
}

async function pick(query: string, food: Food, grams: string) {
  vi.mocked(searchFoods).mockResolvedValue({ ok: true, foods: [food] });
  await userEvent.clear(screen.getByTestId('food-search-input'));
  await userEvent.type(screen.getByTestId('food-search-input'), query);
  await userEvent.click(await screen.findByTestId('food-search-result'));
  const input = screen.getByTestId('food-search-grams');
  await userEvent.clear(input);
  await userEvent.type(input, grams);
  await userEvent.click(screen.getByTestId('food-search-add'));
}

const timeInput = () => screen.getByTestId('entry-form-time-input') as HTMLInputElement;
const dateInput = () => screen.getByLabelText(/date/i) as HTMLInputElement;

describe('EntryForm with catalog components', () => {
  beforeEach(() => {
    vi.mocked(createEntry).mockReset();
    vi.mocked(createEntry).mockResolvedValue({ ok: true, id: 1 });
    vi.mocked(searchFoods).mockReset();
    useAuthStore.setState({ user: { id: 1, timezone: TZ } as User });
  });

  it('builds a meal from searched foods and sends components, not sums', async () => {
    const onSubmit = renderForm();
    await pick('Oat', oats, '50');
    await pick('Milk', milk, '200');

    expect(screen.getAllByTestId('draft-component')).toHaveLength(2);
    // 50 g oats + 200 g milk = 186 + 128 kcal; the fields show the computed sum.
    expect(screen.getByLabelText('Calories')).toHaveValue('314');
    expect(screen.getByLabelText('Calories')).toHaveAttribute('readonly');
    expect(screen.getByLabelText('Fiber')).toHaveValue('5');
    expect(screen.getByLabelText('Protein')).toHaveValue('14');

    await userEvent.click(screen.getByRole('button', { name: 'Track' }));
    await waitFor(() => expect(onSubmit).toHaveBeenCalled());

    const body = vi.mocked(createEntry).mock.calls[0][0];
    expect(body.components).toEqual([{ food_id: 5, grams: 50 }, { food_id: 6, grams: 200 }]);
    expect(body.entry_name).toBe('Oats, Milk');
    expect(body.entry_date).toBe(today);
    expect(body.amount).toBeUndefined();
    expect(body.protein_g).toBeUndefined();
    // Today needs no time: the entry takes the moment it is created.
    expect(body.eaten_at).toBeUndefined();
  });

  it('keeps a name the user typed', async () => {
    renderForm();
    await userEvent.type(screen.getByLabelText('Food name'), 'Breakfast');
    await pick('Oat', oats, '50');
    await userEvent.click(screen.getByRole('button', { name: 'Track' }));

    await waitFor(() => expect(createEntry).toHaveBeenCalled());
    expect(vi.mocked(createEntry).mock.calls[0][0].entry_name).toBe('Breakfast');
  });

  it('recomputes when grams change and when a row is removed', async () => {
    renderForm();
    await pick('Oat', oats, '50');
    await pick('Milk', milk, '200');

    const row = screen.getAllByTestId('draft-component')[0];
    const grams = within(row).getByTestId('draft-component-grams');
    await userEvent.clear(grams);
    await userEvent.type(grams, '100');
    expect(screen.getByLabelText('Calories')).toHaveValue('500');

    await userEvent.click(within(screen.getAllByTestId('draft-component')[1]).getByTestId('draft-component-remove'));
    expect(screen.getByLabelText('Calories')).toHaveValue('372');

    // Removing the last one hands the fields back for a direct entry.
    await userEvent.click(within(screen.getByTestId('draft-component')).getByTestId('draft-component-remove'));
    expect(screen.queryByTestId('draft-list')).not.toBeInTheDocument();
    expect(screen.getByLabelText('Calories')).not.toHaveAttribute('readonly');
  });

  it('still tracks a direct entry from typed values', async () => {
    renderForm();
    await userEvent.type(screen.getByLabelText('Food name'), 'Canteen');
    await userEvent.type(screen.getByLabelText('Calories'), '750');
    await userEvent.click(screen.getByRole('button', { name: 'Track' }));

    await waitFor(() => expect(createEntry).toHaveBeenCalled());
    const body = vi.mocked(createEntry).mock.calls[0][0];
    expect(body).toMatchObject({ entry_date: today, entry_name: 'Canteen', amount: 750 });
    expect(body.components).toBeUndefined();
  });
});

describe('EntryForm time field', () => {
  beforeEach(() => {
    vi.mocked(createEntry).mockReset();
    vi.mocked(createEntry).mockResolvedValue({ ok: true });
    useAuthStore.setState({ user: { id: 1, timezone: TZ } as User });
  });

  it('is a native time input, empty for today', () => {
    renderForm();
    expect(timeInput()).toHaveAttribute('type', 'time');
    expect(timeInput()).toHaveValue('');
    expect(screen.queryByTestId('entry-form-time-clear')).not.toBeInTheDocument();
  });

  it('starts at 09:00 on another day and sends it', async () => {
    renderForm(PAST);
    expect(timeInput()).toHaveValue('09:00');

    await userEvent.type(screen.getByLabelText('Calories'), '500');
    await userEvent.click(screen.getByRole('button', { name: 'Track' }));
    await waitFor(() => expect(createEntry).toHaveBeenCalled());

    const body = vi.mocked(createEntry).mock.calls[0][0];
    expect(body.entry_date).toBe(PAST);
    expect(body.eaten_at).toBe('2026-01-15T09:00:00+01:00');
  });

  it('follows the date until the user picks a time of their own', async () => {
    renderForm();
    fireEvent.change(dateInput(), { target: { value: PAST } });
    expect(timeInput()).toHaveValue('09:00');

    fireEvent.change(dateInput(), { target: { value: today } });
    expect(timeInput()).toHaveValue('');

    fireEvent.change(dateInput(), { target: { value: PAST } });
    fireEvent.change(timeInput(), { target: { value: '18:00' } });
    expect(timeInput()).toHaveValue('18:00');

    // A deliberate choice survives another date change.
    fireEvent.change(dateInput(), { target: { value: '2026-01-16' } });
    expect(timeInput()).toHaveValue('18:00');
  });

  it('lets the user drop the default time', async () => {
    renderForm(PAST);
    await userEvent.click(screen.getByTestId('entry-form-time-clear'));
    expect(timeInput()).toHaveValue('');

    await userEvent.type(screen.getByLabelText('Calories'), '500');
    await userEvent.click(screen.getByRole('button', { name: 'Track' }));
    await waitFor(() => expect(createEntry).toHaveBeenCalled());
    expect(vi.mocked(createEntry).mock.calls[0][0].eaten_at).toBeUndefined();
  });

  it('sends a time chosen for today, in the account\'s zone', async () => {
    renderForm();
    fireEvent.change(timeInput(), { target: { value: '07:30' } });

    await userEvent.type(screen.getByLabelText('Calories'), '300');
    await userEvent.click(screen.getByRole('button', { name: 'Track' }));
    await waitFor(() => expect(createEntry).toHaveBeenCalled());
    expect(vi.mocked(createEntry).mock.calls[0][0].eaten_at).toMatch(new RegExp(`^${today}T07:30:00[+-]\\d\\d:\\d\\d$`));
  });

  it('puts the barcode scanner next to the food search, since a scan makes a component', () => {
    renderForm();
    const search = screen.getByTestId('entry-form-search');
    expect(within(search).getByTestId('food-search-scan')).toBeInTheDocument();
    // One scanner, not a second one beside the photo and quick-add buttons.
    expect(screen.getAllByLabelText('Scan barcode')).toHaveLength(1);
  });
});
