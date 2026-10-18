import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { getRecentGoals, resetDayGoal, setDayGoal, type GoalInput, type RecentGoal } from '@/api/goals';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';
import { Input } from '@/components/ui/Input';
import { Sheet } from '@/components/ui/Sheet';
import { formatNumber } from '@/lib/format';
import { computeCaloriesFromMacros, MACRO_KEYS, MACRO_LABELS, type MacroKey } from '@/lib/macros';
import type { DayGoal } from '@/types';

interface Props {
  open: boolean;
  onClose: () => void;
  /** The Tagesziel of the shown day; the fields start from it. */
  goal: DayGoal;
  /** Calories are derived from the macros (4P + 4C + 9F) and not editable. */
  autoCalcCalories: boolean;
}

type FieldKey = 'calories' | MacroKey;
type Values = Partial<Record<FieldKey, string>>;

const str = (n: number | null | undefined) => (n != null && n > 0 ? String(n) : '');

// Parsed field value; null for an empty or non-numeric field.
const num = (s: string | undefined): number | null => {
  const trimmed = (s ?? '').trim();
  if (trimmed === '') return null;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : null;
};

function initialValues(goal: DayGoal, macroKeys: MacroKey[]): Values {
  const values: Values = {};
  if (goal.caloriesEnabled) values.calories = str(goal.calories);
  for (const key of macroKeys) values[key] = str(goal.macroGoals[key]);
  return values;
}

function pickValues(pick: RecentGoal, macroKeys: MacroKey[], caloriesEnabled: boolean): Values {
  const values: Values = {};
  if (caloriesEnabled) values.calories = str(pick.calories);
  for (const key of macroKeys) values[key] = str(pick[key]);
  return values;
}

function PickSummary({ pick, macroKeys, caloriesEnabled }: { pick: RecentGoal; macroKeys: MacroKey[]; caloriesEnabled: boolean }) {
  const { t } = useTranslation('dashboard');
  const parts: string[] = [];
  if (caloriesEnabled && pick.calories != null) parts.push(t('goalAdjust.kcal', { amount: formatNumber(pick.calories) }));
  for (const key of macroKeys) {
    const v = pick[key];
    if (v != null && v > 0) parts.push(`${MACRO_LABELS[key].short} ${formatNumber(v)}`);
  }
  return <span className="text-xs tabular-nums text-muted-foreground">{parts.join(' · ')}</span>;
}

function AdjustmentForm({ onClose, goal, autoCalcCalories }: Omit<Props, 'open'>) {
  const { t } = useTranslation('dashboard');
  const queryClient = useQueryClient();
  const macroKeys = MACRO_KEYS.filter((k) => goal.enabledMacros.includes(k));
  const initial = initialValues(goal, macroKeys);
  const initialReason = goal.reason ?? '';

  const [values, setValues] = useState<Values>(initial);
  const [reason, setReason] = useState(initialReason);
  const [error, setError] = useState<string | null>(null);

  const { data: recent } = useQuery({
    queryKey: ['goal-recent'],
    queryFn: getRecentGoals,
    select: (d) => d.recent,
  });

  const showCalories = goal.caloriesEnabled;
  const derivedCalories = autoCalcCalories
    ? computeCaloriesFromMacros(num(values.protein) ?? 0, num(values.carbs) ?? 0, num(values.fat) ?? 0)
    : null;
  const caloriesShown = autoCalcCalories ? str(derivedCalories) : (values.calories ?? '');

  const dirty =
    reason !== initialReason ||
    macroKeys.some((k) => (values[k] ?? '') !== (initial[k] ?? '')) ||
    (showCalories && !autoCalcCalories && (values.calories ?? '') !== (initial.calories ?? ''));

  // A PUT replaces the day's adjustment and fills every unsent key from the
  // Basisziel, not from the adjustment being edited. Sending every field keeps
  // an already adjusted day's other values from silently reverting.
  const buildInput = (): GoalInput => {
    const input: GoalInput = {};
    if (showCalories) {
      const calories = autoCalcCalories ? derivedCalories : num(values.calories);
      if (calories != null) input.calories = calories;
    }
    // An emptied macro means "no goal" (0 on the wire).
    for (const key of macroKeys) input[key] = num(values[key]) ?? 0;
    const trimmed = reason.trim();
    if (trimmed) input.reason = trimmed;
    return input;
  };

  // The server rejects a request with nothing in it (no calories, no macro, no
  // reason), so there is nothing to save then; Reset is the way back.
  const sendable = Object.keys(buildInput()).length > 0;
  const canSave = dirty && sendable;

  const refreshAndClose = () => {
    void queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    void queryClient.invalidateQueries({ queryKey: ['day-entries'] });
    void queryClient.invalidateQueries({ queryKey: ['goal-recent'] });
    onClose();
  };

  const save = useMutation({
    mutationFn: () => setDayGoal(goal.date, buildInput()),
    onSuccess: refreshAndClose,
    onError: (err: Error) => setError(err.message || t('goalAdjust.saveFailed')),
  });

  const reset = useMutation({
    mutationFn: () => resetDayGoal(goal.date),
    onSuccess: refreshAndClose,
    onError: (err: Error) => setError(err.message || t('goalAdjust.resetFailed')),
  });

  const busy = save.isPending || reset.isPending;

  // A quick pick only fills the fields. Nothing is saved until the user does.
  const applyPick = (pick: RecentGoal) => {
    setValues(pickValues(pick, macroKeys, showCalories));
    setReason(pick.reason ?? '');
    setError(null);
  };

  const setField = (key: FieldKey, value: string) => setValues((v) => ({ ...v, [key]: value }));

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (canSave && !busy) {
          setError(null);
          save.mutate();
        }
      }}
    >
      {recent && recent.length > 0 && (
        <section className="flex flex-col gap-1.5" aria-label={t('goalAdjust.recentTitle')}>
          <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">{t('goalAdjust.recentTitle')}</h3>
          <ul className="flex flex-col gap-1.5">
            {recent.map((pick) => (
              <li key={`${pick.used_at}|${pick.reason ?? ''}|${pick.calories ?? ''}`}>
                <button
                  type="button"
                  data-testid="goal-adjust-pick"
                  onClick={() => applyPick(pick)}
                  className="flex w-full flex-col items-start gap-0.5 rounded-md border border-white/10 bg-white/[0.03] px-3 py-2 text-left transition-colors hover:border-white/20 hover:bg-white/[0.06]"
                >
                  <span className="max-w-full truncate text-sm font-semibold">{pick.reason ?? t('goalAdjust.noReason')}</span>
                  <PickSummary pick={pick} macroKeys={macroKeys} caloriesEnabled={showCalories} />
                </button>
              </li>
            ))}
          </ul>
        </section>
      )}

      <div className="grid grid-cols-2 gap-3">
        {showCalories && (
          <div className="col-span-2">
            <Input
              id="goal-adjust-calories-input"
              data-testid="goal-adjust-calories"
              label={t('goalAdjust.caloriesLabel')}
              type="text"
              inputMode="tel"
              autoComplete="off"
              value={caloriesShown}
              readOnly={autoCalcCalories}
              className={autoCalcCalories ? 'cursor-not-allowed opacity-60' : undefined}
              onChange={(e) => setField('calories', e.target.value)}
            />
            {autoCalcCalories && <p className="mt-1 text-xs text-muted-foreground">{t('goalAdjust.caloriesAuto')}</p>}
          </div>
        )}
        {macroKeys.map((key) => (
          <Input
            key={key}
            id={`goal-adjust-${key}-input`}
            data-testid={`goal-adjust-${key}`}
            label={t('goalAdjust.macroLabel', { macro: MACRO_LABELS[key].label })}
            type="text"
            inputMode="numeric"
            autoComplete="off"
            value={values[key] ?? ''}
            onChange={(e) => setField(key, e.target.value)}
          />
        ))}
        <div className="col-span-2">
          <Input
            id="goal-adjust-reason-input"
            data-testid="goal-adjust-reason"
            label={t('goalAdjust.reasonLabel')}
            type="text"
            maxLength={120}
            autoComplete="off"
            placeholder={t('goalAdjust.reasonPlaceholder')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
      </div>

      {error && <Alert data-testid="goal-adjust-error" message={error} />}

      <div className="flex flex-wrap items-center justify-between gap-2">
        {goal.adjusted ? (
          <Button
            type="button"
            variant="outline"
            data-testid="goal-adjust-reset"
            disabled={busy}
            loading={reset.isPending}
            onClick={() => {
              setError(null);
              reset.mutate();
            }}
          >
            {t('goalAdjust.reset')}
          </Button>
        ) : (
          <span />
        )}
        <div className="flex gap-2">
          <Button type="button" variant="ghost" onClick={onClose} disabled={busy}>
            {t('goalAdjust.cancel')}
          </Button>
          <Button type="submit" data-testid="goal-adjust-save" disabled={!canSave || busy} loading={save.isPending}>
            {t('goalAdjust.save')}
          </Button>
        </div>
      </div>
    </form>
  );
}

// Sheet unmounts its subtree when closed, so the form (and its field state)
// starts fresh from the day's Tagesziel every time it opens.
export default function AdjustmentSheet({ open, onClose, goal, autoCalcCalories }: Props) {
  const { t } = useTranslation('dashboard');
  return (
    <Sheet open={open} onClose={onClose} title={t('goalAdjust.title')}>
      <AdjustmentForm onClose={onClose} goal={goal} autoCalcCalories={autoCalcCalories} />
    </Sheet>
  );
}
