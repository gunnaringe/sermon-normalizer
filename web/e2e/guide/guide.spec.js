// Makes the pictures of the documentation (docs/img) from a real speech, by using the app the way
// the guide describes. Not a test of the app (tests/ is that): run with scripts/test.sh guide.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const OUT = process.env.OUT;
const shot = async (page, name, target = page) => {
  // The header sticks to the top of the window: in a picture of one part of the page it would sit on top of it.
  const style = target !== page ? await page.addStyleTag({ content: 'header { position: static !important; }' }) : null;
  await page.waitForTimeout(400); // a dialog fades in
  await target.screenshot({ path: path.join(OUT, name + '.png') });
  await style?.evaluate((el) => el.remove());
};
// toEdit scrolls so the waveform and what is under it fill the window.
const toEdit = (page) => page.evaluate(() => window.scrollTo(0, document.querySelectorAll('section.step')[1].offsetTop - 70));
const editor = (page, fn, arg) => page.evaluate(fn, arg);
const save = (page) => page.getByRole('button', { name: 'Save', exact: true });

async function openEditor(page, folder) {
  await page.goto('#/folder/' + folder);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 120_000 });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 120_000 });
}
// clearProposed removes the section the editor starts an uncut recording with, so the guide's own sections are made next.
const sectionHint = (page) => page.locator('.section-hint');
async function clearProposed(page) {
  await expect(sectionHint(page)).not.toHaveText('', { timeout: 120_000 });
  if ((await sectionHint(page).textContent()).startsWith('The sermon is set to')) {
    await page.locator('.section').getByRole('button', { name: 'Remove' }).click();
    await expect(sectionHint(page)).toContainText('Drag on the waveform to select the sermon');
  }
}

test.describe.configure({ mode: 'serial' });

test('earlier Sundays: three are done, the rest wait', async ({ page }) => {
  for (const day of ['2026-09-06', '2026-09-13', '2026-09-20']) {
    await openEditor(page, `2026/${day}`);
    await clearProposed(page);
    await editor(page, () => window.__editor.addSection(8, 112));
    await save(page).click();
    await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 120_000 });
  }
  const labels = { '2026-09-06': ['Communion'], '2026-09-13': ['Guest speaker'], '2026-09-20': ['Easter', 'Baptism'] };
  for (const [day, l] of Object.entries(labels)) {
    const r = await page.request.put(`api/labels?path=${encodeURIComponent('2026/' + day)}`, { data: { labels: l } });
    expect(r.ok()).toBe(true);
  }
});

test('upload, cut, save', async ({ page }) => {
  await page.goto('');
  await expect(page.locator('button.year-head', { hasText: '2026' })).toHaveAttribute('aria-expanded', 'true');
  await shot(page, '01-recordings');

  // Upload: the date is taken from the file name; here it is typed.
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'service.wav'));
  await page.locator('input[name=date]').fill('2026-10-18');
  await shot(page, '02-upload', page.locator('.upload'));
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/grace-chapel\/folder\/2026\/2026-10-18$/);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 300_000 });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 300_000 });
  await expect(page.locator('.input-file')).not.toContainText('Reading', { timeout: 120_000 });
  await expect(page.locator('.input-file')).not.toContainText('Checking', { timeout: 120_000 });
  await expect(sectionHint(page)).toContainText('The sermon is set to', { timeout: 120_000 });
  await shot(page, '03-editor');

  // Two sections, with what is between them left out, and a cough cut out of the second.
  await clearProposed(page);
  await editor(page, () => {
    window.__editor.addSection(6, 250, 'Part one');
    window.__editor.addSection(298, 832, 'Part two');
    window.__editor.addCut(483, 489, 'Cough');
  });
  await expect(page.locator('.section')).toHaveCount(2);
  await toEdit(page);
  await shot(page, '04-sections-and-cuts');
  fs.copyFileSync(path.join(OUT, '04-sections-and-cuts.png'), path.join(OUT, 'hero.png'));
  const edit = page.locator('section.step').nth(1);
  await shot(page, '05-sections-list', edit.locator('.controls'));

  // A cut that is not in a section does nothing: it says so, and Save is not blocked.
  await editor(page, () => window.__editor.addCut(265, 280, 'Aside'));
  await expect(page.locator('.cut.outside .cut-warn')).toBeVisible();
  await shot(page, '06-cut-warning', edit.locator('.group', { hasText: 'Cut out' }));
  await page.locator('.cut.outside').getByRole('button', { name: 'Remove' }).click();

  const output = page.locator('section.step').last();
  await output.scrollIntoViewIfNeeded();
  await shot(page, '07-output', output);

  // Save: the edit is kept, then the files are made; progress can be followed or left.
  await save(page).click();
  await expect(page.locator('progress').first()).toBeVisible({ timeout: 60_000 });
  await shot(page, '08-processing', page.locator('section.step').last());
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 600_000 });
  await shot(page, '09-done', page.locator('section.step').last());
});

test('the finished recording in the list, labels and search', async ({ page }) => {
  const r = await page.request.put(`api/labels?path=${encodeURIComponent('2026/2026-10-18')}`, { data: { labels: ['Inaugural', 'Speech'] } });
  expect(r.ok()).toBe(true);
  await page.goto('');
  const row = page.locator('tr.row', { hasText: '2026-10-18' }).first();
  await row.getByRole('button', { name: '2026-10-18', exact: true }).click();
  await expect(page.locator('tr.detail-row audio').first()).toBeVisible({ timeout: 30_000 });
  await shot(page, '10-recording-details');
  await page.locator('input.search').fill('easter');
  await expect(page.locator('tr.row')).toHaveCount(1);
  await shot(page, '11-search');
});

test('settings and a share for people without a login', async ({ page, context }) => {
  await page.goto('#/settings');
  await expect(page.getByRole('heading', { name: 'Default tags' })).toBeVisible();
  await shot(page, '12-settings');
  await page.goto('#/settings/shares');
  await page.locator('input[name=label]').fill('Sound desk');
  await page.locator('input[name=maxUploads]').fill('5');
  await page.locator('input[name=expires]').fill('2026-12-31');
  await page.getByRole('button', { name: 'Make share' }).click();
  const dialog = page.getByRole('dialog');
  const link = await dialog.locator('.share-link').textContent();
  await shot(page, '13-share-made');
  await dialog.getByRole('button', { name: 'OK' }).click();
  await shot(page, '14-shares', page.locator('#shares'));

  // The public page: no login, the code from the link or typed.
  const visitor = await context.newPage();
  await visitor.goto('http://localhost:8081/');
  await shot(visitor, '15-public-code');
  await visitor.close();
  const guest = await context.newPage(); // the link in a new tab: the code in it is filled in and leaves the address bar
  await guest.goto(link);
  await expect(guest.getByRole('heading', { name: 'Sound desk' })).toBeVisible();
  await guest.locator('input[type=file]').setInputFiles({ name: 'Guest talk 2026-11-01.flac', mimeType: 'audio/flac', buffer: fs.readFileSync(path.join(DATA, 'guest.flac')) });
  await expect(guest.getByText('Received. Thank you!')).toBeVisible({ timeout: 60_000 });
  await shot(guest, '16-public-uploaded');
});

test('on a phone, and in the dark', async ({ browser }) => {
  const phone = await browser.newContext({ baseURL: 'http://localhost:8080/', viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  const p = await phone.newPage();
  await p.goto('');
  await expect(p.locator('tr.row').first()).toBeVisible();
  await shot(p, '17-phone-recordings');
  await openEditor(p, '2026/2026-10-18');
  await toEdit(p);
  await shot(p, '18-phone-editor');
  await phone.close();

  const dark = await browser.newContext({ baseURL: 'http://localhost:8080/', viewport: { width: 1280, height: 800 }, colorScheme: 'dark' });
  const d = await dark.newPage();
  await openEditor(d, '2026/2026-10-18');
  await toEdit(d);
  await shot(d, '19-dark-editor');
  await dark.close();
});
