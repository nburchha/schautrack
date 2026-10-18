import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DayGoal } from '@/types';
import { useDashboardStore } from '@/stores/dashboardStore';
import Dashboard from './Dashboard';

vi.mock('@/hooks/useAuth', () => ({
  useRequireAuth: () => ({ user: { id: 1, bodyFatEnabled: false }, isLoading: false }),
}));
vi.mock('@/api/entries', () => ({ getDashboard: vi.fn(), getDayEntries: vi.fn() }));
vi.mock('@/api/weight', () => ({ getWeightDay: vi.fn() }));

// The panel's own rendering is covered by TodayPanel.test.tsx; here it only
// has to say whose and which day's goal it was handed.
vi.mock('./TodayPanel', () => ({
  default: ({ goal }: { goal: DayGoal }) => <div data-testid="today-panel">{`${goal.date}:${goal.calories}`}</div>,
}));
vi.mock('./AdjustmentSheet', () => ({ default: () => null }));
vi.mock('./EntryForm', () => ({ default: () => null }));
vi.mock('./SavedFoodsRow', () => ({ default: () => null }));
vi.mock('./Timeline', () => ({ default: () => null }));
vi.mock('./EntryList', () => ({ default: () => null }));
vi.mock('./WeightRow', () => ({ default: () => null }));
vi.mock('./PlanCard', () => ({ default: () => null }));
vi.mock('./TodoList', () => ({ default: () => null }));
vi.mock('./NoteEditor', () => ({ default: () => null }));

import { getDashboard, getDayEntries } from '@/api/entries';
import { getWeightDay } from '@/api/weight';

const DAY1 = '2026-08-05';
const DAY2 = '2026-08-06';

const goalOf = (date: string, calories: number): DayGoal => ({
  date, adjusted: false, reason: null, calories, baseCalories: null, macroGoals: {},
  enabledMacros: [], macroModes: {}, goalThreshold: 10, caloriesEnabled: true,
});

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => { resolve = r; });
  return { promise, resolve };
}

beforeEach(() => {
  vi.resetAllMocks();
  useDashboardStore.setState({ selectedDate: DAY1, currentUserId: 1, currentLabel: 'You', canEdit: true });
  vi.mocked(getDashboard).mockResolvedValue({
    user: { id: 1 },
    selectedDate: DAY1,
    todayStr: DAY1,
    sharedViews: [{ userId: 1, shares: {} }, { userId: 2, shares: { nutrition: true } }],
    range: {},
    autoCalcCalories: false,
    caloriesEnabled: true,
    enabledMacros: [],
    weightUnit: 'kg',
  } as never);
  vi.mocked(getWeightDay).mockResolvedValue({ entry: null, lastWeight: null } as never);
});

function renderDashboard() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={client}>
      <Dashboard />
    </QueryClientProvider>,
  );
}

describe('Dashboard Today panel', () => {
  it('never shows one account\'s goal while another account loads', async () => {
    const friend = deferred<never>();
    vi.mocked(getDayEntries).mockImplementation(((userId: number, date: string) =>
      userId === 1 ? Promise.resolve({ ok: true, date, entries: [], goal: goalOf(date, 2400) }) : friend.promise) as never);
    renderDashboard();
    expect(await screen.findByTestId('today-panel')).toHaveTextContent(`${DAY1}:2400`);

    act(() => useDashboardStore.getState().selectUser(2, 'Friend', false));

    await waitFor(() => expect(getDayEntries).toHaveBeenCalledWith(2, DAY1));
    expect(screen.queryByTestId('today-panel')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-panel-skeleton')).toBeInTheDocument();

    friend.resolve({ ok: true, date: DAY1, entries: [], goal: goalOf(DAY1, 1800) } as never);
    expect(await screen.findByTestId('today-panel')).toHaveTextContent(`${DAY1}:1800`);
  });

  it('never labels the previous day\'s goal as the newly selected day', async () => {
    const next = deferred<never>();
    vi.mocked(getDayEntries).mockImplementation(((_: number, date: string) =>
      date === DAY1 ? Promise.resolve({ ok: true, date, entries: [], goal: goalOf(date, 2400) }) : next.promise) as never);
    renderDashboard();
    expect(await screen.findByTestId('today-panel')).toHaveTextContent(`${DAY1}:2400`);

    act(() => useDashboardStore.getState().selectDay(DAY2));

    await waitFor(() => expect(getDayEntries).toHaveBeenCalledWith(1, DAY2));
    expect(screen.queryByTestId('today-panel')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-panel-skeleton')).toBeInTheDocument();

    next.resolve({ ok: true, date: DAY2, entries: [], goal: goalOf(DAY2, 3100) } as never);
    expect(await screen.findByTestId('today-panel')).toHaveTextContent(`${DAY2}:3100`);
  });
});
