import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { EntryComponent } from '@/types';
import type { EntryDetailResponse } from '@/api/entries';
import MealRoutes from './MealRoutes';

vi.mock('@/hooks/useAuth', () => ({
  useRequireAuth: () => ({ user: { id: 1 }, isLoading: false }),
}));
vi.mock('@/api/entries', () => ({
  getEntry: vi.fn(),
  updateComponentGrams: vi.fn(),
  deleteComponent: vi.fn(),
}));

import { deleteComponent, getEntry, updateComponentGrams } from '@/api/entries';

const oats: EntryComponent = {
  id: 11, food_id: 5, name: 'Oats', grams: 50, calories_per_100g: 372,
  macros_per_100g: { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 },
};
const milk: EntryComponent = {
  id: 12, food_id: null, name: 'Milk', grams: 200, calories_per_100g: 64,
  macros_per_100g: { protein_g: 3.4, carbs_g: 5, fat_g: 3.5, fiber_g: null, sugar_g: 5 },
};

function detail(components: EntryComponent[], over: Partial<EntryDetailResponse['entry']> = {}): EntryDetailResponse {
  return {
    ok: true,
    caloriesEnabled: true,
    enabledMacros: ['protein', 'fiber'],
    entry: {
      id: 7, date: '2026-08-30', calories: 314, name: 'Porridge', eaten_at: '2026-08-30T07:30:00Z', local_time: '7:30 AM',
      macros: { protein_g: 14, carbs_g: 39, fat_g: 11, fiber_g: 5, sugar_g: 11 },
      components, ...over,
    },
  };
}

function renderPage(path = '/entries/7') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={[path]}>
        {/* Mounted like router.tsx does, so MealRoutes' own <Routes> resolve relative to it. */}
        <Routes>
          <Route path="/entries/:id/*" element={<MealRoutes />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('Meal page', () => {
  beforeEach(() => {
    vi.mocked(getEntry).mockReset();
    vi.mocked(updateComponentGrams).mockReset();
    vi.mocked(deleteComponent).mockReset();
  });

  it('shows the meal totals, including fiber, and one card per component', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats, milk]));
    renderPage();

    expect(await screen.findByTestId('meal-title')).toHaveTextContent('Porridge');
    expect(within(screen.getByTestId('meal-total-fiber')).getByText('5')).toBeInTheDocument();
    expect(within(screen.getByTestId('meal-total-protein')).getByText('14')).toBeInTheDocument();
    // Only oats know fiber, so the total is flagged as a lower bound.
    expect(screen.getByTestId('meal-total-fiber')).toHaveAttribute('title', expect.stringMatching(/only 1 of 2/i));
    expect(screen.getByTestId('meal-total-protein')).not.toHaveAttribute('title');

    const cards = screen.getAllByTestId('meal-component');
    expect(cards).toHaveLength(2);
    expect(within(cards[0]).getByTestId('meal-component-kcal')).toHaveTextContent('186');
    expect(within(cards[1]).getByText('Ad hoc')).toBeInTheDocument();
  });

  it('asks the server for another account\'s entry when ?user= is set, and is read-only', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats]));
    renderPage('/entries/7?user=42');

    await screen.findByTestId('meal-title');
    expect(getEntry).toHaveBeenCalledWith(7, 42);
    expect(screen.queryByTestId('meal-add-component')).not.toBeInTheDocument();
    expect(screen.queryByTestId('meal-component-remove')).not.toBeInTheDocument();
    expect(screen.getByTestId('meal-component-grams')).toBeDisabled();
  });

  it('sends the new grams and shows the server\'s recomputed totals', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats]));
    vi.mocked(updateComponentGrams).mockResolvedValue(
      detail([{ ...oats, grams: 100 }], { calories: 372, macros: { protein_g: 14, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 } }),
    );
    renderPage();

    await userEvent.click(await screen.findByTestId('meal-component-grams'));
    const input = screen.getByTestId('meal-component-grams-input');
    await userEvent.clear(input);
    await userEvent.type(input, '100,5{Enter}');

    await waitFor(() => expect(updateComponentGrams).toHaveBeenCalledWith(7, 11, 100.5));
    // The calorie ring carries the server's new total, not a client-side guess.
    expect(await screen.findByRole('img', { name: /: 372 kcal/ })).toBeInTheDocument();
  });

  it('does not send a change for an unchanged or invalid amount', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats]));
    renderPage();

    for (const text of ['50', '0', 'abc']) {
      await userEvent.click(await screen.findByTestId('meal-component-grams'));
      const input = screen.getByTestId('meal-component-grams-input');
      await userEvent.clear(input);
      await userEvent.type(input, `${text}{Enter}`);
    }
    expect(updateComponentGrams).not.toHaveBeenCalled();
  });

  it('asks before removing a component', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats, milk]));
    vi.mocked(deleteComponent).mockResolvedValue(detail([milk], { calories: 128 }));
    renderPage();

    await userEvent.click((await screen.findAllByTestId('meal-component-remove'))[0]);
    expect(deleteComponent).not.toHaveBeenCalled();
    await userEvent.click(screen.getByTestId('meal-component-remove-confirm'));

    await waitFor(() => expect(deleteComponent).toHaveBeenCalledWith(7, 11));
    await waitFor(() => expect(screen.getAllByTestId('meal-component')).toHaveLength(1));
  });

  it('explains a direct entry instead of listing components', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([], { name: 'Mensa' }));
    renderPage();

    expect(await screen.findByTestId('meal-direct')).toBeInTheDocument();
    expect(screen.queryByTestId('meal-component')).not.toBeInTheDocument();
    // The totals are still shown.
    expect(within(screen.getByTestId('meal-total-fiber')).getByText('5')).toBeInTheDocument();
  });

  it('links the plus button to the add-component route, which renders a placeholder', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail([oats]));
    renderPage();

    await userEvent.click(await screen.findByTestId('meal-add-component'));
    expect(await screen.findByTestId('add-component-placeholder')).toBeInTheDocument();
    await userEvent.click(screen.getByTestId('add-component-back'));
    expect(await screen.findByTestId('meal-title')).toBeInTheDocument();
  });
});
