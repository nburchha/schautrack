/**
 * Records walkthrough videos and screenshots of the catalog search, the entry
 * form with components, and the meal page (work package 4).
 *
 *   npx tsx scripts/demo-paket4.ts
 *
 * Needs a running compose.test.yml stack with the BLS catalog imported:
 *
 *   docker exec schautrack-test-web-1 ./import-foods --source bls
 *
 * It creates a throwaway account, so it never touches real data. Output goes to
 * DEMO_OUT (default docs/demo/paket-4): one .mp4 per scene plus PNG stills.
 * Uses the installed Google Chrome; set DEMO_CHANNEL= to use Playwright's own.
 */
import { chromium, devices, type Browser, type BrowserContext, type Page } from '@playwright/test';
import { execFileSync } from 'child_process';
import { mkdirSync, rmSync, writeFileSync } from 'fs';
import { createIsolatedUser, loginUser, psql } from '../e2e/fixtures/helpers';

const BASE = process.env.DEMO_BASE_URL || 'http://localhost:3001';
const OUT = process.env.DEMO_OUT || 'docs/demo/paket-4';
const CHANNEL = process.env.DEMO_CHANNEL === undefined ? 'chrome' : process.env.DEMO_CHANNEL || undefined;
// English, whatever the machine's language is: the selectors below read the UI text.
const PHONE = { ...devices['iPhone 14'], locale: 'en-US' };
const TZ = Intl.DateTimeFormat().resolvedOptions().timeZone;
const localDate = (offsetDays = 0) => {
  const d = new Date();
  d.setDate(d.getDate() + offsetDays);
  return d.toLocaleDateString('en-CA');
};
const TODAY = localDate();
const YESTERDAY = localDate(-1);

const wait = (page: Page, ms: number) => page.waitForTimeout(ms);

/** A red dot where the finger lands, so taps are visible in the recording. */
const TAP_MARKER = `
  addEventListener('pointerdown', (e) => {
    const dot = document.createElement('div');
    dot.style.cssText = 'position:fixed;z-index:2147483647;pointer-events:none;width:34px;height:34px;margin:-17px 0 0 -17px;border-radius:50%;background:rgba(255,80,80,.55);border:2px solid rgba(255,255,255,.8);transition:transform .45s ease-out,opacity .45s ease-out;left:' + e.clientX + 'px;top:' + e.clientY + 'px';
    document.documentElement.appendChild(dot);
    requestAnimationFrame(() => { dot.style.transform = 'scale(1.8)'; dot.style.opacity = '0'; });
    setTimeout(() => dot.remove(), 600);
  }, true);
`;

let user: { email: string; password: string; id: string };

function seedFreshDay() {
  psql(`DELETE FROM calorie_entries WHERE user_id = ${user.id}`);
  const id = psql(
    `INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount, eaten_at)
     VALUES (${user.id}, '${TODAY}', 'Porridge', 0, ('${TODAY} 07:30'::timestamp AT TIME ZONE '${TZ}')) RETURNING id`,
  );
  psql(`INSERT INTO entry_components (entry_id, name, grams, kcal_100g, protein_100g, carbs_100g, fat_100g, fiber_100g, sugar_100g)
        VALUES (${id}, 'Hafer Flocken', 50, 348, 13, 58.7, 7, 10, 1.3),
               (${id}, 'Vollmilch 3,5 %', 200, 64, 3.4, 4.8, 3.6, NULL, 4.8)`);
  psql(`UPDATE calorie_entries e SET amount = ROUND(s.k)::int, protein_g = ROUND(s.p)::int, carbs_g = ROUND(s.c)::int,
          fat_g = ROUND(s.f)::int, fiber_g = ROUND(s.fi)::int, sugar_g = ROUND(s.su)::int
        FROM (SELECT SUM(grams*kcal_100g/100) k, SUM(grams*protein_100g/100) p, SUM(grams*carbs_100g/100) c,
                     SUM(grams*fat_100g/100) f, SUM(grams*fiber_100g/100) fi, SUM(grams*sugar_100g/100) su
              FROM entry_components WHERE entry_id = ${id}) s WHERE e.id = ${id}`);
  psql(`INSERT INTO calorie_entries (user_id, entry_date, entry_name, amount, eaten_at)
        VALUES (${user.id}, '${TODAY}', 'Mensa lunch', 750, ('${TODAY} 12:40'::timestamp AT TIME ZONE '${TZ}'))`);
}

async function login(browser: Browser): Promise<string> {
  // Log in over the API, like the e2e suite, and keep only the session cookie.
  // No page is opened here: the first dashboard visit stores the browser's
  // language on the account, and that must happen in the English scene context.
  const { context } = await loginUser(browser, user.email, user.password);
  const state = `${OUT}/.state.json`;
  await context.storageState({ path: state });
  await context.close();
  return state;
}

interface Frame { file: string; t: number }

/**
 * Starts a screencast that keeps every frame as a JPEG. Playwright's own .webm
 * writer needs a bundled ffmpeg; collecting the frames and encoding them with
 * the system ffmpeg works with any installed Chrome.
 */
async function startCapture(page: Page, dir: string) {
  rmSync(dir, { recursive: true, force: true });
  mkdirSync(dir, { recursive: true });
  const frames: Frame[] = [];
  let n = 0;
  await page.screencast.start({
    size: { width: 585, height: 1266 },
    quality: 90,
    onFrame: ({ data, timestamp }) => {
      const file = `${dir}/f${String(n++).padStart(6, '0')}.jpg`;
      writeFileSync(file, data);
      frames.push({ file, t: timestamp });
    },
  });
  return frames;
}

function encode(frames: Frame[], out: string) {
  if (frames.length < 2) return false;
  // Timestamps are in seconds or milliseconds depending on the Playwright
  // version; the median gap tells which (frames are never seconds apart).
  const gaps = frames.slice(1).map((f, i) => f.t - frames[i].t).sort((a, b) => a - b);
  const unit = gaps[Math.floor(gaps.length / 2)] > 1 ? 1000 : 1;
  const lines: string[] = [];
  frames.forEach((f, i) => {
    const next = frames[i + 1];
    const seconds = next ? Math.max(0.01, (next.t - f.t) / unit) : 1;
    lines.push(`file '${f.file}'`, `duration ${seconds.toFixed(3)}`);
  });
  lines.push(`file '${frames[frames.length - 1].file}'`); // the concat demuxer ignores the last duration
  const list = `${out}.txt`;
  writeFileSync(list, lines.join('\n'));
  execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', list,
    '-vf', 'fps=30,format=yuv420p', '-c:v', 'libx264', '-crf', '22', '-movflags', '+faststart', out]);
  rmSync(list, { force: true });
  return true;
}

async function scene(browser: Browser, state: string, name: string, run: (page: Page) => Promise<void>) {
  seedFreshDay();
  const frameDir = `${OUT}/.frames-${name}`;
  const ctx: BrowserContext = await browser.newContext({ ...PHONE, storageState: state });
  await ctx.addInitScript(TAP_MARKER);
  const page = await ctx.newPage();
  console.log(`scene ${name}`);
  let frames: Frame[] = [];
  try {
    await page.goto(`${BASE}/dashboard`);
    await page.getByRole('button', { name: 'Add food' }).waitFor({ state: 'visible', timeout: 15000 });
    frames = await startCapture(page, frameDir);
    await wait(page, 1200);
    await run(page);
    await wait(page, 1500);
  } finally {
    await page.screencast.stop().catch(() => {});
    await ctx.close();
    if (encode(frames, `${OUT}/${name}.mp4`)) console.log(`  ${OUT}/${name}.mp4 (${frames.length} frames)`);
    rmSync(frameDir, { recursive: true, force: true });
  }
}

async function still(page: Page, name: string) {
  await wait(page, 500);
  await page.screenshot({ path: `${OUT}/${name}.png` });
  console.log(`  ${OUT}/${name}.png`);
}

async function openSheet(page: Page) {
  await page.getByRole('button', { name: 'Add food' }).click();
  await page.getByRole('dialog', { name: 'Add food' }).waitFor({ state: 'visible' });
  await wait(page, 700);
}

/** Types like a person, so the hits visibly update between keystrokes. */
async function typeSlowly(page: Page, text: string, delay = 220) {
  const input = page.getByTestId('food-search-input');
  await input.click();
  await input.pressSequentially(text, { delay });
}

async function pickAndAdd(page: Page, query: string, grams: string, hit?: string, shots?: { grams?: string }) {
  const input = page.getByTestId('food-search-input');
  await input.click();
  await input.pressSequentially(query, { delay: 140 });
  const results = page.getByTestId('food-search-result');
  const target = hit ? results.filter({ hasText: hit }).first() : results.first();
  await target.waitFor({ state: 'visible' });
  await wait(page, 900);
  await target.click();
  const g = page.getByTestId('food-search-grams');
  await g.fill('');
  await g.pressSequentially(grams, { delay: 160 });
  await wait(page, 600);
  if (shots?.grams) await still(page, shots.grams);
  await page.getByTestId('food-search-add').click();
  await wait(page, 700);
}

(async () => {
  mkdirSync(OUT, { recursive: true });
  user = createIsolatedUser('demo-paket4');
  // Real accounts store their time zone at registration; a seeded one has none, so times would render in UTC.
  psql(`UPDATE users SET timezone = :'tz' WHERE id = ${user.id}`, { tz: TZ });
  const browser = await chromium.launch({ channel: CHANNEL });
  const state = await login(browser);

  // 1. Live search -----------------------------------------------------------
  await scene(browser, state, '01-live-search', async (page) => {
    await openSheet(page);
    await still(page, 'search-1-hint');

    await typeSlowly(page, 'Hafer');
    await page.getByTestId('food-search-result').first().waitFor();
    await wait(page, 1200);
    await still(page, 'search-2-hits');

    // The catalog spells it "Hafer Flocken"; spaces are ignored while searching.
    await page.getByTestId('food-search-input').pressSequentially('flocken', { delay: 220 });
    await wait(page, 1500);
    await still(page, 'search-3-compound');

    // A typo still finds it.
    await page.getByTestId('food-search-input').fill('');
    await wait(page, 600);
    await typeSlowly(page, 'Haverflocken');
    await wait(page, 1500);
    await still(page, 'search-4-typo');

    await page.getByTestId('food-search-input').fill('');
    await wait(page, 600);
    await typeSlowly(page, 'xqzvw');
    await page.getByTestId('food-search-empty').waitFor();
    await wait(page, 1200);
    await still(page, 'search-5-empty');
  });

  // 2. New entry: search, add, track; then the same on another day -----------
  await scene(browser, state, '02-new-entry', async (page) => {
    await openSheet(page);
    await pickAndAdd(page, 'Haferflocken', '50', 'Hafer Flocken', { grams: 'new-1-grams' });
    await pickAndAdd(page, 'Skyr', '150', 'Skyr');
    await pickAndAdd(page, 'Banane', '100', 'Banane roh');
    await page.locator('#entry-calories').scrollIntoViewIfNeeded();
    await wait(page, 1000);
    await still(page, 'new-2-draft');

    // Change a row's grams: the total follows.
    const first = page.getByTestId('draft-component-grams').first();
    await first.fill('');
    await first.pressSequentially('80', { delay: 200 });
    await wait(page, 1200);
    await page.locator('form button[type="submit"]').scrollIntoViewIfNeeded();
    await still(page, 'new-3-total');
    await page.locator('form button[type="submit"]').click();
    await page.getByText('Entry tracked').waitFor();
    await wait(page, 1500);
    await still(page, 'new-4-tracked');

    // Another day: the time starts at 09:00.
    await openSheet(page);
    await page.locator('form input[type="date"]').fill(YESTERDAY);
    await wait(page, 800);
    await pickAndAdd(page, 'Skyr', '200', 'Skyr');
    await page.getByTestId('entry-form-time').scrollIntoViewIfNeeded();
    await wait(page, 1000);
    await still(page, 'new-5-other-day-0900');
    await page.getByTestId('entry-form-time-input').fill('19:00');
    await wait(page, 800);
    await still(page, 'new-6-time-edited');
    await wait(page, 800);
    await page.locator('form button[type="submit"]').click();
    await page.getByText('Entry tracked').waitFor();
  });

  // 3. Existing entry: add a component ---------------------------------------
  await scene(browser, state, '03-add-component', async (page) => {
    const row = page.getByTestId('entry-row').filter({ hasText: 'Porridge' });
    await row.getByTestId('entry-open').click();
    await page.getByTestId('meal-title').waitFor();
    await wait(page, 1200);
    await still(page, 'add-1-meal-before');

    await page.getByTestId('meal-add-component').click();
    await page.getByTestId('food-search-input').waitFor();
    await wait(page, 800);
    await typeSlowly(page, 'Banane');
    await page.getByTestId('food-search-result').first().waitFor();
    await wait(page, 1000);
    await still(page, 'add-2-search');
    await page.getByTestId('food-search-result').filter({ hasText: 'Banane roh' }).first().click();
    const g = page.getByTestId('food-search-grams');
    await g.fill('');
    await g.pressSequentially('120', { delay: 200 });
    await wait(page, 900);
    await still(page, 'add-3-grams');
    await page.getByTestId('food-search-add').click();
    await page.getByTestId('meal-component').nth(2).waitFor();
    await wait(page, 1500);
    await still(page, 'add-4-meal-after');
  });

  // 4. Existing entry: change and remove a component, change the time --------
  await scene(browser, state, '04-change-component', async (page) => {
    const row = page.getByTestId('entry-row').filter({ hasText: 'Porridge' });
    await row.getByTestId('entry-open').click();
    await page.getByTestId('meal-title').waitFor();
    await wait(page, 1200);

    await page.getByTestId('meal-component-grams').first().click();
    const input = page.getByTestId('meal-component-grams-input');
    await input.fill('');
    await input.pressSequentially('90', { delay: 220 });
    await wait(page, 600);
    await still(page, 'edit-1-grams');
    await input.press('Enter');
    await wait(page, 1500);

    await page.getByTestId('meal-component-remove').last().click();
    await wait(page, 900);
    await still(page, 'edit-2-remove');
    await page.getByTestId('meal-component-remove-confirm').click();
    await wait(page, 1500);

    await page.getByTestId('meal-time-input').fill('08:15');
    await wait(page, 700);
    await still(page, 'edit-3-time');
    await page.getByTestId('meal-time-input').blur();
    await wait(page, 1500);
    await still(page, 'edit-4-after');
  });

  // 5. Delete and undo an entry with components ------------------------------
  await scene(browser, state, '05-delete-undo', async (page) => {
    const row = page.getByTestId('entry-row').filter({ hasText: 'Porridge' });
    await row.getByRole('button', { name: 'Delete entry' }).click();
    await page.getByRole('button', { name: 'Undo' }).waitFor();
    await wait(page, 800);
    await still(page, 'undo-1-toast');
    await page.getByRole('button', { name: 'Undo' }).click();
    await page.getByTestId('entry-component-count').first().waitFor();
    await wait(page, 1200);
    await still(page, 'undo-2-restored');
  });

  // The error state, as a still only: the search endpoint fails.
  {
    const ctx = await browser.newContext({ ...PHONE, storageState: state });
    const page = await ctx.newPage();
    await page.route('**/api/foods/search*', (route) => route.abort());
    await page.goto(`${BASE}/dashboard`);
    await openSheet(page);
    await typeSlowly(page, 'Hafer', 60);
    await page.getByTestId('food-search-error').waitFor();
    await still(page, 'search-6-error');
    await ctx.close();
  }

  await browser.close();
  rmSync(`${OUT}/.state.json`, { force: true });
  psql(`DELETE FROM calorie_entries WHERE user_id = ${user.id}`);
  console.log('done');
})().catch((err) => {
  console.error(err);
  process.exit(1);
});
