import { Link, useParams } from 'react-router';
import { useTranslation } from 'react-i18next';

// Placeholder for the food search. The meal page links here today so the route
// exists; the catalog search (work package 4) plugs into this page later and
// adds the chosen food to the entry.
export default function AddComponent() {
  const { t } = useTranslation('dashboard');
  const { id } = useParams();

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
      <section className="surface p-6 text-center" data-testid="add-component-placeholder">
        <p className="m-0 text-sm text-muted-foreground">{t('meal.addPage.soon')}</p>
      </section>
    </div>
  );
}
