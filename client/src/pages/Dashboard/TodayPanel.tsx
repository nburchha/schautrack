import { useTranslation } from 'react-i18next';
import type { DayGoal } from '@/types';
import { MACRO_LABELS, type MacroKey } from '@/lib/macros';
import { formatNumber } from '@/lib/format';
import { cn } from '@/lib/utils';

interface Props {
  /** The Tagesziel of the shown day for the viewed account (`goal` of GET /entries/day). */
  goal: DayGoal;
  /** Calories eaten on that day. */
  eatenCalories: number;
  /** Grams eaten per macro on that day; a missing key counts as 0. */
  macroTotals: Record<string, number>;
  /**
   * Opens the goal adjustment for this day. Without it the panel is read-only
   * and shows no edit control (friend views, or until the sheet is attached).
   */
  onEdit?: () => void;
}

// Energy per gram, for the stacked "where do my calories come from" bar.
const KCAL_PER_GRAM = { protein: 4, carbs: 4, fat: 9 } as const;
type EnergyKey = keyof typeof KCAL_PER_GRAM;
const ENERGY_KEYS: EnergyKey[] = ['protein', 'carbs', 'fat'];

const TEXT_COLOR: Record<string, string> = {
  protein: 'text-macro-protein',
  carbs: 'text-macro-carbs',
  fat: 'text-macro-fat',
  fiber: 'text-macro-fiber',
};

const BG_COLOR: Record<string, string> = {
  protein: 'bg-macro-protein',
  carbs: 'bg-macro-carbs',
  fat: 'bg-macro-fat',
  fiber: 'bg-macro-fiber',
};

const label = (key: string) => MACRO_LABELS[key as MacroKey]?.label || key;

const pct = (n: number) => `${Number(n.toFixed(2))}%`;

// Sources are scaled against the calorie goal. Once they add up to more than
// the goal (or when there is no goal) they are squeezed into the full bar
// instead, so the bar always shows the balance and never overflows.
function sourceSegments(
  keys: EnergyKey[],
  totals: Record<string, number>,
  calorieGoal: number | null,
): { key: EnergyKey; kcal: number; width: string }[] {
  const parts = keys.map((key) => ({ key, kcal: Math.max(0, totals[key] || 0) * KCAL_PER_GRAM[key] }));
  const sum = parts.reduce((s, p) => s + p.kcal, 0);
  const base = Math.max(calorieGoal && calorieGoal > 0 ? calorieGoal : 0, sum);
  return parts.map((p) => ({ ...p, width: pct(base > 0 ? (p.kcal / base) * 100 : 0) }));
}

function EditButton({ onEdit }: { onEdit: () => void }) {
  const { t } = useTranslation('dashboard');
  return (
    <button
      type="button"
      data-testid="today-edit"
      aria-label={t('todayPanel.editGoal')}
      title={t('todayPanel.editGoal')}
      onClick={onEdit}
      className="-m-1 inline-flex size-8 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-white/[0.06] hover:text-foreground"
    >
      <svg aria-hidden="true" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M12 20h9" /><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4Z" />
      </svg>
    </button>
  );
}

function CalorieHeader({ goal, eaten, onEdit }: { goal: DayGoal; eaten: number; onEdit?: () => void }) {
  const { t } = useTranslation('dashboard');
  const calorieGoal = goal.calories && goal.calories > 0 ? goal.calories : null;
  const remaining = calorieGoal != null ? calorieGoal - eaten : null;
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <span data-testid="today-eaten" className="font-display text-3xl font-bold leading-none tabular-nums text-macro-kcal">
          {formatNumber(eaten)}
        </span>
        {calorieGoal != null ? (
          <span data-testid="today-goal" className="text-sm tabular-nums text-muted-foreground">
            {t('todayPanel.ofGoal', { goal: formatNumber(calorieGoal) })}
          </span>
        ) : (
          <span className="text-sm text-muted-foreground">kcal</span>
        )}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {remaining != null && (
          <span data-testid="today-remaining" className="text-sm font-semibold tabular-nums text-[#c3ccdd]">
            {remaining >= 0
              ? t('todayPanel.kcalLeft', { amount: formatNumber(remaining) })
              : t('todayPanel.kcalOver', { amount: formatNumber(-remaining) })}
          </span>
        )}
        {onEdit && <EditButton onEdit={onEdit} />}
      </div>
    </div>
  );
}

function AdjustedChip({ goal }: { goal: DayGoal }) {
  const { t } = useTranslation('dashboard');
  return (
    <p
      data-testid="today-adjusted"
      className="inline-flex w-fit max-w-full items-center gap-1.5 rounded-full border border-primary/30 bg-primary/10 px-2.5 py-1 text-xs text-foreground"
    >
      <svg aria-hidden="true" width="11" height="11" viewBox="0 0 24 24" fill="currentColor" className="shrink-0 text-primary">
        <path d="M13 2 4 14h7l-1 8 9-12h-7Z" />
      </svg>
      <span className="min-w-0 truncate">
        {goal.reason ? t('todayPanel.adjustedReason', { reason: goal.reason }) : t('todayPanel.adjusted')}
      </span>
      {goal.baseCalories != null && (
        <span className="shrink-0 tabular-nums text-muted-foreground">
          · {t('todayPanel.base', { calories: formatNumber(goal.baseCalories) })}
        </span>
      )}
    </p>
  );
}

function SourceBar({ keys, totals, calorieGoal }: { keys: EnergyKey[]; totals: Record<string, number>; calorieGoal: number | null }) {
  const { t } = useTranslation('dashboard');
  const segments = sourceSegments(keys, totals, calorieGoal);
  const aria = segments
    .map((s) => t('todayPanel.sourceItem', { macro: label(s.key), kcal: formatNumber(Math.round(s.kcal)) }))
    .join(', ');
  return (
    <div
      data-testid="today-source-bar"
      role="img"
      aria-label={`${t('todayPanel.sourceBar')}: ${aria}`}
      className="flex h-3 overflow-hidden rounded-full bg-black/35"
    >
      {segments.map((s) => (
        <div
          key={s.key}
          data-segment={s.key}
          className={cn('h-full transition-[width] duration-700 ease-out', BG_COLOR[s.key])}
          style={{ width: s.width }}
        />
      ))}
    </div>
  );
}

function MacroCell({ macroKey, eaten, goal }: { macroKey: string; eaten: number; goal: number | undefined }) {
  const { t } = useTranslation('dashboard');
  return (
    <div data-testid={`today-macro-${macroKey}`} className="min-w-0">
      <div className={cn('font-display text-[11px] font-bold uppercase tracking-wider', TEXT_COLOR[macroKey])}>{label(macroKey)}</div>
      <div className="mt-0.5 text-sm font-bold tabular-nums text-[#c3ccdd]">
        {goal ? t('todayPanel.gramsOfGoal', { eaten, goal }) : t('todayPanel.grams', { eaten })}
      </div>
    </div>
  );
}

function FiberRow({ eaten, goal }: { eaten: number; goal: number | undefined }) {
  const { t } = useTranslation('dashboard');
  const fill = goal ? Math.min(100, Math.max(0, (eaten / goal) * 100)) : 0;
  return (
    <div data-testid="today-fiber" className="flex items-center gap-3">
      <span className={cn('font-display text-[11px] font-bold uppercase tracking-wider', TEXT_COLOR.fiber)}>{label('fiber')}</span>
      {goal ? (
        <div
          role="progressbar"
          aria-label={label('fiber')}
          aria-valuemin={0}
          aria-valuenow={eaten}
          aria-valuemax={goal}
          className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full bg-black/35"
        >
          <div className={cn('h-full rounded-full transition-[width] duration-700 ease-out', BG_COLOR.fiber)} style={{ width: pct(fill) }} />
        </div>
      ) : (
        <div className="flex-1" />
      )}
      <span className="shrink-0 text-sm font-bold tabular-nums text-[#c3ccdd]">
        {goal ? t('todayPanel.gramsOfGoal', { eaten, goal }) : t('todayPanel.grams', { eaten })}
      </span>
    </div>
  );
}

// Calorie-compact, macro-first: the budget is a header line, the macros carry
// the panel. Colour never signals a miss here: a min-goal that is not reached
// yet is no failure at midday and a limit that is exceeded is shown by its
// numbers alone.
export default function TodayPanel({ goal, eatenCalories, macroTotals, onEdit }: Props) {
  const { t } = useTranslation('dashboard');
  // Sugar stays a Settings option and is never part of this panel.
  const shown = goal.enabledMacros.filter((k) => k !== 'sugar');
  const energyKeys = ENERGY_KEYS.filter((k) => shown.includes(k));
  const showFiber = shown.includes('fiber');
  const calorieGoal = goal.calories && goal.calories > 0 ? goal.calories : null;

  if (!goal.caloriesEnabled && shown.length === 0) {
    return (
      <p className="text-center text-sm text-muted-foreground py-6">
        {t('dashboard.noNutrientsTracked')} <a href="/settings">{t('dashboard.enableTrackingLink')}</a>
      </p>
    );
  }

  const eaten = (key: string) => macroTotals[key] || 0;

  return (
    <section className="surface flex flex-col gap-3 p-5" aria-label={t('todayPanel.title')}>
      {goal.caloriesEnabled ? (
        <CalorieHeader goal={goal} eaten={eatenCalories} onEdit={onEdit} />
      ) : (
        onEdit && (
          <div className="flex justify-end">
            <EditButton onEdit={onEdit} />
          </div>
        )
      )}
      {goal.adjusted && <AdjustedChip goal={goal} />}
      {energyKeys.length > 0 && <SourceBar keys={energyKeys} totals={macroTotals} calorieGoal={calorieGoal} />}
      {energyKeys.length > 0 && (
        <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(${energyKeys.length}, minmax(0, 1fr))` }}>
          {energyKeys.map((key) => (
            <MacroCell key={key} macroKey={key} eaten={eaten(key)} goal={goal.macroGoals[key]} />
          ))}
        </div>
      )}
      {showFiber && <FiberRow eaten={eaten('fiber')} goal={goal.macroGoals.fiber} />}
    </section>
  );
}
