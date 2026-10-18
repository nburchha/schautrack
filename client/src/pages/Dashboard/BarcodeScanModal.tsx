import { useState, useRef, useCallback, useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import * as Dialog from '@radix-ui/react-dialog';
import Quagga from '@ericblade/quagga2';
import { lookupBarcode } from '@/api/barcode';
import { useAuth } from '@/hooks/useAuth';
import { MACRO_LABELS, getEnabledMacros } from '@/lib/macros';
import { DEFAULT_GRAMS, foodPortion, formatAmount, parseGrams, servingGrams } from '@/lib/components';
import { formatNumber } from '@/lib/format';
import { Button } from '@/components/ui/Button';
import { GramsInput, InvalidGramsHint } from '@/components/FoodSearch/GramsInput';
import { cn } from '@/lib/utils';
import type { Food } from '@/types';

interface Props {
  isOpen: boolean;
  onClose: () => void;
  /**
   * Called with the scanned product, now a catalog food, and the grams to use.
   * The scan makes a component, never a whole meal. A rejection keeps the
   * product on screen; the caller reports the failure.
   */
  onPick: (food: Food, grams: number) => Promise<void> | void;
  /** Label of the confirm button, e.g. "Add". */
  pickLabel: string;
}

type Phase = 'scanning' | 'loading' | 'result' | 'error';
type Mode = 'camera' | 'upload' | 'manual';

interface ProductData {
  food: Food;
  servingSize?: string | null;
  /** The serving in grams as the grams field accepts it; null when OFF has none or an unusable one. */
  serving: number | null;
  /** Open Food Facts was unreachable; these are the values stored by an earlier scan. */
  cached: boolean;
}

const DEFAULT_GRAMS_TEXT = String(DEFAULT_GRAMS);

export default function BarcodeScanModal({ isOpen, onClose, onPick, pickLabel }: Props) {
  const { t } = useTranslation('dashboard');
  const { user } = useAuth();
  const enabledMacros = getEnabledMacros(user?.macrosEnabled ?? {});
  const [phase, setPhase] = useState<Phase>('scanning');
  const [mode, setMode] = useState<Mode>('camera');
  const [barcode, setBarcode] = useState('');
  const [manualCode, setManualCode] = useState('');
  const [product, setProduct] = useState<ProductData | null>(null);
  const [grams, setGrams] = useState(DEFAULT_GRAMS_TEXT);
  const [picking, setPicking] = useState(false);
  const [errorMsg, setErrorMsg] = useState('');
  const [cameraAvailable, setCameraAvailable] = useState(true);
  const [scannerReady, setScannerReady] = useState(false);
  const scannerRef = useRef<HTMLDivElement>(null);
  const quaggaRunning = useRef(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  // Quagga2 fires onDetected several times per second and is prone to
  // single-read errors (especially the EAN-13 check digit). Require two
  // consecutive identical reads before triggering a lookup.
  const lastCodeRef = useRef<{ code: string; count: number }>({ code: '', count: 0 });

  // Incremented on every stopScanner so an in-flight Quagga.init (camera
  // permission prompt open while the modal closes) knows it went stale and
  // must shut the camera down instead of starting the scanner.
  const scanSessionRef = useRef(0);

  // Incremented on close, retry and every new lookup so a response that lands
  // after the modal moved on is dropped instead of written into a reset modal.
  const lookupGenRef = useRef(0);

  const stopScanner = useCallback(() => {
    scanSessionRef.current++;
    // Always detach the detection handler — it is registered before init
    // finishes, so it can exist while quaggaRunning is still false.
    Quagga.offDetected();
    if (quaggaRunning.current) {
      Quagga.stop();
      quaggaRunning.current = false;
    }
  }, []);

  const doLookup = useCallback(async (code: string) => {
    const generation = ++lookupGenRef.current;
    setBarcode(code);
    setPhase('loading');
    setErrorMsg('');
    stopScanner();

    try {
      const res = await lookupBarcode(code);
      if (generation !== lookupGenRef.current) return;
      if (!res.ok) {
        setErrorMsg(res.error || t('barcodeScan.errorProductNotFound'));
        setPhase('error');
        return;
      }
      // Without calories the product is not stored, so there is no food to pick.
      if (res.caloriesPer100g == null || !res.food) {
        setErrorMsg(res.note || t('barcodeScan.errorNoCalorieData'));
        setProduct(null);
        setPhase('error');
        return;
      }
      const serving = servingGrams(res.servingQuantity);
      setProduct({ food: res.food, servingSize: res.servingSize, serving, cached: !!res.cached });
      setGrams(String(serving ?? DEFAULT_GRAMS));
      setPhase('result');
    } catch {
      if (generation !== lookupGenRef.current) return;
      setErrorMsg(t('barcodeScan.errorLookupFailed'));
      setPhase('error');
    }
  }, [stopScanner, t]);

  const startScanner = useCallback(() => {
    if (!scannerRef.current || quaggaRunning.current) return;
    const session = scanSessionRef.current;

    Quagga.init(
      {
        inputStream: {
          type: 'LiveStream',
          target: scannerRef.current,
          constraints: {
            facingMode: 'environment',
            width: { ideal: 640 },
            height: { ideal: 480 },
          },
        },
        decoder: {
          readers: ['ean_reader', 'ean_8_reader', 'upc_reader', 'upc_e_reader'],
        },
        locate: true,
      },
      (err: Error | null) => {
        if (session !== scanSessionRef.current) {
          // Scanner was stopped (modal closed / mode switched) while init
          // was acquiring the camera — release it instead of starting.
          if (!err) Quagga.stop();
          return;
        }
        if (err) {
          setCameraAvailable(false);
          return;
        }
        Quagga.start();
        quaggaRunning.current = true;
        setScannerReady(true);
      }
    );

    Quagga.onDetected((data) => {
      const code = data.codeResult?.code;
      if (!code || !/^\d{8,13}$/.test(code)) return;
      if (lastCodeRef.current.code === code) {
        lastCodeRef.current.count += 1;
        if (lastCodeRef.current.count >= 2) {
          doLookup(code);
        }
      } else {
        lastCodeRef.current = { code, count: 1 };
      }
    });
  }, [doLookup]);

  useEffect(() => {
    if (isOpen && phase === 'scanning' && mode === 'camera' && cameraAvailable) {
      const timer = setTimeout(startScanner, 100);
      return () => clearTimeout(timer);
    }
    return undefined;
  }, [isOpen, phase, mode, cameraAvailable, startScanner]);

  useEffect(() => {
    if (!isOpen) {
      stopScanner();
      lookupGenRef.current++;
      setPhase('scanning');
      setMode('camera');
      setBarcode('');
      setManualCode('');
      setProduct(null);
      setGrams(DEFAULT_GRAMS_TEXT);
      setErrorMsg('');
      setCameraAvailable(true);
      setScannerReady(false);
      lastCodeRef.current = { code: '', count: 0 };
    }
  }, [isOpen, stopScanner]);

  const handleFileChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0];
    if (!file) return;
    setErrorMsg('');

    const src = URL.createObjectURL(file);

    Quagga.decodeSingle(
      {
        src,
        numOfWorkers: 0,
        decoder: {
          readers: ['ean_reader', 'ean_8_reader', 'upc_reader', 'upc_e_reader'],
        },
        locate: true,
      },
      (result) => {
        URL.revokeObjectURL(src);
        const code = result?.codeResult?.code;
        if (code && /^\d{8,13}$/.test(code)) {
          doLookup(code);
        } else {
          setErrorMsg(t('barcodeScan.errorNoBarcodeInImage'));
        }
      }
    );

    // Reset file input so same file can be re-selected
    if (fileInputRef.current) fileInputRef.current.value = '';
  };

  const handleManualSubmit = (e: React.SubmitEvent) => {
    e.preventDefault();
    const code = manualCode.trim();
    if (/^\d{8,13}$/.test(code)) {
      doLookup(code);
    } else {
      setErrorMsg(t('barcodeScan.errorInvalidBarcode'));
    }
  };

  const handleModeSwitch = (newMode: Mode) => {
    if (newMode === mode) return;
    stopScanner();
    setMode(newMode);
    setErrorMsg('');
  };

  const parsedGrams = parseGrams(grams);

  // The same arithmetic as the saved component row. Unknown values stay unknown
  // (null): a product without a fiber figure shows a dash, not "0 g".
  const portion = product && parsedGrams != null ? foodPortion(product.food, parsedGrams) : null;

  const handlePick = async () => {
    if (!product || parsedGrams == null || picking) return;
    setPicking(true);
    try {
      await onPick(product.food, parsedGrams);
    } catch {
      // The caller reports the failure; stay on the product so a retry is one tap.
    } finally {
      setPicking(false);
    }
  };

  const handleRetry = () => {
    lookupGenRef.current++;
    setPhase('scanning');
    setBarcode('');
    setManualCode('');
    setProduct(null);
    setGrams(DEFAULT_GRAMS_TEXT);
    setErrorMsg('');
    lastCodeRef.current = { code: '', count: 0 };
  };

  const handleServingPreset = (g: number) => {
    setGrams(String(g));
  };

  return (
    <Dialog.Root open={isOpen} onOpenChange={(open) => { if (!open) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-[300] bg-black/90 sm:bg-black/60 sm:backdrop-blur-sm" />
        <Dialog.Content data-modal-layer="scanner" className="fixed inset-0 z-[300] bg-black flex flex-col overflow-hidden sm:overflow-y-auto sm:inset-auto sm:inset-x-4 sm:top-1/2 sm:-translate-y-1/2 sm:mx-auto sm:max-w-md sm:max-h-[90vh] sm:rounded-xl sm:border sm:border-border sm:bg-card">
          <div className="flex items-center justify-between px-4 py-2 border-b border-border bg-card/80 sm:bg-transparent shrink-0 z-10">
            <Dialog.Title className="text-sm font-semibold text-foreground">{t('barcodeScan.modalTitle')}</Dialog.Title>
            <Dialog.Close aria-label={t('dashboard.closeAriaLabel')} className="size-8 flex items-center justify-center rounded-md border border-destructive/30 bg-destructive/10 text-destructive hover:bg-destructive/20 transition-colors cursor-pointer">
              <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <path d="M18 6L6 18" /><path d="M6 6l12 12" />
              </svg>
            </Dialog.Close>
          </div>

          <div className="flex flex-col gap-3 flex-1 min-h-0 p-4 sm:flex-initial bg-card sm:bg-transparent">
            {/* Mode tabs */}
            {phase === 'scanning' && (
              <div className="flex gap-1 rounded-md bg-muted p-1">
                {(['camera', 'upload', 'manual'] as const).map((m) => (
                  <button
                    key={m}
                    type="button"
                    className={cn(
                      'flex-1 rounded-md px-3 py-1.5 text-xs font-medium transition-colors cursor-pointer',
                      mode === m
                        ? 'bg-card text-foreground shadow-sm'
                        : 'text-muted-foreground hover:text-foreground'
                    )}
                    onClick={() => handleModeSwitch(m)}
                  >
                    {m === 'camera' ? t('dashboard.cameraTab') : m === 'upload' ? t('dashboard.uploadTab') : t('barcodeScan.manualTab')}
                  </button>
                ))}
              </div>
            )}

            {/* Scanning phase */}
            {phase === 'scanning' && (
              <>
                {mode === 'camera' && (
                  cameraAvailable ? (
                    <div
                      ref={scannerRef}
                      className={cn(
                        "relative rounded-md overflow-hidden bg-black/30 flex-1 sm:flex-initial min-h-[240px] [&_canvas]:hidden",
                        scannerReady ? '[&_video]:w-full [&_video]:h-full [&_video]:object-cover [&_video]:block' : '[&_video]:opacity-0 [&_video]:absolute'
                      )}
                    >
                      {!scannerReady && (
                        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 z-10">
                          <div className="relative size-10">
                            <div className="absolute inset-0 rounded-full border-2 border-primary/20" />
                            <div className="absolute inset-0 rounded-full border-2 border-primary border-t-transparent animate-spin" />
                          </div>
                          <span className="text-xs text-muted-foreground">{t('barcodeScan.startingCamera')}</span>
                        </div>
                      )}
                      {scannerReady && <div className="absolute inset-x-8 top-1/2 -translate-y-1/2 h-0.5 bg-primary/60 z-10 pointer-events-none" />}
                    </div>
                  ) : (
                    <div className="flex flex-col items-center gap-2 py-6 text-muted-foreground">
                      <p className="text-sm">{t('barcodeScan.cameraNotAvailable')}</p>
                    </div>
                  )
                )}

                {mode === 'upload' && (
                  <div className="flex flex-col items-center gap-3 py-8 text-muted-foreground">
                    <p className="text-sm">{t('barcodeScan.uploadPhotoText')}</p>
                    <input
                      ref={fileInputRef}
                      type="file"
                      accept="image/*"
                      className="text-sm file:mr-2 file:rounded-md file:border-0 file:bg-primary file:px-3 file:py-1.5 file:text-xs file:font-medium file:text-white file:cursor-pointer"
                      onChange={handleFileChange}
                    />
                  </div>
                )}

                {mode === 'manual' && (
                  <form onSubmit={handleManualSubmit} className="flex gap-2 py-4">
                    <input
                      type="text"
                      className="flex-1 rounded-md border border-input bg-muted/50 px-3 py-2 text-sm text-foreground outline-none transition-colors focus:border-ring focus:ring-1 focus:ring-ring placeholder:text-muted-foreground/50"
                      value={manualCode}
                      onChange={(e) => { setManualCode(e.target.value); setErrorMsg(''); }}
                      placeholder={t('barcodeScan.enterBarcodePlaceholder')}
                      inputMode="numeric"
                      maxLength={13}
                      autoFocus
                    />
                    <Button type="submit" size="sm">{t('barcodeScan.lookUpButton')}</Button>
                  </form>
                )}

                {errorMsg && (
                  <div className="text-center text-sm text-destructive">{errorMsg}</div>
                )}
              </>
            )}

            {/* Loading phase */}
            {phase === 'loading' && (
              <div className="flex flex-1 flex-col items-center justify-center gap-4 py-12">
                <div className="relative size-12">
                  <div className="absolute inset-0 rounded-full border-2 border-primary/20" />
                  <div className="absolute inset-0 rounded-full border-2 border-primary border-t-transparent animate-spin" />
                </div>
                <span className="text-sm font-medium text-muted-foreground animate-pulse">{t('barcodeScan.lookingUpText', { barcode })}</span>
              </div>
            )}

            {/* Result phase */}
            {phase === 'result' && product && (
              <>
                <div className="flex flex-col items-center gap-1 py-2">
                  <div className="text-base font-semibold text-foreground text-center" data-testid="barcode-product-name">{product.food.name}</div>
                  <div className="text-xs text-muted-foreground">
                    {t('barcodeScan.calPer100g', { value: formatNumber(Math.round(product.food.calories_per_100g ?? 0)) })}
                    {product.servingSize && t('barcodeScan.servingSizeSuffix', { size: product.servingSize })}
                  </div>
                </div>

                {product.cached && (
                  <p className="m-0 text-center text-xs text-muted-foreground" data-testid="barcode-cached-note">{t('barcodeScan.cachedNote')}</p>
                )}

                {/* Amount selector */}
                <div className="flex items-center gap-2">
                  <GramsInput
                    value={grams}
                    onChange={setGrams}
                    onEnter={() => void handlePick()}
                    name={product.food.name}
                    testId="barcode-grams"
                  />
                  <div className="flex gap-1">
                    {product.serving != null && product.serving !== DEFAULT_GRAMS && (
                      <button
                        type="button"
                        className={`rounded-md px-2.5 py-2 text-xs font-medium transition-colors cursor-pointer border ${parsedGrams === product.serving ? 'border-primary bg-primary/10 text-primary' : 'border-border bg-transparent text-muted-foreground hover:text-foreground hover:border-primary/50'}`}
                        onClick={() => handleServingPreset(product.serving ?? DEFAULT_GRAMS)}
                      >
                        {product.servingSize || `${product.serving}g`}
                      </button>
                    )}
                    <button
                      type="button"
                      className={`rounded-md px-2.5 py-2 text-xs font-medium transition-colors cursor-pointer border ${parsedGrams === DEFAULT_GRAMS ? 'border-primary bg-primary/10 text-primary' : 'border-border bg-transparent text-muted-foreground hover:text-foreground hover:border-primary/50'}`}
                      onClick={() => handleServingPreset(DEFAULT_GRAMS)}
                    >
                      {DEFAULT_GRAMS}g
                    </button>
                  </div>
                </div>
                <InvalidGramsHint value={grams} className="text-center" />

                {/* Computed values. OFF is crowdsourced, so a missing figure shows as a dash. */}
                <div className="flex flex-col items-center gap-1">
                  <div className="text-2xl font-bold text-primary tabular-nums" data-testid="barcode-kcal">{portion ? formatAmount(portion.kcal) : '–'} cal</div>
                  <div className="flex gap-4 mt-1">
                    {enabledMacros.map((key) => {
                      const val = portion?.macros[key];
                      const label = MACRO_LABELS[key];
                      return (
                        <div key={key} className="flex flex-col items-center">
                          <span className="text-sm font-semibold tabular-nums">{val == null ? '–' : `${formatAmount(val)}g`}</span>
                          <span className="text-xs uppercase tracking-wider text-muted-foreground">{label?.label || key}</span>
                        </div>
                      );
                    })}
                  </div>
                </div>

                <div className="flex gap-2 justify-center">
                  <Button size="sm" onClick={() => void handlePick()} disabled={parsedGrams == null} loading={picking} data-testid="barcode-pick">{pickLabel}</Button>
                  <Button size="sm" variant="ghost" onClick={handleRetry} disabled={picking}>{t('barcodeScan.scanAnotherButton')}</Button>
                </div>
              </>
            )}

            {/* Attribution */}
            {(phase === 'result' || phase === 'scanning') && (
              <div className="text-center text-[10px] text-muted-foreground">
                {t('barcodeScan.attributionPrefix')}{' '}
                <a href="https://world.openfoodfacts.org" target="_blank" rel="noopener noreferrer" className="underline hover:text-muted-foreground">
                  {t('barcodeScan.attributionLinkText')}
                </a>
                {' '}&middot; ODbL
              </div>
            )}

            {/* Error phase */}
            {phase === 'error' && (
              <>
                <div className="text-center py-4">
                  <div className="text-sm text-destructive">{errorMsg}</div>
                </div>
                <div className="flex gap-2 justify-center">
                  <Button size="sm" onClick={handleRetry}>{t('barcodeScan.tryAgainButton')}</Button>
                  <Button size="sm" variant="ghost" onClick={onClose}>{t('dashboard.closeButton')}</Button>
                </div>
              </>
            )}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
