import { describe, expect, it } from 'vitest';
import { localTimeOf, toEatenAt } from './eatenAt';

describe('toEatenAt', () => {
  it('uses the summer offset of the zone', () => {
    expect(toEatenAt('2026-08-05', '07:30', 'Europe/Berlin')).toBe('2026-08-05T07:30:00+02:00');
  });

  it('uses the winter offset of the zone', () => {
    expect(toEatenAt('2026-01-15', '09:00', 'Europe/Berlin')).toBe('2026-01-15T09:00:00+01:00');
  });

  it('handles zones west of UTC and half-hour zones', () => {
    expect(toEatenAt('2026-08-05', '12:00', 'America/New_York')).toBe('2026-08-05T12:00:00-04:00');
    expect(toEatenAt('2026-08-05', '12:00', 'Asia/Kolkata')).toBe('2026-08-05T12:00:00+05:30');
  });

  it('picks the right offset on the day the clocks change', () => {
    // Berlin moves to summer time on 2026-03-29 at 02:00; 09:00 is already +02:00.
    expect(toEatenAt('2026-03-29', '09:00', 'Europe/Berlin')).toBe('2026-03-29T09:00:00+02:00');
    expect(toEatenAt('2026-03-29', '01:00', 'Europe/Berlin')).toBe('2026-03-29T01:00:00+01:00');
  });

  it('falls back to the browser zone for an unknown zone name', () => {
    expect(toEatenAt('2026-08-05', '07:30', 'Not/AZone')).toMatch(/^2026-08-05T07:30:00[+-]\d\d:\d\d$/);
  });
});

describe('localTimeOf', () => {
  it('renders the wall-clock time in the zone', () => {
    expect(localTimeOf('2026-08-05T05:30:00Z', 'Europe/Berlin')).toBe('07:30');
    expect(localTimeOf('2026-01-15T08:00:00Z', 'Europe/Berlin')).toBe('09:00');
  });

  it('round-trips with toEatenAt', () => {
    expect(localTimeOf(toEatenAt('2026-08-05', '18:45', 'America/New_York'), 'America/New_York')).toBe('18:45');
  });

  it('shows midnight as 00:00, not 24:00', () => {
    expect(localTimeOf('2026-08-04T22:00:00Z', 'Europe/Berlin')).toBe('00:00');
  });
});
