import { useTranslation } from 'react-i18next';
import type { EntryComponent, EntryDetail } from '@/types';
import { MACRO_KEYS, MACRO_LABELS, type MacroKey } from '@/lib/macros';
import { energySplit, entryMacroTotals, formatAmount, isIncomplete, sumMacros } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { Ring } from '@/components/ui/Ring';
import { cn } from '@/lib/utils';

const TEXT_COLOR: Record<string, string> = {
  kcal: 'text-macro-kcal',
  protein: 'text-macro-protein',
  carbs: 'text-macro-carbs',
  fat: 'text-macro-fat',
  fiber: 'text-macro-fiber',
  sugar: 'text-macro-sugar',
};

const BAR_COLOR: Record<string, string> = {
  protein: 'bg-macro-protein',
  carbs: 'bg-macro-carbs',
  fat: 'bg-macro-fat',
};

const NO_STATUS = { statusClass: '', statusText: '' };

interface Props {
  entry: EntryDetail;
  caloriesEnabled: boolean;
}

// The meal in one card: calories, how the energy splits across protein, carbs
// and fat, and every macro including fiber and sugar. Fiber stays out of the
// energy split on purpose (its energy is not 4 kcal/g and part of it already
// sits inside the carbohydrate figure) but gets a tile of its own.
export default function MealSummary({ entry, caloriesEnabled }: Props) {
  const { t } = useTranslation('dashboard');
  const totals = entryMacroTotals(entry.macros);
  const sums = sumMacros(entry.components);
  const split = energySplit(totals);

  return (
    <section className="surface flex flex-col gap-5 p-5" aria-label={t('meal.summary.title')}>
      <div className="flex items-center gap-5">
        {caloriesEnabled && (
          <Ring
            macroKey="kcal"
            label={t('entries.caloriesLabel')}
            value={entry.calories}
            goal={null}
            unit="kcal"
            status={NO_STATUS}
            size={112}
          />
        )}
        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <p className="text-[11px] font-bold uppercase tracking-[0.08em] text-muted-foreground">
            {t('meal.summary.energySplit')}
          </p>
          {split ? (
            <>
              <div
                className="flex h-2.5 overflow-hidden rounded-full bg-black/35"
                role="img"
                aria-label={(['protein', 'carbs', 'fat'] as const)
                  .map((k) => `${MACRO_LABELS[k].label} ${formatNumber(Math.round(split[k] * 100))}%`)
                  .join(', ')}
              >
                {(['protein', 'carbs', 'fat'] as const).map((k) => (
                  <div key={k} className={cn('h-full', BAR_COLOR[k])} style={{ width: `${split[k] * 100}%` }} />
                ))}
              </div>
              <ul className="flex flex-wrap gap-x-3 gap-y-1 text-[12px] tabular-nums">
                {(['protein', 'carbs', 'fat'] as const).map((k) => (
                  <li key={k} className="inline-flex items-center gap-1.5">
                    <span className={cn('size-2 rounded-full', BAR_COLOR[k])} aria-hidden="true" />
                    <span className="text-muted-foreground">{MACRO_LABELS[k].label}</span>
                    <span className="font-bold text-foreground">{formatNumber(Math.round(split[k] * 100))}%</span>
                  </li>
                ))}
              </ul>
            </>
          ) : (
            <p className="text-sm text-muted-foreground">{t('meal.summary.noEnergySplit')}</p>
          )}
        </div>
      </div>

      <dl className="grid grid-cols-2 gap-2 sm:grid-cols-5">
        {MACRO_KEYS.map((key) => (
          <MacroTile
            key={key}
            macroKey={key}
            value={totals[key]}
            known={sums[key].known}
            total={sums[key].total}
            incomplete={isIncomplete(sums[key])}
            components={entry.components}
          />
        ))}
      </dl>
    </section>
  );
}

function MacroTile({ macroKey, value, known, total, incomplete, components }: {
  macroKey: MacroKey;
  value: number | null;
  known: number;
  total: number;
  incomplete: boolean;
  components: EntryComponent[];
}) {
  const { t } = useTranslation('dashboard');
  const label = MACRO_LABELS[macroKey].label;
  // A direct entry has no components to ask, so it is never "incomplete".
  const hint = incomplete && components.length > 0
    ? t('meal.summary.incomplete', { known, total })
    : undefined;

  return (
    <div
      className="flex min-w-0 flex-col gap-0.5 rounded-[14px] border border-white/[0.07] bg-white/[0.03] px-3 py-2.5"
      title={hint}
      data-testid={`meal-total-${macroKey}`}
    >
      <dt className={cn('truncate text-[11px] font-bold uppercase tracking-[0.08em]', TEXT_COLOR[macroKey])}>{label}</dt>
      <dd className="m-0 flex items-baseline gap-1 tabular-nums">
        <span className="text-lg font-bold text-foreground">{value == null ? '–' : formatAmount(value)}</span>
        {value != null && <span className="text-xs text-muted-foreground/60">g</span>}
        {hint && (
          <span className="ml-auto text-xs font-bold text-warning" aria-label={hint}>≥</span>
        )}
      </dd>
    </div>
  );
}
