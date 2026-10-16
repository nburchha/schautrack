import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { searchFoods } from '@/api/foods';
import type { Food } from '@/types';
import { formatAmount } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { parseAmount } from '@/lib/mathParser';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/utils';

const MIN_QUERY = 2;
const DEBOUNCE_MS = 250;
const DEFAULT_GRAMS = '100';
const MIN_GRAMS = 0.01;
const MAX_GRAMS = 10000;

const inputClass =
  'h-10 w-full rounded-md border border-input bg-muted/50 px-3 py-2 text-sm text-foreground outline-none transition-colors focus:border-ring focus:ring-1 focus:ring-ring placeholder:text-muted-foreground/50';

/** "2*45" and "12,5" both work; the server stores hundredths of a gram. */
function parseGrams(raw: string): number | null {
  const parsed = parseAmount(raw.replace(/,/g, '.'), { maxAbs: MAX_GRAMS });
  if (!parsed.ok) return null;
  const grams = Math.round(parsed.value * 100) / 100;
  return grams >= MIN_GRAMS && grams <= MAX_GRAMS ? grams : null;
}

interface Props {
  /** Called with the chosen food and grams. The search resets once it resolves; a rejection keeps the selection. */
  onPick: (food: Food, grams: number) => Promise<void> | void;
  /** Label of the confirm button, e.g. "Add". */
  pickLabel: string;
  autoFocus?: boolean;
}

/**
 * Catalog search → pick a food → enter grams. Results update while typing and
 * the previous ones stay on screen until the next ones arrive, so the list
 * never flashes empty between keystrokes.
 */
export function FoodSearch({ onPick, pickLabel, autoFocus }: Props) {
  const { t } = useTranslation('dashboard');
  const [input, setInput] = useState('');
  const [term, setTerm] = useState('');
  const [selected, setSelected] = useState<Food | null>(null);
  const [grams, setGrams] = useState(DEFAULT_GRAMS);
  const [picking, setPicking] = useState(false);

  useEffect(() => {
    const trimmed = input.trim();
    const id = setTimeout(() => setTerm(trimmed), DEBOUNCE_MS);
    return () => clearTimeout(id);
  }, [input]);

  const active = term.length >= MIN_QUERY;
  const { data, isError, isFetching, refetch } = useQuery({
    queryKey: ['foods', term],
    queryFn: ({ signal }) => searchFoods(term, signal),
    enabled: active,
    staleTime: 60_000,
  });

  // The hits on screen. They are replaced when the next search lands rather than
  // dropped when the term changes, so the list does not flash empty between
  // keystrokes; but they are cleared when the search goes idle, so an old
  // search's hits never reappear under a new, unrelated one.
  const [foods, setFoods] = useState<Food[]>([]);
  useEffect(() => {
    if (!active) setFoods([]);
    else if (data) setFoods(data.foods);
  }, [active, data]);

  const parsedGrams = parseGrams(grams);

  const choose = (food: Food) => {
    setSelected(food);
    setGrams(DEFAULT_GRAMS);
  };

  const confirm = async () => {
    if (!selected || parsedGrams == null || picking) return;
    setPicking(true);
    try {
      await onPick(selected, parsedGrams);
      setSelected(null);
      setInput('');
      setTerm('');
    } catch {
      // The caller reports the failure; keep the food and grams so a retry is one tap.
    } finally {
      setPicking(false);
    }
  };

  if (selected) {
    const kcal = (selected.calories_per_100g ?? 0) * (parsedGrams ?? 0) / 100;
    return (
      <div className="flex flex-col gap-3" data-testid="food-search-selected">
        <div className="min-w-0">
          <p className="m-0 truncate text-[15px] font-semibold text-foreground" title={selected.name}>{selected.name}</p>
          <p className="m-0 mt-0.5 text-xs text-muted-foreground tabular-nums">
            {t('foodSearch.per100g', { kcal: formatNumber(Math.round(selected.calories_per_100g ?? 0)) })}
          </p>
        </div>

        <div className="flex items-center gap-2">
          <label className="flex flex-1 items-center gap-2">
            <span className="sr-only">{t('foodSearch.gramsLabel', { name: selected.name })}</span>
            <input
              className={cn(inputClass, 'tabular-nums', parsedGrams == null && 'border-destructive')}
              inputMode="decimal"
              value={grams}
              onChange={(e) => setGrams(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') {
                  e.preventDefault();
                  void confirm();
                }
              }}
              data-testid="food-search-grams"
              autoFocus
            />
            <span className="text-sm text-muted-foreground">g</span>
          </label>
          <span className="w-24 text-right text-sm tabular-nums text-muted-foreground" data-testid="food-search-kcal">
            {parsedGrams == null ? '–' : t('foodSearch.portionKcal', { kcal: formatAmount(kcal) })}
          </span>
        </div>
        {parsedGrams == null && (
          <p className="m-0 text-xs text-destructive" role="alert">
            {t('foodSearch.invalidGrams', { min: MIN_GRAMS, max: formatNumber(MAX_GRAMS) })}
          </p>
        )}

        <div className="flex items-center justify-between gap-2">
          <Button type="button" size="sm" variant="ghost" onClick={() => setSelected(null)} disabled={picking}>
            {t('foodSearch.back')}
          </Button>
          <Button type="button" size="sm" onClick={() => void confirm()} disabled={parsedGrams == null} loading={picking} data-testid="food-search-add">
            {pickLabel}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-2">
      <input
        className={inputClass}
        type="search"
        enterKeyHint="search"
        autoComplete="off"
        autoCorrect="off"
        spellCheck={false}
        aria-label={t('foodSearch.ariaLabel')}
        placeholder={t('foodSearch.placeholder')}
        value={input}
        onChange={(e) => setInput(e.target.value)}
        autoFocus={autoFocus}
        data-testid="food-search-input"
      />

      {!active && (
        <p className="m-0 px-1 text-xs text-muted-foreground" data-testid="food-search-hint">{t('foodSearch.hint', { min: MIN_QUERY })}</p>
      )}

      {active && isError && (
        <div className="flex items-center justify-between gap-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2" role="alert" data-testid="food-search-error">
          <span className="text-sm text-destructive">{t('foodSearch.error')}</span>
          <Button type="button" size="sm" variant="outline" onClick={() => void refetch()}>{t('foodSearch.retry')}</Button>
        </div>
      )}

      {active && !isError && data && data.foods.length === 0 && (
        <p className="m-0 px-1 py-2 text-sm text-muted-foreground" data-testid="food-search-empty">
          {t('foodSearch.noResults', { q: term })}
        </p>
      )}

      {active && !isError && foods.length > 0 && (
        <ul
          className={cn('m-0 flex max-h-72 list-none flex-col divide-y divide-divider overflow-y-auto p-0 transition-opacity', isFetching && 'opacity-60')}
          aria-busy={isFetching}
          data-testid="food-search-results"
        >
          {foods.map((food) => {
            const usable = food.calories_per_100g != null;
            return (
              <li key={food.id}>
                <button
                  type="button"
                  className="flex w-full min-w-0 cursor-pointer items-center gap-2 bg-transparent px-1 py-2.5 text-left transition-colors hover:bg-white/[0.03] disabled:cursor-not-allowed disabled:opacity-50"
                  onClick={() => choose(food)}
                  disabled={!usable}
                  data-testid="food-search-result"
                >
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[15px] text-foreground" title={food.name}>{food.name}</span>
                    <span className="block text-xs text-muted-foreground tabular-nums">
                      {usable
                        ? t('foodSearch.per100g', { kcal: formatNumber(Math.round(food.calories_per_100g ?? 0)) })
                        : t('foodSearch.noKcal')}
                    </span>
                  </span>
                  <span className="shrink-0 rounded-full border border-white/10 px-2 py-px text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
                    {t(`foodSearch.source.${food.source}`)}
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

export default FoodSearch;
