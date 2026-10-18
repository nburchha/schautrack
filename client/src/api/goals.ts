import { api } from './client';

/** Goal values of a day. null = no goal, or the account does not track it. */
export interface GoalValues {
  calories: number | null;
  protein: number | null;
  carbs: number | null;
  fat: number | null;
  fiber: number | null;
  sugar: number | null;
}

export interface DayGoalResult extends GoalValues {
  date: string;
  adjusted: boolean;
  reason: string | null;
  /** The Basisziel the adjustment replaced; null when the day is not adjusted. */
  base: GoalValues | null;
  updated_at: string | null;
}

/** An earlier adjustment, offered as a quick pick. */
export interface RecentGoal extends GoalValues {
  reason: string | null;
  used_at: string;
}

/**
 * Any of the value keys plus a reason; at least one is required. Keys that are
 * not sent are filled from the Basisziel by the server (not from a previous
 * adjustment of the same day), so a caller that edits an adjusted day has to
 * send every field it wants to keep.
 */
export type GoalInput = Partial<{ [K in keyof GoalValues]: number }> & { reason?: string };

export function setDayGoal(date: string, input: GoalInput) {
  return api<{ ok: boolean; goal: DayGoalResult }>(`/api/goals/${date}`, {
    method: 'PUT',
    body: JSON.stringify(input),
  });
}

export function resetDayGoal(date: string) {
  return api<{ ok: boolean; goal: DayGoalResult }>(`/api/goals/${date}/reset`, { method: 'POST' });
}

export function getRecentGoals() {
  return api<{ ok: boolean; recent: RecentGoal[] }>('/api/goals/recent');
}
