import { useTranslation } from 'react-i18next';
import { MAX_GRAMS, MIN_GRAMS, parseGrams } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { cn } from '@/lib/utils';

interface Props {
  value: string;
  onChange: (value: string) => void;
  /** Enter was pressed. */
  onEnter: () => void;
  /** Name of the food, for the accessible label. */
  name: string;
  testId: string;
  autoFocus?: boolean;
}

/** The grams field of the catalog pick and the barcode scan: same input, same limits, same "g" suffix. */
export function GramsInput({ value, onChange, onEnter, name, testId, autoFocus }: Props) {
  const { t } = useTranslation('dashboard');
  return (
    <div className="relative flex-1">
      <input
        type="text"
        inputMode="decimal"
        aria-label={t('foodSearch.gramsLabel', { name })}
        className={cn(
          'h-10 w-full rounded-md border border-input bg-muted/50 px-3 py-2 pr-8 text-sm text-foreground outline-none transition-colors focus:border-ring focus:ring-1 focus:ring-ring tabular-nums',
          parseGrams(value) == null && 'border-destructive',
        )}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault();
            onEnter();
          }
        }}
        data-testid={testId}
        autoFocus={autoFocus}
      />
      <span className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-[10px] text-muted-foreground">g</span>
    </div>
  );
}

/** Says why the grams are refused; renders nothing while they are fine. */
export function InvalidGramsHint({ value, className }: { value: string; className?: string }) {
  const { t } = useTranslation('dashboard');
  if (parseGrams(value) != null) return null;
  return (
    <p className={cn('m-0 text-xs text-destructive', className)} role="alert">
      {t('foodSearch.invalidGrams', { min: MIN_GRAMS, max: formatNumber(MAX_GRAMS) })}
    </p>
  );
}
