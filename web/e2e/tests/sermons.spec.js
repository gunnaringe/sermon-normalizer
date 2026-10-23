// The folder list: a sermon opens its details (play, download, make a missing
// format); a recording that is not cut yet is "to do" and still opens the editor.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });
const folder = '2026/2026-07-05';

test.beforeAll(async ({ request }) => {
  const dir = path.join(DATA, 'root', folder);
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2026-07-05_raw.flac'));
  const p = 'api/';
  const q = '?path=' + encodeURIComponent(folder);
  const settings = { sections: [{ start: 3, end: 25 }], cuts: [{ start: 10, end: 12 }], fadeIn: 0, fadeOut: 0 };
  expect((await request.put(p + 'sermon' + q, { data: settings })).ok()).toBe(true);
  expect((await request.post(p + 'sermon/save' + q)).ok()).toBe(true);
  const run = await request.post(p + 'run' + q, {
    data: { ...settings, flac: true, mp3: false, opus: true, stereo: false, noiseReduction: 0, targetLufs: -16, truePeak: -1.5, mp3Bitrate: '128k', opusBitrate: '64k', force: false },
  });
  expect(run.ok(), await run.text()).toBe(true);
  const { id } = await run.json();
  await expect.poll(async () => (await (await request.get(p + 'jobs/' + id)).json()).state, { timeout: 90_000 }).toBe('done');
});

test('a sermon opens its details in the list: play, download, make a missing format', async ({ page }) => {
  await page.goto('');
  const row = page.locator('.year tr.row', { hasText: '2026-07-05' });
  await expect(row).not.toContainText('to do');
  await expect(row).toContainText('done');
  await row.click();
  await expect(page).toHaveURL(/#\/first-church$/);
  const detail = page.locator('tr.detail-row');
  await expect(detail.locator('audio')).toHaveCount(1); // one player, never the whole service
  await expect(detail.locator('audio')).toHaveAttribute('src', /opus|flac/); // no MP3 yet
  await expect(detail).toContainText('Length 0:20');
  await expect(detail.getByRole('link', { name: 'Edit sermon' })).toBeVisible();
  await expect(detail).not.toContainText('original'); // the whole service is not offered
  await expect(row).not.toContainText('raw');
  await expect(detail.locator('.player-what')).toHaveText('Plays: Opus'); // no MP3 yet
  // Download opens the files: those made, with their size, and Make for the one that is not.
  await page.screenshot({ path: path.join(shots, 'sermon-list-open.png') });
  await detail.locator('summary[aria-label=Download]').click();
  await expect(detail.getByRole('link', { name: 'Download FLAC' })).toHaveAttribute('href', /download=1/);
  await expect(detail.getByRole('link', { name: 'Download Opus' })).toBeVisible();
  await expect(detail.getByRole('link', { name: 'Download MP3' })).toHaveCount(0);
  const mp3 = detail.locator('button[name=make-mp3]');
  await expect(mp3).toContainText('not made');
  await page.screenshot({ path: path.join(shots, 'sermon-list-download.png') });
  // Escape and a click outside close it.
  await page.keyboard.press('Escape');
  await expect(mp3).toBeHidden();
  await detail.locator('summary[aria-label=Download]').click();
  await page.locator('h1, header').first().click();
  await expect(mp3).toBeHidden();
  await detail.locator('summary[aria-label=Download]').click();

  await mp3.click();
  await expect(mp3).toHaveClass(/busy/);
  await expect(detail.getByText(/conversion is running/)).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'sermon-list-making.png') });
  // Made: the menu is still open, with the file in it.
  await expect(detail.getByRole('link', { name: 'Download MP3' })).toBeVisible({ timeout: 60_000 });
  expect(fs.readdirSync(path.join(DATA, 'root', folder)).some((f) => f.endsWith('.mp3'))).toBe(true);

  await row.locator('button.sermon-name').click(); // the name button too: once, not twice
  await expect(detail).toHaveCount(0);
  await row.click();
  await expect(detail).toHaveCount(1);
  await row.click(); // closes again
  await expect(detail).toHaveCount(0);
  await row.getByRole('button', { name: /Show details/ }).click();
  await expect(page.locator('tr.detail-row')).toHaveCount(1);
});

test('a recording that is not cut yet is "to do", has an edit button, and still opens the editor', async ({ page }) => {
  await page.goto('');
  const row = page.locator('.year tr.row', { hasText: '2026-08-30' });
  await expect(row).toContainText('to do');
  await row.click(); // anywhere on the row
  await expect(page.getByRole('link', { name: 'Open in the editor' })).toBeVisible();
  await expect(page.locator('tr.detail-row audio')).toHaveCount(0);
  await expect(page.locator('tr.detail-row')).not.toContainText('Download'); // nothing to download yet
  await expect(page).toHaveURL(/#\/first-church$/);
  await page.screenshot({ path: path.join(shots, 'sermon-list-unprocessed.png') });
  await page.getByRole('link', { name: 'Open in the editor' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-08-30$/);
});

test('sermon details fit a phone', async ({ browser }) => {
  const ctx = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, baseURL: process.env.BASE_URL || 'http://localhost:8080/' });
  const page = await ctx.newPage();
  await page.goto('');
  await page.locator('.year tr.row', { hasText: '2026-07-05' }).click();
  await expect(page.locator('tr.detail-row audio')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: path.join(shots, 'phone-sermon-list.png') });
  await ctx.close();
});

test('a recording with changes not saved in the editor says so in the list', async ({ page, request }) => {
  const q = '?path=' + encodeURIComponent(folder);
  const adjusted = { sections: [{ start: 3, end: 24 }], cuts: [], fadeIn: 0, fadeOut: 0 };
  expect((await request.put('api/sermon' + q, { data: adjusted })).ok()).toBe(true);
  try {
    await page.goto('');
    const row = page.locator('.year tr.row', { hasText: '2026-07-05' });
    await expect(row).toContainText('not saved');
    await page.locator('.year').first().screenshot({ path: path.join(shots, 'recordings-not-saved.png') });
  } finally {
    expect((await request.post('api/sermon/revert' + q)).ok()).toBe(true);
  }
  await page.reload();
  await expect(page.locator('.year tr.row', { hasText: '2026-07-05' })).not.toContainText('not saved');
});
