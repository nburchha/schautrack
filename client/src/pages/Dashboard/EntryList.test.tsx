import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router';
import { describe, expect, it, vi } from 'vitest';
import type { Entry } from '@/types';
import EntryList from './EntryList';

vi.mock('@/api/entries', () => ({
  updateEntry: vi.fn(),
  deleteEntry: vi.fn(),
  createEntry: vi.fn(),
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
