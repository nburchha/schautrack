import { useState } from 'react';
import { Link, useParams, useSearchParams } from 'react-router';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useTranslation } from 'react-i18next';
import { useRequireAuth } from '@/hooks/useAuth';
import { deleteComponent, getEntry, updateComponentGrams, type EntryDetailResponse } from '@/api/entries';
import { useDashboardStore } from '@/stores/dashboardStore';
import { useToastStore } from '@/stores/toastStore';
import { kcalShares } from '@/lib/components';
import { formatDate } from '@/lib/format';
import { QueryError } from '@/components/ui/QueryError';
import { SectionLabel } from '@/components/ui/SectionLabel';
import { Button } from '@/components/ui/Button';
import { ApiError } from '@/api/client';
import MealSummary from './MealSummary';
import ComponentCard from './ComponentCard';

export default function Meal() {
  const { t } = useTranslation('dashboard');
  const queryClient = useQueryClient();
  const addToast = useToastStore((s) => s.addToast);
  const { user, isLoading: authLoading } = useRequireAuth();
  const { id } = useParams();
  const [searchParams] = useSearchParams();
  const currentLabel = useDashboardStore((s) => s.currentLabel);
  const [busy, setBusy] = useState(false);

  const entryId = Number(id);
  // ?user= is only set when the list was showing a linked account's day.
  const rawUser = Number(searchParams.get('user'));
  const viewUserId = Number.isInteger(rawUser) && rawUser > 0 && rawUser !== user?.id ? rawUser : undefined;
  const canEdit = viewUserId === undefined;
  const queryKey = ['entry', entryId, viewUserId ?? null];

  const { data, isLoading, isError, error, isFetching, refetch } = useQuery({
    queryKey,
    queryFn: () => getEntry(entryId, viewUserId),
    enabled: !!user && Number.isInteger(entryId) && entryId > 0,
    // A missing or foreign entry will not appear on retry.
    retry: (count, err) => !(err instanceof ApiError && err.status === 404) && count < 2,
  });

  const afterWrite = (res: EntryDetailResponse) => {
    queryClient.setQueryData(queryKey, res);
    // See EntryList: the SSE echo only reaches this tab when the write hit the
    // instance holding its stream, so refresh the day views locally too.
    queryClient.invalidateQueries({ queryKey: ['dashboard'] });
    queryClient.invalidateQueries({ queryKey: ['day-entries'] });
  };

  const run = async (action: () => Promise<EntryDetailResponse>, failure: string) => {
    setBusy(true);
    try {
      afterWrite(await action());
    } catch (err) {
      addToast('error', err instanceof Error ? err.message : failure);
    }
    setBusy(false);
  };

  const back = (
    <Link
      to="/dashboard"
      className="inline-flex w-fit items-center gap-1.5 rounded-full border border-white/10 bg-white/[0.04] px-3 py-1.5 text-[13px] text-muted-foreground no-underline transition-colors hover:border-white/20 hover:text-foreground"
      data-testid="meal-back"
    >
      <svg aria-hidden="true" width="13" height="13" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
        <path d="M15 18l-6-6 6-6" />
      </svg>
      {t('meal.back')}
    </Link>
  );

  if (isError && error instanceof ApiError && error.status === 404) {
    return (
      <div className="flex flex-col gap-3">
        {back}
        <p className="py-8 text-center text-sm text-muted-foreground">{t('meal.notFound')}</p>
      </div>
    );
  }
  if (isError && !data) {
    return <QueryError error={error} onRetry={() => refetch()} retrying={isFetching} />;
  }
  if (authLoading || isLoading || !data) {
    return <div className="flex items-center justify-center py-12"><div className="size-6 rounded-full border-2 border-primary border-t-transparent animate-spin" /></div>;
  }

  const { entry, caloriesEnabled } = data;
  const components = entry.components;
  const shares = kcalShares(components);

  return (
    <div className="flex flex-col gap-3">
      <header className="flex flex-col gap-3 px-1 pt-1">
        {back}
        <div className="min-w-0">
          <h1 className="m-0 truncate font-display text-2xl font-bold text-foreground" data-testid="meal-title">
            {entry.name || t('meal.untitled')}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            <span className="tabular-nums">{formatDate(`${entry.date}T00:00:00`, undefined, { weekday: 'short', month: 'short', day: 'numeric' })}</span>
            {' · '}
            <span className="tabular-nums">{entry.local_time}</span>
            {!canEdit && <> · {t('meal.sharedBy', { label: currentLabel })}</>}
          </p>
        </div>
      </header>

      <MealSummary entry={entry} caloriesEnabled={caloriesEnabled} />

      <section className="surface p-4">
        <SectionLabel
          right={canEdit ? (
            <Button asChild size="sm" variant="outline">
              <Link to={`/entries/${entry.id}/add`} className="no-underline" data-testid="meal-add-component">
                <span aria-hidden="true">+</span> {t('meal.components.add')}
              </Link>
            </Button>
          ) : undefined}
        >
          {t('meal.components.title')}
          {components.length > 0 && <span className="ml-1.5 font-normal text-muted-foreground">({components.length})</span>}
        </SectionLabel>

        {components.length === 0 ? (
          <div className="rounded-[14px] border border-dashed border-white/10 px-4 py-6 text-center" data-testid="meal-direct">
            <p className="m-0 text-sm font-semibold text-foreground">{t('meal.direct.title')}</p>
            <p className="m-0 mt-1 text-sm text-muted-foreground">{t('meal.direct.body')}</p>
          </div>
        ) : (
          <ul className="m-0 flex list-none flex-col gap-2.5 p-0">
            {components.map((c, i) => (
              <ComponentCard
                key={c.id}
                component={c}
                share={shares[i]}
                canEdit={canEdit}
                busy={busy}
                onChangeGrams={(grams) => run(() => updateComponentGrams(entry.id, c.id, grams), t('meal.toastUpdateFailed'))}
                onDelete={() => run(() => deleteComponent(entry.id, c.id), t('meal.toastDeleteFailed'))}
              />
            ))}
          </ul>
        )}
        {canEdit && components.length === 1 && (
          <p className="m-0 mt-2.5 px-1 text-xs text-muted-foreground">{t('meal.lastComponentHint')}</p>
        )}
      </section>
    </div>
  );
}
