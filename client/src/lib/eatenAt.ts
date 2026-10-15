// The server stores when a meal was eaten as an instant (`eaten_at`, RFC 3339)
// and derives the entry's day from it in the account's time zone. The UI works
// with a calendar date and an "HH:MM" wall-clock time, so these two helpers
// convert between them in that same zone, DST included.

function browserZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

/** A zone name Intl accepts; the browser's own when the stored one is unknown. */
function safeZone(tz: string | null | undefined): string {
  if (!tz) return browserZone();
  try {
    new Intl.DateTimeFormat('en-US', { timeZone: tz });
    return tz;
  } catch {
    return browserZone();
  }
}

/** Offset of `tz` from UTC, in minutes, at the given instant. */
function offsetMinutes(tz: string, utcMs: number): number {
  const parts = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    hourCycle: 'h23',
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
  }).formatToParts(new Date(utcMs));
  const get = (type: string) => Number(parts.find((p) => p.type === type)?.value);
  const asUtc = Date.UTC(get('year'), get('month') - 1, get('day'), get('hour'), get('minute'), get('second'));
  return Math.round((asUtc - Math.floor(utcMs / 1000) * 1000) / 60000);
}

function formatOffset(minutes: number): string {
  const sign = minutes < 0 ? '-' : '+';
  const abs = Math.abs(minutes);
  return `${sign}${String(Math.floor(abs / 60)).padStart(2, '0')}:${String(abs % 60).padStart(2, '0')}`;
}

/**
 * RFC 3339 `eaten_at` for a wall-clock `date` (YYYY-MM-DD) and `time` (HH:MM)
 * in `tz`, e.g. toEatenAt('2026-08-05', '07:30', 'Europe/Berlin') is
 * '2026-08-05T07:30:00+02:00'.
 */
export function toEatenAt(date: string, time: string, tz: string | null | undefined): string {
  const zone = safeZone(tz);
  const [y, m, d] = date.split('-').map(Number);
  const [h, min] = time.split(':').map(Number);
  const naive = Date.UTC(y, m - 1, d, h, min, 0);
  // The offset depends on the instant, which depends on the offset: take a
  // first guess, then correct once, which settles it away from DST changes.
  let offset = offsetMinutes(zone, naive);
  const corrected = offsetMinutes(zone, naive - offset * 60000);
  if (corrected !== offset) offset = corrected;
  return `${date}T${time}:00${formatOffset(offset)}`;
}

/** The wall-clock "HH:MM" of an RFC 3339 instant in `tz`. */
export function localTimeOf(iso: string, tz: string | null | undefined): string {
  return new Intl.DateTimeFormat('en-GB', {
    timeZone: safeZone(tz),
    hourCycle: 'h23',
    hour: '2-digit', minute: '2-digit',
  }).format(new Date(iso));
}
