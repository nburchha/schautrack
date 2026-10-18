import { Suspense, useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { searchFoods } from '@/api/foods';
import type { Food } from '@/types';
import { DEFAULT_GRAMS, foodPortion, formatAmount, parseGrams } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { Button } from '@/components/ui/Button';
import { cn } from '@/lib/utils';
import { GramsInput, InvalidGramsHint } from './GramsInput';
import { lazyRoute } from '@/lib/lazyRoute';

// Lazy-loaded: the scanner pulls in the quagga2 barcode decoder, which is only
// fetched when someone first opens it.
const BarcodeScanModal = lazyRoute(() => import('@/pages/Dashboard/BarcodeScanModal'));

const MIN_QUERY = 2;
const DEBOUNCE_MS = 250;

const inputClass =
  'h-10 w-full rounded-md border border-input bg-muted/50 px-3 py-2 text-sm text-foreground outline-none transition-colors focus:border-ring focus:ring-1 focus:ring-ring placeholder:text-muted-foreground/50';

interface Props {
  /** Called with the chosen food and grams. The search resets once it resolves; a rejection keeps the selection. */
  onPick: (food: Food, grams: number) => Promise<void> | void;
  /** Label of the confirm button, e.g. "Add". */
  pickLabel: string;
  autoFocus?: boolean;
  /** Show a barcode scanner next to the search. A scanned product is picked like a search hit. */
  barcode?: boolean;
}

/**
 * Catalog search → pick a food → enter grams. Results update while typing and
 * the previous ones stay on screen until the next ones arrive, so the list
 * never flashes empty between keystrokes.
 */
export function FoodSearch({ onPick, pickLabel, autoFocus, barcode }: Props) {
  const { t } = useTranslation('dashboard');
  const [input, setInput] = useState('');
  const [term, setTerm] = useState('');
  const [selected, setSelected] = useState<Food | null>(null);
  const [grams, setGrams] = useState(String(DEFAULT_GRAMS));
  const [picking, setPicking] = useState(false);
  const [scanOpen, setScanOpen] = useState(false);
  // Once opened the scanner stays mounted so its close-cleanup keeps running;
  // it is not mounted, and its chunk not fetched, before the first open.
  const [scanEverOpened, setScanEverOpened] = useState(false);
  useEffect(() => {
    if (!barcode) {
      setScanOpen(false);
      setScanEverOpened(false);
    }
  }, [barcode]);

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
    setGrams(String(DEFAULT_GRAMS));
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
    const kcal = parsedGrams == null ? null : foodPortion(selected, parsedGrams).kcal;
    return (
      <div className="flex flex-col gap-3" data-testid="food-search-selected">
        <div className="min-w-0">
          <p className="m-0 truncate text-[15px] font-semibold text-foreground" title={selected.name}>{selected.name}</p>
          <p className="m-0 mt-0.5 text-xs text-muted-foreground tabular-nums">
            {t('foodSearch.per100g', { kcal: formatNumber(Math.round(selected.calories_per_100g ?? 0)) })}
          </p>
        </div>

        <div className="flex items-center gap-2">
          <GramsInput
            value={grams}
            onChange={setGrams}
            onEnter={() => void confirm()}
            name={selected.name}
            testId="food-search-grams"
            autoFocus
          />
          <span className="w-24 text-right text-sm tabular-nums text-muted-foreground" data-testid="food-search-kcal">
            {kcal == null ? '–' : t('foodSearch.portionKcal', { kcal: formatAmount(kcal) })}
          </span>
        </div>
        <InvalidGramsHint value={grams} />

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
      <div className="flex items-center gap-2">
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
        {barcode && (
          <button
            type="button"
            className="flex size-10 shrink-0 cursor-pointer items-center justify-center rounded-md border border-input bg-muted/50 text-muted-foreground transition-colors hover:text-foreground"
            onClick={() => { setScanEverOpened(true); setScanOpen(true); }}
            aria-label={t('entries.scanBarcodeLabel')}
            title={t('entries.scanBarcodeLabel')}
            data-testid="food-search-scan"
          >
            <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
              <path d="M3 5v-2h4" /><path d="M17 3h4v2" /><path d="M21 19v2h-4" /><path d="M7 21h-4v-2" />
              <path d="M7 8v8" /><path d="M12 8v8" /><path d="M17 8v8" /><path d="M5 8v8" /><path d="M15 8v8" /><path d="M19 8v8" />
            </svg>
          </button>
        )}
      </div>

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

      {barcode && scanEverOpened && (
        <Suspense fallback={null}>
          <BarcodeScanModal
            isOpen={scanOpen}
            onClose={() => setScanOpen(false)}
            onPick={async (food, grams) => {
              // A rejection leaves the scanner open on the product, so a retry is one tap.
              await onPick(food, grams);
              setScanOpen(false);
            }}
            pickLabel={pickLabel}
          />
        </Suspense>
      )}
    </div>
  );
}

export default FoodSearch;
