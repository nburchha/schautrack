import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import type { Entry } from '@/types';
import { createEntry, deleteEntry, getEntry } from '@/api/entries';
import { useToastStore } from '@/stores/toastStore';
import EntryList from './EntryList';

vi.mock('@/api/entries', () => ({
  updateEntry: vi.fn(),
  deleteEntry: vi.fn(),
  createEntry: vi.fn(),
  getEntry: vi.fn(),
}));

vi.mock('@/api/savedFoods', () => ({ saveEntryAsFood: vi.fn() }));

const entry = (over: Partial<Entry> = {}): Entry => ({
  id: 7, date: '2026-08-30', time: '07:30', amount: 314, name: 'Porridge',
  macros: { protein: 7, carbs: 40, fat: 5, fiber: 5, sugar: 2 }, componentCount: 2, ...over,
});

function Where() {
  const loc = useLocation();
  return <div data-testid="where">{loc.pathname + loc.search}</div>;
}

function renderList(entries: Entry[], extra: { viewUserId?: number; canEdit?: boolean } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <MemoryRouter initialEntries={['/dashboard']}>
        <Routes>
          <Route
            path="/dashboard"
            element={
              <EntryList
                entries={entries}
                canEdit={extra.canEdit ?? true}
                viewUserId={extra.viewUserId}
                enabledMacros={['protein', 'fiber']}
                caloriesEnabled
                autoCalcCalories={false}
              />
            }
          />
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('EntryList -> meal page', () => {
  it('shows how many components an entry has, and nothing for a direct entry', () => {
    renderList([entry(), entry({ id: 8, name: 'Mensa', componentCount: 0 })]);
    expect(screen.getByText('2 components')).toBeInTheDocument();
    expect(screen.getAllByTestId('entry-component-count')).toHaveLength(1);
  });

  it('opens the meal page when the card is clicked', async () => {
    renderList([entry()]);
    await userEvent.click(screen.getByTestId('entry-row'));
    expect(screen.getByTestId('where')).toHaveTextContent('/entries/7');
  });

  it('opens the meal page through the chevron link', async () => {
    renderList([entry()]);
    const link = screen.getByRole('link', { name: /open details for porridge/i });
    expect(link).toHaveAttribute('href', '/entries/7');
    await userEvent.click(link);
    expect(screen.getByTestId('where')).toHaveTextContent('/entries/7');
  });

  it('keeps the controls inside the card to themselves', async () => {
    renderList([entry()]);
    // Clicking the name starts the inline rename; it must not navigate away.
    await userEvent.click(screen.getByRole('button', { name: 'Porridge' }));
    expect(screen.queryByTestId('where')).not.toBeInTheDocument();
    expect(screen.getByDisplayValue('Porridge')).toBeInTheDocument();
  });

  it('carries the linked account into the link so the right entry is read', () => {
    renderList([entry()], { viewUserId: 42, canEdit: false });
    expect(screen.getByRole('link', { name: /open details/i })).toHaveAttribute('href', '/entries/7?user=42');
  });
});

describe('EntryList delete and undo', () => {
  const detail = {
    ok: true, caloriesEnabled: true, enabledMacros: ['protein'],
    entry: {
      id: 7, date: '2026-08-30', calories: 314, name: 'Porridge', eaten_at: '2026-08-30T05:30:00Z', local_time: '7:30 AM',
      macros: { protein_g: 7, carbs_g: 40, fat_g: 5, fiber_g: 5, sugar_g: 2 },
      components: [{ id: 1, food_id: 5, name: 'Oats', grams: 50, calories_per_100g: 372, macros_per_100g: { protein_g: 13.5, carbs_g: 58, fat_g: 7, fiber_g: 10, sugar_g: 1 } }],
    },
  };

  async function deleteAndUndo() {
    useToastStore.setState({ toasts: [] });
    renderList([entry()]);
    await userEvent.click(screen.getByRole('button', { name: /delete/i }));
    await vi.waitFor(() => expect(deleteEntry).toHaveBeenCalledWith(7));
    const toast = useToastStore.getState().toasts.at(-1);
    await toast?.action?.onClick();
  }

  it('undo recreates a meal with its components and the time it was eaten', async () => {
    vi.mocked(getEntry).mockResolvedValue(detail);
    vi.mocked(deleteEntry).mockResolvedValue({ ok: true });
    vi.mocked(createEntry).mockResolvedValue({ ok: true, id: 9 });
    await deleteAndUndo();

    expect(createEntry).toHaveBeenCalledWith(expect.objectContaining({
      entry_date: '2026-08-30',
      entry_name: 'Porridge',
      eaten_at: '2026-08-30T05:30:00Z',
      components: [{ food_id: 5, grams: 50 }],
    }));
    expect(vi.mocked(createEntry).mock.calls.at(-1)?.[0].amount).toBeUndefined();
  });

  it('undo still works as a direct entry when the details could not be read', async () => {
    vi.mocked(getEntry).mockRejectedValue(new Error('offline'));
    vi.mocked(deleteEntry).mockResolvedValue({ ok: true });
    vi.mocked(createEntry).mockResolvedValue({ ok: true, id: 9 });
    await deleteAndUndo();

    expect(createEntry).toHaveBeenLastCalledWith(expect.objectContaining({ entry_name: 'Porridge', amount: 314 }));
  });
});
