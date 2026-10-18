import { useState, useCallback, useEffect, useRef, Suspense } from 'react';
import { useTranslation } from 'react-i18next';
import { useQueryClient } from '@tanstack/react-query';
import type { AIUsage } from '@/types';
import { createEntry } from '@/api/entries';
import { useAuth } from '@/hooks/useAuth';
import { useAuthStore } from '@/stores/authStore';
import { createSavedFood } from '@/api/savedFoods';
import { MACRO_LABELS, computeCaloriesFromMacros } from '@/lib/macros';
import { parseAmount } from '@/lib/mathParser';
import { Button } from '@/components/ui/Button';
import { FoodSearch } from '@/components/FoodSearch/FoodSearch';
import { MAX_DRAFT_COMPONENTS, draftName, draftPayload, draftTotals, type DraftComponent } from '@/lib/draft';
import { formatAmount } from '@/lib/components';
import { todayIn, toEatenAt } from '@/lib/eatenAt';
import { cn } from '@/lib/utils';
import type { Food } from '@/types';
import { QuantityStepper } from '@/components/ui/QuantityStepper';
import { useToastStore } from '@/stores/toastStore';
import { lazyRoute } from '@/lib/lazyRoute';

// Lazy-loaded so its chunk is only fetched when a user first opens it, rather
// than eagerly on every page load.
const AIPhotoModal = lazyRoute(() => import('./AIPhotoModal'));

interface Props {
  selectedDate: string;
  caloriesEnabled: boolean;
  autoCalcCalories: boolean;
  enabledMacros: string[];
  hasAiEnabled: boolean;
  aiUsage: AIUsage | null;
  aiProviderName: string | null;
  onSubmit: () => void;
}

const inputClass = 'w-full rounded-md border border-input bg-muted/50 px-3 py-2 text-sm text-foreground outline-none transition-colors focus:border-ring focus:ring-1 focus:ring-ring placeholder:text-muted-foreground/50';

// Secondary entry methods (photo, barcode) and save-as-quick-add. Same
// vocabulary as .surface — hairline edge plus a 1px inner top highlight —
// at tile scale, so they read as part of the card rather than as leftover
// bordered boxes next to the primary action.
const actionTileClass =
  'grid size-10 shrink-0 place-items-center rounded-xl bg-white/[0.04] text-muted-foreground cursor-pointer ' +
  'shadow-[0_0_0_1px_rgba(255,255,255,0.07),inset_0_1px_0_rgba(255,255,255,0.06)] ' +
  'transition-colors hover:bg-white/[0.07] hover:text-foreground ' +
  'disabled:cursor-not-allowed disabled:opacity-40 disabled:hover:bg-white/[0.04] disabled:hover:text-muted-foreground';

/** A past day has no "now" to fall back on, so the time starts at a plausible morning hour. */
const PAST_DAY_TIME = '09:00';

/** Grams as typed: "12,5" and "12.5" both work. null when the server would refuse the value. */
function parseDraftGrams(raw: string): number | null {
  const n = Number(raw.trim().replace(',', '.'));
  const rounded = Math.round(n * 100) / 100;
  return Number.isFinite(n) && rounded >= 0.01 && rounded <= 10000 ? rounded : null;
}

function DraftRow({ item, onGrams, onRemove }: {
  item: DraftComponent;
  onGrams: (grams: number) => void;
  onRemove: () => void;
}) {
  const { t } = useTranslation('dashboard');
  const [text, setText] = useState(String(item.grams));
  const parsed = parseDraftGrams(text);
  const kcal = ((item.food.calories_per_100g ?? 0) * (parsed ?? item.grams)) / 100;

  return (
    <li className="flex items-center gap-2 py-2" data-testid="draft-component">
      <span className="min-w-0 flex-1 truncate text-sm text-foreground" title={item.food.name}>{item.food.name}</span>
      <label className="flex items-center gap-1">
        <span className="sr-only">{t('entries.draftGramsLabel', { name: item.food.name })}</span>
        <input
          className={cn(
            'h-8 w-16 rounded-md border bg-muted/50 px-2 text-right text-sm tabular-nums text-foreground outline-none focus:border-ring',
            parsed == null ? 'border-destructive' : 'border-input',
          )}
          inputMode="decimal"
          value={text}
          onChange={(e) => {
            setText(e.target.value);
            const g = parseDraftGrams(e.target.value);
            if (g != null) onGrams(g);
          }}
          onKeyDown={(e) => { if (e.key === 'Enter') e.preventDefault(); }}
          data-testid="draft-component-grams"
        />
        <span className="text-xs text-muted-foreground">g</span>
      </label>
      <span className="w-16 text-right text-sm tabular-nums text-muted-foreground">{formatAmount(kcal)} kcal</span>
      <button
        type="button"
        className="size-7 shrink-0 cursor-pointer rounded-[10px] border border-destructive/30 bg-destructive/10 text-destructive transition-colors hover:bg-destructive/20"
        onClick={onRemove}
        aria-label={t('entries.draftRemoveLabel', { name: item.food.name })}
        data-testid="draft-component-remove"
      >
        <svg aria-hidden="true" className="mx-auto" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <path d="M18 6L6 18" /><path d="M6 6l12 12" />
        </svg>
      </button>
    </li>
  );
}

export default function EntryForm({ selectedDate, caloriesEnabled, autoCalcCalories, enabledMacros, hasAiEnabled, aiUsage, aiProviderName, onSubmit }: Props) {
  const { t } = useTranslation('dashboard');
  const barcodeEnabled = useAuthStore((s) => s.barcodeEnabled);
  const [name, setName] = useState('');
  const [amount, setAmount] = useState('');
  const [macros, setMacros] = useState<Record<string, string>>({});
  const [quantity, setQuantity] = useState(1);
  const [loading, setLoading] = useState(false);
  const [date, setDate] = useState(selectedDate);
  const [aiModalOpen, setAiModalOpen] = useState(false);
  const [savingFood, setSavingFood] = useState(false);
  const [localAiUsage, setLocalAiUsage] = useState<AIUsage | null>(aiUsage);
  const [draft, setDraft] = useState<DraftComponent[]>([]);
  const [time, setTime] = useState('');
  const timeTouched = useRef(false);
  const draftKey = useRef(0);
  const queryClient = useQueryClient();
  const { user } = useAuth();
  const tz = user?.timezone;

  // Once a modal has been opened it stays mounted (kept alive by these latches)
  // so its existing close-cleanup effects keep running; it just isn't mounted —
  // and its chunk isn't fetched — until the first open.
  const aiModalMounted = useRef(false);
  if (aiModalOpen) aiModalMounted.current = true;

  useEffect(() => {
    setLocalAiUsage(aiUsage);
  }, [aiUsage]);
  const addToast = useToastStore((s) => s.addToast);

  useEffect(() => {
    setDate(selectedDate);
  }, [selectedDate]);

  // Today needs no time: the entry falls back to the moment it is created. Any
  // other day does, so it starts at a default the user can change or clear.
  // Once the user has touched the picker their choice stays.
  useEffect(() => {
    if (timeTouched.current) return;
    setTime(date === todayIn(tz) ? '' : PAST_DAY_TIME);
  }, [date, tz]);

  const hasDraft = draft.length > 0;
  const totals = hasDraft ? draftTotals(draft) : null;

  const addToDraft = (food: Food, grams: number) => {
    if (draft.length >= MAX_DRAFT_COMPONENTS) {
      addToast('error', t('entries.draftFull', { max: MAX_DRAFT_COMPONENTS }));
      throw new Error('draft full');
    }
    setDraft((prev) => [...prev, { key: ++draftKey.current, food, grams }]);
  };

  const safeParseMacro = (val: string | undefined): number => {
    if (!val) return 0;
    const n = parseInt(val, 10);
    return Number.isFinite(n) ? n : 0;
  };

  const computedCalories = autoCalcCalories
    ? computeCaloriesFromMacros(
        safeParseMacro(macros.protein),
        safeParseMacro(macros.carbs),
        safeParseMacro(macros.fat)
      )
    : null;

  const handleMacroChange = useCallback((key: string, value: string) => {
    setMacros((prev) => ({ ...prev, [key]: value }));
  }, []);

  const handleSubmit = async (e: React.SubmitEvent) => {
    e.preventDefault();
    setLoading(true);

    const qty = Math.max(1, Math.trunc(quantity));
    const data: Parameters<typeof createEntry>[0] = { entry_date: date };
    if (time) data.eaten_at = toEatenAt(date, time, tz);
    if (name.trim()) data.entry_name = name.trim();
    if (hasDraft) {
      // The server computes calories and macros from the components.
      data.components = draftPayload(draft);
      if (!data.entry_name) data.entry_name = draftName(draft);
    } else if (amount && !autoCalcCalories) {
      const parsed = parseAmount(amount, { maxAbs: 100000 });
      if (!parsed.ok) {
        addToast('error', t('entries.toastInvalidCalorieAmount'));
        setLoading(false);
        return;
      }
      data.amount = parsed.value * qty;
    }
    for (const key of hasDraft ? [] : enabledMacros) {
      if (macros[key]) {
        const parsed = Number(macros[key]);
        if (!Number.isFinite(parsed)) {
          addToast('error', t('entries.toastInvalidMacroValue', { macro: key }));
          setLoading(false);
          return;
        }
        const macroKey = `${key}_g` as keyof typeof data;
        (data as Record<string, unknown>)[macroKey] = parsed * qty;
      }
    }

    try {
      await createEntry(data);
      setName('');
      setAmount('');
      setMacros({});
      setQuantity(1);
      setDraft([]);
      timeTouched.current = false;
      setTime(date === todayIn(tz) ? '' : PAST_DAY_TIME);
      onSubmit();
      addToast('success', t('entries.toastTracked'));
    } catch (err) {
      addToast('error', err instanceof Error ? err.message : t('entries.toastTrackFailed'));
    }
    setLoading(false);
  };

  const applyResult = (result: { name?: string; calories?: number; macros?: Record<string, number> }) => {
    if (result.name) setName(result.name);
    if (result.calories) setAmount(String(result.calories));
    if (result.macros) {
      const newMacros: Record<string, string> = {};
      for (const [key, val] of Object.entries(result.macros)) {
        if (enabledMacros.includes(key) && val) {
          newMacros[key] = String(val);
        }
      }
      setMacros(newMacros);
    }
  };

  const hasDirectInput = !!(amount || computedCalories || Object.values(macros).some((v) => v));
  const hasInput = hasDraft || hasDirectInput;
  const canSaveAsFood = !!(name.trim() && hasDirectInput && !hasDraft);
  const aiDisabled = hasAiEnabled && localAiUsage && localAiUsage.limit > 0 && localAiUsage.remaining === 0;

  const showQtyPreview = quantity > 1 && enabledMacros.some((k) => macros[k]);

  const handleSaveAsFood = async () => {
    if (!canSaveAsFood || savingFood) return;
    setSavingFood(true);
    try {
      const payload: Parameters<typeof createSavedFood>[0] = { name: name.trim() };
      if (amount && !autoCalcCalories) {
        const parsed = parseAmount(amount, { maxAbs: 100000 });
        if (parsed.ok) payload.amount = parsed.value;
      } else if (computedCalories) {
        payload.amount = computedCalories;
      }
      for (const key of enabledMacros) {
        const raw = macros[key];
        if (raw) {
          const n = Number(raw);
          if (Number.isFinite(n)) {
            (payload as Record<string, unknown>)[`${key}_g`] = n;
          }
        }
      }
      await createSavedFood(payload);
      queryClient.invalidateQueries({ queryKey: ['savedFoods'] });
      addToast('success', t('entries.toastSavedFood', { name: name.trim() }));
    } catch (err) {
      addToast('error', err instanceof Error ? err.message : t('entries.toastSaveFoodFailed'));
    }
    setSavingFood(false);
  };

  return (
    <>
      {/* Catalog search sits outside the <form> so Enter in it never submits the entry. */}
      <div className="mb-3 flex flex-col gap-2 pt-1" data-testid="entry-form-search">
        <FoodSearch onPick={addToDraft} pickLabel={t('entries.addToDraft')} barcode={barcodeEnabled} />
        {hasDraft && (
          <ul className="m-0 flex list-none flex-col divide-y divide-divider p-0" data-testid="draft-list">
            {draft.map((item) => (
              <DraftRow
                key={item.key}
                item={item}
                onGrams={(grams) => setDraft((prev) => prev.map((d) => (d.key === item.key ? { ...d, grams } : d)))}
                onRemove={() => setDraft((prev) => prev.filter((d) => d.key !== item.key))}
              />
            ))}
          </ul>
        )}
      </div>

      <form onSubmit={handleSubmit} className="flex flex-col">
        {/* Food name + Quantity */}
        <div className="mb-3 flex items-end gap-2">
          <div className="flex flex-1 flex-col">
            <input
              className={inputClass}
              type="text"
              aria-label={t('entries.foodNameAriaLabel')}
              placeholder={t('entries.foodNamePlaceholder')}
              value={name}
              onChange={(e) => setName(e.target.value)}
              onBlur={() => setName((n) => n.trim())}
              maxLength={120}
            />
          </div>
          {!hasDraft && (
            <div className="flex flex-col gap-1">
              <label className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">{t('entries.qtyLabel')}</label>
              <QuantityStepper value={quantity} onChange={setQuantity} />
            </div>
          )}
        </div>

        {/* Calories */}
        {caloriesEnabled && (
          <div className="mb-3 flex flex-col gap-1">
            <label htmlFor="entry-calories" className="text-xs font-semibold uppercase tracking-wider text-macro-kcal">{t('entries.caloriesLabel')}</label>
            <input
              id="entry-calories"
              className={`${inputClass} ${autoCalcCalories || hasDraft ? 'opacity-60 cursor-not-allowed' : ''}`}
              type="text"
              inputMode="tel"
              placeholder="0"
              value={totals ? Math.round(totals.kcal) : autoCalcCalories ? (computedCalories ?? '') : amount}
              onChange={(e) => setAmount(e.target.value)}
              readOnly={autoCalcCalories || hasDraft}
            />
          </div>
        )}

        {/* Macros grid */}
        {enabledMacros.length > 0 && (
          <div
            className="grid gap-2 mb-3"
            style={{ gridTemplateColumns: `repeat(${enabledMacros.length <= 3 ? enabledMacros.length : Math.ceil(enabledMacros.length / 2)}, 1fr)` }}
          >
            {enabledMacros.map((key) => {
              const color = {
                protein: 'text-macro-protein',
                carbs: 'text-macro-carbs',
                fat: 'text-macro-fat',
                fiber: 'text-macro-fiber',
                sugar: 'text-macro-sugar',
              }[key] || 'text-muted-foreground';

              return (
                <div key={key} className="flex flex-col gap-1">
                  <label htmlFor={`entry-macro-${key}`} className={`text-xs font-semibold uppercase tracking-wider ${color}`}>
                    {MACRO_LABELS[key as keyof typeof MACRO_LABELS]?.label || key}
                  </label>
                  <input
                    id={`entry-macro-${key}`}
                    className={`${inputClass} ${hasDraft ? 'opacity-60 cursor-not-allowed' : ''}`}
                    type="text"
                    inputMode="numeric"
                    placeholder="0"
                    value={totals ? (totals.macros[key as keyof typeof totals.macros] != null ? Math.round(totals.macros[key as keyof typeof totals.macros] ?? 0) : '') : macros[key] || ''}
                    onChange={(e) => handleMacroChange(key, e.target.value)}
                    readOnly={hasDraft}
                  />
                </div>
              );
            })}
          </div>
        )}

        {/* Multiplier preview */}
        {showQtyPreview && (
          <div className="mb-3 text-xs text-muted-foreground tabular-nums">
            {t('entries.qtyMacrosPreview', { count: quantity })}
          </div>
        )}

        {/* Date + AI + Submit */}
        <div className="flex flex-col gap-2 sm:flex-row sm:items-center">
          <div className="flex flex-wrap items-center gap-2">
            <input
              type="date"
              aria-label={t('entries.entryDateAriaLabel')}
              className="rounded-md border border-input bg-muted/50 px-2 py-1.5 text-sm text-foreground outline-none focus:border-ring focus:ring-1 focus:ring-ring"
              value={date}
              onChange={(e) => setDate(e.target.value)}
            />
            <div className="flex items-center gap-1" data-testid="entry-form-time">
              <input
                type="time"
                aria-label={t('entries.timeLabel')}
                className="rounded-md border border-input bg-muted/50 px-2 py-1.5 text-sm text-foreground outline-none focus:border-ring focus:ring-1 focus:ring-ring"
                value={time}
                onChange={(e) => { timeTouched.current = true; setTime(e.target.value); }}
                data-testid="entry-form-time-input"
              />
              {time && (
                <button
                  type="button"
                  className="size-7 shrink-0 cursor-pointer rounded-[10px] border border-border text-muted-foreground transition-colors hover:text-foreground"
                  onClick={() => { timeTouched.current = true; setTime(''); }}
                  aria-label={t('entries.timeClearLabel')}
                  data-testid="entry-form-time-clear"
                >
                  <svg aria-hidden="true" className="mx-auto" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                    <path d="M18 6L6 18" /><path d="M6 6l12 12" />
                  </svg>
                </button>
              )}
            </div>
          </div>

          <div className="flex items-center gap-2 sm:ml-auto">
            {hasAiEnabled && (
              <button
                type="button"
                className={`relative ${actionTileClass}`}
                onClick={() => setAiModalOpen(true)}
                disabled={!!aiDisabled || hasDraft}
                aria-label={t('entries.estimateAiAriaLabel')}
                title={aiDisabled ? t('entries.aiLimitReachedTitle') : t('entries.estimateAiTitle')}
              >
                <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M9.937 15.5A2 2 0 0 0 8.5 14.063l-6.135-1.582a.5.5 0 0 1 0-.962L8.5 9.936A2 2 0 0 0 9.937 8.5l1.582-6.135a.5.5 0 0 1 .963 0L14.063 8.5A2 2 0 0 0 15.5 9.937l6.135 1.582a.5.5 0 0 1 0 .963L15.5 14.063a2 2 0 0 0-1.437 1.437l-1.582 6.135a.5.5 0 0 1-.963 0z" />
                  <path d="M20 3v4" /><path d="M22 5h-4" />
                </svg>
                {localAiUsage && localAiUsage.limit > 0 && (
                  <span className="absolute -bottom-0.5 -right-0.5 rounded-full bg-background px-1 text-[10px] font-semibold tabular-nums leading-tight text-muted-foreground">
                    {localAiUsage.remaining}
                  </span>
                )}
              </button>
            )}

            <button
              type="button"
              className={actionTileClass}
              onClick={handleSaveAsFood}
              disabled={!canSaveAsFood || savingFood}
              aria-label={t('entries.saveAsQuickAddLabel')}
              title={canSaveAsFood ? t('entries.saveAsTemplateTitle') : t('entries.addNameAndValuesFirstTitle')}
            >
              {savingFood ? (
                <span className="size-4 animate-spin rounded-full border-2 border-current border-t-transparent" />
              ) : (
                <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <path d="M19 21l-7-5-7 5V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2z" />
                </svg>
              )}
            </button>

            <Button
              type="submit"
              size="default"
              loading={loading}
              disabled={!hasInput}
              className={`flex-1 sm:flex-initial border-0 font-semibold ${
                hasInput
                  ? 'bg-gradient-to-br from-secondary to-primary text-primary-foreground shadow-[0_4px_16px_rgba(109,140,255,0.35)] hover:brightness-110'
                  : 'bg-white/[0.04] text-muted-foreground shadow-[inset_0_0_0_1px_rgba(255,255,255,0.07)]'
              }`}
            >
              {t('entries.trackButton')}
            </Button>
          </div>
        </div>
      </form>

      {aiModalMounted.current && (
        <Suspense fallback={null}>
          <AIPhotoModal
            isOpen={aiModalOpen}
            onClose={() => setAiModalOpen(false)}
            onResult={(result) => {
              applyResult(result);
              setLocalAiUsage((u) => u && u.limit > 0 ? { ...u, used: u.used + 1, remaining: Math.max(0, u.remaining - 1) } : u);
              setAiModalOpen(false);
            }}
            enabledMacros={enabledMacros}
            providerName={aiProviderName}
          />
        </Suspense>
      )}
    </>
  );
}
