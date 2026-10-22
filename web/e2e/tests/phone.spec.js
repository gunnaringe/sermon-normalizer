// The studio on a phone: every page fits the screen (no sideways scrolling),
// and the editor can be used by touch.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });

test.use({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true });

// fits checks that nothing sticks out sideways. A phone widens the page to
// fit what sticks out, so compare with the phone's own width.
async function fits(page, name) {
  await page.screenshot({ path: path.join(shots, `phone-${name}.png`), fullPage: true });
  const width = await page.evaluate(() => document.documentElement.scrollWidth);
  expect(width, `${name} is wider than the screen`).toBeLessThanOrEqual(390);
}

test('phone: every page fits the screen', async ({ page }) => {
  await page.goto('');
  await expect(page.getByRole('button', { name: '2026-10-04', exact: true }).first()).toBeVisible();
  await fits(page, 'recordings');
  await expect(page.locator('.upload .drop')).toBeVisible();
  await page.locator('.upload input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await expect(page.getByRole('button', { name: 'Upload' })).toBeVisible();
  await fits(page, 'upload');
  await page.goto('#/settings');
  await expect(page.getByRole('heading', { name: 'Default tags' })).toBeVisible();
  await fits(page, 'settings');
  await page.goto('#/shares');
  await expect(page.getByRole('heading', { name: 'Shares' })).toBeVisible();
  await fits(page, 'shares');
  await page.goto('#/about');
  await expect(page.getByRole('heading', { name: 'About Sermon Studio' })).toBeVisible();
  await fits(page, 'about');
});

test('phone: the logo is a square, so a long church name has the room it can', async ({ page }) => {
  await page.route('**/api/tenants', (r) => r.fulfill({ json: {
    tenants: [{ id: 'first-church', name: 'Norkirken Melhus kirke', logo: '1' }], default: 'first-church', canAdd: false, shares: true } }));
  await page.route('**/api/logo*', (r) => r.fulfill({ contentType: 'image/svg+xml',
    body: '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 120 40"><rect width="120" height="40" fill="#b00020"/></svg>' }));
  await page.goto('');
  const logo = page.locator('header .brand img');
  await expect(logo).toBeVisible();
  const box = await logo.boundingBox();
  expect([box.width, box.height]).toEqual([34, 34]); // a wide logo too
  await expect(page.locator('#church-name')).toHaveText('Norkirken Melhus kirke');
  await page.locator('header').screenshot({ path: path.join(shots, 'phone-header-logo.png') });
});

test('phone: the editor by touch', async ({ page }) => {
  // Forget the edit of earlier tests, and only that: what is known about the recording (such as the WAV
  // it was converted from) is read by later tests.
  const file = path.join(DATA, 'root/2026/2026-10-04/recording.json');
  if (fs.existsSync(file)) {
    const st = JSON.parse(fs.readFileSync(file, 'utf8'));
    fs.writeFileSync(file, JSON.stringify({ ...st, saved: null, savedAt: undefined, adjusted: null }));
  }
  await page.goto('#/folder/2026/2026-10-04');
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 60_000 });
  // The editor starts with the sermon set to the whole recording (it has no silence at the ends).
  await expect(page.getByText('The sermon is set to the recording without the silence')).toBeVisible({ timeout: 60_000 });
  await fits(page, 'editor-opened');
  await page.locator('.section').getByRole('button', { name: 'Remove' }).tap();
  await expect.poll(() => page.evaluate(() => window.__editor.sections.length)).toBe(0);
  await page.evaluate(() => window.scrollTo(0, 0)); // the tap scrolled; the waveform must not sit under the header

  // A finger drag on the waveform selects the sermon.
  const box = await page.locator('#waveform').boundingBox();
  const y = box.y + box.height / 2;
  const x = (t) => box.x + (t / 100) * box.width;
  // Real touch events, so the browser decides between scrolling and dragging.
  const cdp = await page.context().newCDPSession(page);
  const touch = (type, px) => cdp.send('Input.dispatchTouchEvent', { type, touchPoints: type === 'touchEnd' ? [] : [{ x: px, y }] });
  await touch('touchStart', x(30));
  await page.waitForTimeout(150); // a touch must be held a moment before it drags
  for (const t of [35, 45, 55, 65, 70]) await touch('touchMove', x(t));
  await touch('touchEnd');
  await expect.poll(() => page.evaluate(() => window.__editor.sections.length)).toBe(1);
  const [s] = await page.evaluate(() => window.__editor.sections);
  expect(s.start).toBeGreaterThan(25);
  expect(s.end).toBeLessThan(75);

  // Everything else is buttons: cut out, zoom, play.
  await page.evaluate(() => window.__editor.ws.setTime(40));
  await page.getByRole('button', { name: 'Cut out here' }).tap();
  await page.evaluate(() => window.__editor.ws.setTime(45));
  await page.getByRole('button', { name: 'End the cut here' }).tap();
  await expect(page.getByLabel('Cut 1 from')).toBeVisible();
  await page.getByRole('button', { name: '+', exact: true }).tap();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  await fits(page, 'editor');
});
