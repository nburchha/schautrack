import { Link, useNavigate, useParams } from 'react-router';
import { useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { addComponent } from '@/api/entries';
import { FoodSearch } from '@/components/FoodSearch/FoodSearch';
import { useToastStore } from '@/stores/toastStore';
import { useAuthStore } from '@/stores/authStore';
import type { Food } from '@/types';

// Search the catalog, pick a food, enter grams: the food becomes a component of
// this meal and its totals are recomputed by the server.
export default function AddComponent() {
  const { t } = useTranslation('dashboard');
  const { id } = useParams();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const addToast = useToastStore((s) => s.addToast);
  const entryId = Number(id);
  const barcodeEnabled = useAuthStore((s) => s.barcodeEnabled);

  const add = async (food: Food, grams: number) => {
    try {
      const res = await addComponent(entryId, { food_id: food.id, grams });
      // The meal page reads this key (own entry, no ?user=).
      queryClient.setQueryData(['entry', entryId, null], res);
      queryClient.invalidateQueries({ queryKey: ['dashboard'] });
      queryClient.invalidateQueries({ queryKey: ['day-entries'] });
      addToast('success', t('meal.addPage.toastAdded', { name: food.name }));
      navigate(`/entries/${id}`);
    } catch (err) {
      addToast('error', err instanceof Error ? err.message : t('meal.addPage.toastFailed'));
      throw err;
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <header className="flex flex-col gap-3 px-1 pt-1">
        <Link
          to={`/entries/${id}`}
          className="inline-flex w-fit items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.04] px-3 py-1.5 text-[13px] text-muted-foreground no-underline transition-colors hover:border-white/20 hover:text-foreground"
          data-testid="add-component-back"
        >
          <svg aria-hidden="true" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <path d="M15 18l-6-6 6-6" />
          </svg>
          {t('meal.addPage.back')}
        </Link>
        <h1 className="m-0 font-display text-2xl font-bold text-foreground">{t('meal.addPage.title')}</h1>
      </header>
      <section className="surface p-4" data-testid="add-component">
        <FoodSearch onPick={add} pickLabel={t('meal.addPage.add')} autoFocus barcode={barcodeEnabled} />
      </section>
    </div>
  );
}
