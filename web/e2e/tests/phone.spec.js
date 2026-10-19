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
  await expect(page.getByRole('link', { name: '2026/' })).toBeVisible();
  await fits(page, 'folders');
  await page.goto('#/folders/2026');
  await expect(page.getByRole('link', { name: '2026-10-04' })).toBeVisible();
  await fits(page, 'folder-list');
  await page.goto('#/upload');
  await expect(page.locator('.drop')).toBeVisible();
  await fits(page, 'upload');
  await page.goto('#/settings');
  await expect(page.getByRole('heading', { name: 'Default tags' })).toBeVisible();
  await fits(page, 'settings');
  await page.goto('#/shares');
  await expect(page.getByRole('heading', { name: /Shares/ })).toBeVisible();
  await fits(page, 'shares');
});

test('phone: the editor by touch', async ({ page }) => {
  fs.rmSync(path.join(DATA, 'root/2026/2026-10-04/sermon.json'), { force: true });
  await page.goto('#/folder/2026/2026-10-04');
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 60_000 });
  await fits(page, 'editor-empty');

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
  await expect(page.getByLabel('Cut 1 from')).toBeVisible();
  await page.getByRole('button', { name: '+', exact: true }).tap();
  await expect(page.getByRole('button', { name: 'Run' })).toBeEnabled();
  await fits(page, 'editor');
});
