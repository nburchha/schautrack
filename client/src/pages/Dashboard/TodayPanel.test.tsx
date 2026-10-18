import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import type { DayGoal } from '@/types';
import TodayPanel from './TodayPanel';

const MODES = { calories: 'limit', protein: 'target', carbs: 'target', fat: 'limit', fiber: 'target', sugar: 'limit' };

const goal = (over: Partial<DayGoal> = {}): DayGoal => ({
  date: '2026-08-05',
  adjusted: false,
  reason: null,
  calories: 3100,
  baseCalories: null,
  macroGoals: { protein: 150, carbs: 480, fat: 67, fiber: 35 },
  enabledMacros: ['protein', 'carbs', 'fat', 'fiber'],
  macroModes: MODES,
  goalThreshold: 10,
  caloriesEnabled: true,
  ...over,
});

const TOTALS = { protein: 92, carbs: 148, fat: 41, fiber: 14 };

function renderPanel(g: DayGoal, eatenCalories = 1420, macroTotals: Record<string, number> = TOTALS, onEdit?: () => void) {
  return render(<TodayPanel goal={g} eatenCalories={eatenCalories} macroTotals={macroTotals} onEdit={onEdit} />);
}

describe('TodayPanel calorie header', () => {
  it('shows eaten, goal and the calories left', () => {
    renderPanel(goal());
    expect(screen.getByTestId('today-eaten')).toHaveTextContent('1,420');
    expect(screen.getByTestId('today-goal')).toHaveTextContent('/ 3,100 kcal');
    expect(screen.getByTestId('today-remaining')).toHaveTextContent('1,680 kcal left');
  });

  it('says how many calories over once the goal is passed', () => {
    renderPanel(goal({ calories: 2000 }), 2250);
    expect(screen.getByTestId('today-remaining')).toHaveTextContent('250 kcal over');
    expect(screen.queryByText(/kcal left/)).not.toBeInTheDocument();
  });

  it('shows only the eaten calories when there is no calorie goal', () => {
    renderPanel(goal({ calories: null }));
    expect(screen.getByTestId('today-eaten')).toHaveTextContent('1,420');
    expect(screen.getByText('kcal')).toBeInTheDocument();
    expect(screen.queryByTestId('today-goal')).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-remaining')).not.toBeInTheDocument();
  });
});

describe('TodayPanel adjusted chip', () => {
  it('shows nothing on an unadjusted day', () => {
    renderPanel(goal());
    expect(screen.queryByTestId('today-adjusted')).not.toBeInTheDocument();
  });

  it('names the reason and the Basisziel calories on an adjusted day', () => {
    renderPanel(goal({ adjusted: true, reason: 'Lauf 25 km', baseCalories: 2400 }));
    const chip = screen.getByTestId('today-adjusted');
    expect(chip).toHaveTextContent('adjusted: Lauf 25 km');
    expect(chip).toHaveTextContent('Base 2,400');
  });

  it('still flags an adjusted day that has no reason', () => {
    renderPanel(goal({ adjusted: true, reason: null, baseCalories: 2400 }));
    const chip = screen.getByTestId('today-adjusted');
    expect(chip).toHaveTextContent('adjusted');
    expect(chip).not.toHaveTextContent('null');
    expect(chip).toHaveTextContent('Base 2,400');
  });

  it('omits the Base part when the Basisziel has no calories', () => {
    renderPanel(goal({ adjusted: true, reason: 'Rest', baseCalories: null }));
    expect(screen.getByTestId('today-adjusted')).not.toHaveTextContent('Base');
  });
});

describe('TodayPanel macros', () => {
  it('shows protein, carbs and fat as eaten / goal in grams', () => {
    renderPanel(goal());
    expect(screen.getByTestId('today-macro-protein')).toHaveTextContent('92 / 150 g');
    expect(screen.getByTestId('today-macro-carbs')).toHaveTextContent('148 / 480 g');
    expect(screen.getByTestId('today-macro-fat')).toHaveTextContent('41 / 67 g');
  });

  it('shows fiber as its own progress bar with eaten / goal', () => {
    renderPanel(goal());
    const fiber = screen.getByRole('progressbar', { name: /Fiber/ });
    expect(fiber).toHaveAttribute('aria-valuenow', '14');
    expect(fiber).toHaveAttribute('aria-valuemax', '35');
    expect(screen.getByTestId('today-fiber')).toHaveTextContent('14 / 35 g');
    // fiber is not a column next to protein/carbs/fat
    expect(screen.queryByTestId('today-macro-fiber')).not.toBeInTheDocument();
  });

  it('renders only the enabled macros', () => {
    renderPanel(goal({ enabledMacros: ['protein'], macroGoals: { protein: 150 } }));
    expect(screen.getByTestId('today-macro-protein')).toBeInTheDocument();
    expect(screen.queryByTestId('today-macro-carbs')).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-macro-fat')).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-fiber')).not.toBeInTheDocument();
  });

  it('never renders sugar', () => {
    renderPanel(goal({ enabledMacros: ['protein', 'sugar'], macroGoals: { protein: 150, sugar: 40 } }), 1420, { protein: 92, sugar: 30 });
    expect(screen.queryByText(/sugar/i)).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-macro-sugar')).not.toBeInTheDocument();
  });

  it('shows the eaten value alone for a macro without a goal', () => {
    renderPanel(goal({ macroGoals: { protein: 150, fiber: 35 } }));
    const carbs = screen.getByTestId('today-macro-carbs');
    expect(carbs).toHaveTextContent('148 g');
    expect(carbs).not.toHaveTextContent('/');
  });

  it('shows fiber as a plain value without a bar when it has no goal', () => {
    renderPanel(goal({ macroGoals: { protein: 150 } }));
    expect(screen.getByTestId('today-fiber')).toHaveTextContent('14 g');
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('counts an untracked macro as zero', () => {
    renderPanel(goal(), 0, {});
    expect(screen.getByTestId('today-macro-protein')).toHaveTextContent('0 / 150 g');
  });
});

describe('TodayPanel stacked calorie bar', () => {
  const segments = () => Array.from(document.querySelectorAll<HTMLElement>('[data-segment]'));
  const widthOf = (key: string) => document.querySelector<HTMLElement>(`[data-segment="${key}"]`)?.style.width;

  it('sizes each source against the calorie goal (protein and carbs x4, fat x9)', () => {
    // 100 g protein = 400 kcal, 200 g carbs = 800 kcal, 50 g fat = 450 kcal; goal 2000
    renderPanel(goal({ calories: 2000 }), 1650, { protein: 100, carbs: 200, fat: 50 });
    expect(widthOf('protein')).toBe('20%');
    expect(widthOf('carbs')).toBe('40%');
    expect(widthOf('fat')).toBe('22.5%');
  });

  it('squeezes the sources into the bar once they exceed the goal', () => {
    // 400 + 800 + 450 = 1650 kcal against a 1000 kcal goal
    renderPanel(goal({ calories: 1000 }), 1650, { protein: 100, carbs: 200, fat: 50 });
    const total = segments().reduce((sum, el) => sum + Number.parseFloat(el.style.width), 0);
    expect(total).toBeCloseTo(100, 1);
    expect(Number.parseFloat(widthOf('carbs') ?? '0')).toBeGreaterThan(Number.parseFloat(widthOf('protein') ?? '0'));
  });

  it('omits segments of macros that are not enabled', () => {
    renderPanel(goal({ calories: 2000, enabledMacros: ['protein'], macroGoals: { protein: 150 } }), 1650, { protein: 100, carbs: 200, fat: 50 });
    expect(segments().map((el) => el.dataset.segment)).toEqual(['protein']);
  });

  it('has no bar when no protein, carbs or fat is enabled', () => {
    renderPanel(goal({ enabledMacros: ['fiber'], macroGoals: { fiber: 35 } }));
    expect(screen.queryByTestId('today-source-bar')).not.toBeInTheDocument();
  });
});

describe('TodayPanel colour semantics', () => {
  it('shows no warning styling for a min-goal that is not reached yet', () => {
    const { container } = renderPanel(goal(), 400, { protein: 5, carbs: 20, fat: 3, fiber: 1 });
    expect(container.innerHTML).not.toMatch(/macro-stat--|#ef4444|#f59e0b|text-destructive|text-red|text-amber/);
  });

  it('shows a limit macro over its goal as numbers only', () => {
    const { container } = renderPanel(goal(), 3500, { protein: 92, carbs: 148, fat: 120, fiber: 14 });
    expect(screen.getByTestId('today-macro-fat')).toHaveTextContent('120 / 67 g');
    expect(container.innerHTML).not.toMatch(/macro-stat--|#ef4444|#f59e0b|text-destructive|text-red|text-amber/);
  });
});

describe('TodayPanel degraded states', () => {
  it('degrades to the calorie header when no macro is enabled', () => {
    renderPanel(goal({ enabledMacros: [], macroGoals: {} }));
    expect(screen.getByTestId('today-remaining')).toHaveTextContent('1,680 kcal left');
    expect(screen.queryByTestId('today-source-bar')).not.toBeInTheDocument();
    expect(screen.queryByTestId(/^today-macro-/)).not.toBeInTheDocument();
    expect(screen.queryByTestId('today-fiber')).not.toBeInTheDocument();
  });

  it('shows the macros without a calorie header when calories are hidden', () => {
    renderPanel(goal({ caloriesEnabled: false, calories: null }));
    expect(screen.queryByTestId('today-eaten')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-macro-protein')).toHaveTextContent('92 / 150 g');
  });

  it('keeps the no-nutrients empty state with a link to the settings', () => {
    renderPanel(goal({ caloriesEnabled: false, calories: null, enabledMacros: [], macroGoals: {} }));
    expect(screen.getByText(/No nutrients tracked/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /Enable tracking in settings/ })).toHaveAttribute('href', '/settings');
    expect(screen.queryByTestId('today-eaten')).not.toBeInTheDocument();
  });

  it('treats an account that only tracks sugar as having nothing to show', () => {
    renderPanel(goal({ caloriesEnabled: false, calories: null, enabledMacros: ['sugar'], macroGoals: { sugar: 40 } }));
    expect(screen.getByText(/No nutrients tracked/)).toBeInTheDocument();
  });
});

describe('TodayPanel onEdit', () => {
  it('offers no edit control without an onEdit handler', () => {
    renderPanel(goal());
    expect(screen.queryByTestId('today-edit')).not.toBeInTheDocument();
  });

  it('calls onEdit from the edit control', async () => {
    const onEdit = vi.fn();
    renderPanel(goal(), 1420, TOTALS, onEdit);
    const edit = screen.getByTestId('today-edit');
    await userEvent.click(edit);
    expect(onEdit).toHaveBeenCalledTimes(1);
  });
});
