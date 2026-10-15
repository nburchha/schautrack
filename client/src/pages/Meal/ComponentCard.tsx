import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import type { EntryComponent } from '@/types';
import { MACRO_KEYS, MACRO_LABELS } from '@/lib/macros';
import { formatAmount, portionNutrients, roundAmount } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { MacroPill } from '@/components/ui/MacroPill';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/utils';

interface Props {
  component: EntryComponent;
  /** This component's share of the meal's calories, 0..1. */
  share: number;
  canEdit: boolean;
  busy: boolean;
  onChangeGrams: (grams: number) => Promise<void>;
  onDelete: () => Promise<void>;
}

/** Accepts "12,5" as well as "12.5". NaN when it is not a number. */
function parseGrams(raw: string): number {
  return Number(raw.trim().replace(',', '.'));
}

export default function ComponentCard({ component, share, canEdit, busy, onChangeGrams, onDelete }: Props) {
  const { t } = useTranslation('dashboard');
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const portion = portionNutrients(component);
  const percent = Math.round(share * 100);

  const startEdit = () => {
    if (!canEdit || busy) return;
    setDraft(String(component.grams));
    setEditing(true);
  };

  const commit = async () => {
    setEditing(false);
    const grams = parseGrams(draft);
    if (!Number.isFinite(grams) || grams <= 0 || grams === component.grams) return;
    await onChangeGrams(grams);
  };

  return (
    <li
      className="rounded-[16px] border border-white/[0.07] bg-white/[0.03] p-3.5"
      data-testid="meal-component"
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <p className="truncate text-[15px] font-semibold text-foreground" title={component.name}>{component.name}</p>
          <p className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
            <span className="tabular-nums">
              {t('meal.components.per100g', { kcal: formatNumber(Math.round(component.calories_per_100g)) })}
            </span>
            {component.food_id == null && (
              <span className="rounded-full border border-white/10 px-1.5 py-px text-[10px] font-bold uppercase tracking-wider">
                {t('meal.components.adHoc')}
              </span>
            )}
          </p>
        </div>

        <div className="flex shrink-0 flex-col items-end">
          <span className="text-lg font-bold leading-none tabular-nums text-macro-kcal" data-testid="meal-component-kcal">
            {formatAmount(portion.kcal)}
          </span>
          <span className="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/70">kcal</span>
        </div>
      </div>

      <div className="mt-3 flex items-center gap-2">
        {editing ? (
          <label className="inline-flex items-center gap-1.5">
            <span className="sr-only">{t('meal.components.gramsLabel', { name: component.name })}</span>
            <input
              className="h-9 w-24 rounded-md border border-ring bg-muted/50 px-2 text-sm tabular-nums text-foreground outline-none"
              inputMode="decimal"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={commit}
              onKeyDown={(e) => {
                if (e.key === 'Enter') e.currentTarget.blur();
                if (e.key === 'Escape') {
                  // Blur would commit the draft; drop it first.
                  setDraft(String(component.grams));
                  setEditing(false);
                }
              }}
              data-testid="meal-component-grams-input"
              autoFocus
            />
            <span className="text-sm text-muted-foreground">g</span>
          </label>
        ) : (
          <button
            type="button"
            onClick={startEdit}
            disabled={!canEdit || busy}
            aria-label={canEdit ? t('meal.components.editGrams', { name: component.name }) : undefined}
            className={cn(
              'inline-flex h-9 items-center gap-1 rounded-md border px-2.5 text-sm tabular-nums transition-colors',
              canEdit
                ? 'cursor-pointer border-white/10 bg-white/[0.04] hover:border-primary/40 hover:bg-primary/5'
                : 'cursor-default border-transparent px-0',
            )}
            data-testid="meal-component-grams"
          >
            <span className="font-bold text-foreground">{formatAmount(component.grams)}</span>
            <span className="text-muted-foreground">g</span>
          </button>
        )}

        <div className="flex min-w-0 flex-1 items-center gap-2" title={t('meal.components.share', { percent })}>
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-black/35" aria-hidden="true">
            <div
              className="h-full rounded-full bg-macro-kcal/80 transition-[width] duration-500"
              style={{ width: `${Math.min(100, share * 100)}%` }}
            />
          </div>
          <span className="w-9 shrink-0 text-right text-xs tabular-nums text-muted-foreground">{percent}%</span>
        </div>

        {canEdit && !confirmingDelete && (
          <button
            type="button"
            onClick={() => setConfirmingDelete(true)}
            disabled={busy}
            className="flex size-8 shrink-0 cursor-pointer items-center justify-center rounded-[10px] border border-destructive/30 bg-destructive/10 text-destructive transition-colors hover:bg-destructive/20 disabled:opacity-40"
            aria-label={t('meal.components.remove', { name: component.name })}
            title={t('meal.components.removeTitle')}
            data-testid="meal-component-remove"
          >
            <svg aria-hidden="true" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M18 6L6 18" /><path d="M6 6l12 12" />
            </svg>
          </button>
        )}
      </div>

      {confirmingDelete && (
        <div className="mt-3 flex flex-wrap items-center justify-between gap-2 rounded-[12px] border border-destructive/30 bg-destructive/10 px-3 py-2" role="alert">
          <span className="text-sm text-foreground">{t('meal.components.confirmRemove', { name: component.name })}</span>
          <span className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => setConfirmingDelete(false)}>{t('meal.components.keep')}</Button>
            <Button
              size="sm"
              variant="destructive"
              loading={busy}
              onClick={async () => { await onDelete(); setConfirmingDelete(false); }}
              data-testid="meal-component-remove-confirm"
            >
              {t('meal.components.confirmRemoveAction')}
            </Button>
          </span>
        </div>
      )}

      <div className="mt-2.5 flex flex-wrap gap-x-1 gap-y-0.5">
        {MACRO_KEYS.map((key) => (
          <MacroPill
            key={key}
            macroKey={key}
            label={MACRO_LABELS[key].label}
            value={portion.macros[key] == null ? null : roundAmount(portion.macros[key])}
            unit="g"
          />
        ))}
      </div>
    </li>
  );
}
