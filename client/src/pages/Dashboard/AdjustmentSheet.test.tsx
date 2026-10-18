import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { DayGoal } from '@/types';
import { ApiError } from '@/api/client';
import AdjustmentSheet from './AdjustmentSheet';

vi.mock('@/api/goals', () => ({
  setDayGoal: vi.fn(),
  resetDayGoal: vi.fn(),
  getRecentGoals: vi.fn(),
}));

import { getRecentGoals, resetDayGoal, setDayGoal } from '@/api/goals';

const DATE = '2026-08-05';
const MODES = { calories: 'limit', protein: 'target', carbs: 'target', fat: 'limit', fiber: 'target', sugar: 'limit' };

const goal = (over: Partial<DayGoal> = {}): DayGoal => ({
  date: DATE,
  adjusted: false,
  reason: null,
  calories: 2400,
  baseCalories: null,
  macroGoals: { protein: 150, carbs: 300, fat: 67, fiber: 35 },
  enabledMacros: ['protein', 'carbs', 'fat', 'fiber'],
  macroModes: MODES,
  goalThreshold: 10,
  caloriesEnabled: true,
  ...over,
});

const none = { calories: null, protein: null, carbs: null, fat: null, fiber: null, sugar: null };
const LAUF = { ...none, reason: 'Lauf 25 km', calories: 3100, protein: 150, carbs: 480, fat: 67, fiber: 35, used_at: '2026-08-01T10:00:00Z' };
const RUHE = { ...none, reason: null, calories: 1800, protein: 120, used_at: '2026-07-20T10:00:00Z' };

function renderSheet(g: DayGoal = goal(), { autoCalc = false, onClose = vi.fn() } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  render(
    <QueryClientProvider client={client}>
      <AdjustmentSheet open onClose={onClose} goal={g} autoCalcCalories={autoCalc} />
    </QueryClientProvider>,
  );
  return { onClose, invalidate };
}

const field = (key: string) => screen.getByTestId(`goal-adjust-${key}`) as HTMLInputElement;

beforeEach(() => {
  vi.resetAllMocks();
  vi.mocked(getRecentGoals).mockResolvedValue({ ok: true, recent: [] });
  vi.mocked(setDayGoal).mockResolvedValue({ ok: true, goal: {} as never });
  vi.mocked(resetDayGoal).mockResolvedValue({ ok: true, goal: {} as never });
});

describe('AdjustmentSheet fields', () => {
  it('starts from the day Tagesziel with calories and every enabled macro', () => {
    renderSheet();
    expect(field('calories').value).toBe('2400');
    expect(field('protein').value).toBe('150');
    expect(field('carbs').value).toBe('300');
    expect(field('fat').value).toBe('67');
    expect(field('fiber').value).toBe('35');
    expect(field('reason').value).toBe('');
  });

  it('renders no field for a macro that is not enabled', () => {
    renderSheet(goal({ enabledMacros: ['protein'], macroGoals: { protein: 150 } }));
    expect(screen.getByTestId('goal-adjust-protein')).toBeInTheDocument();
    expect(screen.queryByTestId('goal-adjust-carbs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('goal-adjust-fat')).not.toBeInTheDocument();
    expect(screen.queryByTestId('goal-adjust-fiber')).not.toBeInTheDocument();
    expect(screen.queryByTestId('goal-adjust-sugar')).not.toBeInTheDocument();
  });

  it('offers sugar when it is enabled', () => {
    renderSheet(goal({ enabledMacros: ['protein', 'sugar'], macroGoals: { protein: 150, sugar: 50 } }));
    expect(field('sugar').value).toBe('50');
  });

  it('leaves out calories when the account hides them', () => {
    renderSheet(goal({ caloriesEnabled: false, calories: null }));
    expect(screen.queryByTestId('goal-adjust-calories')).not.toBeInTheDocument();
  });

  it('keeps the numeric keypad on the calorie input', () => {
    renderSheet();
    expect(field('calories')).toHaveAttribute('inputmode', 'tel');
  });

  it('shows a macro without a goal as an empty field', () => {
    renderSheet(goal({ macroGoals: { protein: 150 } }));
    expect(field('carbs').value).toBe('');
  });
});

describe('AdjustmentSheet saving', () => {
  it('sends the fields and the reason, refreshes the day and closes', async () => {
    const { onClose, invalidate } = renderSheet();
    await userEvent.clear(field('calories'));
    await userEvent.type(field('calories'), '3100');
    await userEvent.clear(field('carbs'));
    await userEvent.type(field('carbs'), '480');
    await userEvent.type(field('reason'), 'Lauf 25 km');
    await userEvent.click(screen.getByTestId('goal-adjust-save'));

    await waitFor(() => expect(onClose).toHaveBeenCalled());
    // Every field goes along: a PUT fills unsent keys from the Basisziel, which
    // on an already adjusted day would silently drop the other adjusted values.
    expect(setDayGoal).toHaveBeenCalledWith(DATE, {
      calories: 3100, protein: 150, carbs: 480, fat: 67, fiber: 35, reason: 'Lauf 25 km',
    });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['dashboard'] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['day-entries'] });
  });

  it('sends 0 for a macro the user emptied (no goal)', async () => {
    renderSheet();
    await userEvent.clear(field('fiber'));
    await userEvent.click(screen.getByTestId('goal-adjust-save'));
    await waitFor(() => expect(setDayGoal).toHaveBeenCalled());
    expect(vi.mocked(setDayGoal).mock.calls[0][1]).toMatchObject({ fiber: 0, protein: 150 });
  });

  it('cannot save while nothing was changed', async () => {
    renderSheet();
    expect(screen.getByTestId('goal-adjust-save')).toBeDisabled();
    await userEvent.type(field('reason'), 'x');
    expect(screen.getByTestId('goal-adjust-save')).toBeEnabled();
  });

  it('shows the server message and stays open on a rejected save', async () => {
    vi.mocked(setDayGoal).mockRejectedValue(new ApiError(400, 'calories: must be a whole number from 1 to 9999'));
    const { onClose } = renderSheet();
    await userEvent.type(field('reason'), 'x');
    await userEvent.click(screen.getByTestId('goal-adjust-save'));
    expect(await screen.findByTestId('goal-adjust-error')).toHaveTextContent('calories: must be a whole number from 1 to 9999');
    expect(onClose).not.toHaveBeenCalled();
  });
});

describe('AdjustmentSheet reset', () => {
  it('is hidden on a day that is not adjusted', () => {
    renderSheet(goal({ adjusted: false }));
    expect(screen.queryByTestId('goal-adjust-reset')).not.toBeInTheDocument();
  });

  it('resets the day to the Basisziel, refreshes and closes', async () => {
    const { onClose, invalidate } = renderSheet(goal({ adjusted: true, reason: 'Lauf 25 km', baseCalories: 2400, calories: 3100 }));
    expect(field('reason').value).toBe('Lauf 25 km');
    await userEvent.click(screen.getByTestId('goal-adjust-reset'));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
    expect(resetDayGoal).toHaveBeenCalledWith(DATE);
    expect(setDayGoal).not.toHaveBeenCalled();
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['dashboard'] });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['day-entries'] });
  });
});

describe('AdjustmentSheet quick picks', () => {
  it('lists recent adjustments with reason and values', async () => {
    vi.mocked(getRecentGoals).mockResolvedValue({ ok: true, recent: [LAUF, RUHE] });
    renderSheet();
    const picks = await screen.findAllByTestId('goal-adjust-pick');
    expect(picks).toHaveLength(2);
    expect(picks[0]).toHaveTextContent('Lauf 25 km');
    expect(picks[0]).toHaveTextContent('3,100');
  });

  it('only pre-fills the fields and saves nothing', async () => {
    vi.mocked(getRecentGoals).mockResolvedValue({ ok: true, recent: [LAUF] });
    const { onClose } = renderSheet();
    await userEvent.click(await screen.findByTestId('goal-adjust-pick'));
    expect(field('calories').value).toBe('3100');
    expect(field('carbs').value).toBe('480');
    expect(field('reason').value).toBe('Lauf 25 km');
    expect(setDayGoal).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
  });

  it('empties the fields a pick has no value for and clears the reason of a reason-less pick', async () => {
    vi.mocked(getRecentGoals).mockResolvedValue({ ok: true, recent: [RUHE] });
    renderSheet(goal({ adjusted: true, reason: 'Lauf 25 km' }));
    await userEvent.click(await screen.findByTestId('goal-adjust-pick'));
    expect(field('calories').value).toBe('1800');
    expect(field('carbs').value).toBe('');
    expect(field('reason').value).toBe('');
  });

  it('shows no quick pick section when there is nothing recent', async () => {
    renderSheet();
    await waitFor(() => expect(getRecentGoals).toHaveBeenCalled());
    expect(screen.queryByTestId('goal-adjust-pick')).not.toBeInTheDocument();
  });
});

describe('AdjustmentSheet auto-calc accounts', () => {
  it('derives the calories from the macros and makes the field read-only', () => {
    renderSheet(goal({ calories: 2400 }), { autoCalc: true });
    // 150*4 + 300*4 + 67*9 = 2403
    expect(field('calories').value).toBe('2403');
    expect(field('calories')).toHaveAttribute('readonly');
  });

  it('follows the macro fields as they change', async () => {
    renderSheet(goal(), { autoCalc: true });
    await userEvent.clear(field('carbs'));
    await userEvent.type(field('carbs'), '400');
    // 150*4 + 400*4 + 67*9 = 2803
    expect(field('calories').value).toBe('2803');
  });

  it('sends the derived calories', async () => {
    renderSheet(goal(), { autoCalc: true });
    await userEvent.clear(field('carbs'));
    await userEvent.type(field('carbs'), '400');
    await userEvent.click(screen.getByTestId('goal-adjust-save'));
    await waitFor(() => expect(setDayGoal).toHaveBeenCalled());
    expect(vi.mocked(setDayGoal).mock.calls[0][1]).toMatchObject({ calories: 2803, carbs: 400 });
  });

  it('leaves the calories editable for accounts that do not auto-calc', () => {
    renderSheet(goal(), { autoCalc: false });
    expect(field('calories')).not.toHaveAttribute('readonly');
  });
});
