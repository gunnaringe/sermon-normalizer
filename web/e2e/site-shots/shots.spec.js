// The website's screenshots (site/img/shots/<lang>/), made with the real studio from a real speech, once per
// language, each in a church of its own and in that language. Not a test of the app (tests/ is that):
// run with scripts/site-shots.sh. run.sh turns the PNGs into WebP.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { translator, openEditor, waitEditor, toEdit, clearProposed } from './lib.js';

const DATA = process.env.DATA;
const OUT = process.env.OUT;

// What differs between the languages, besides the studio's own texts.
const LANGS = {
  en: { church: 'grace-chapel', labels: [['Communion'], ['Guest speaker'], ['Easter', 'Baptism']], parts: ['Part one', 'Part two', 'Cough'],
    clip: 'Welcome', share: 'For the homebound', speech: ['Inaugural', 'Speech'] },
  nb: { church: 'bykirken', labels: [['Nattverd'], ['Gjestetaler'], ['Påske', 'Dåp']], parts: ['Del 1', 'Del 2', 'Hoste'],
    clip: 'Velkommen', share: 'Til dem som ikke kom', speech: ['Innsettelse', 'Tale'] },
  nn: { church: 'bykyrkja', labels: [['Nattverd'], ['Gjestetalar'], ['Påske', 'Dåp']], parts: ['Del 1', 'Del 2', 'Hoste'],
    clip: 'Velkomen', share: 'Til dei som ikkje kom', speech: ['Innsetjing', 'Tale'] },
};

test.describe.configure({ mode: 'serial' });

for (const [lang, L] of Object.entries(LANGS)) {
  test(`the studio in ${lang}`, async ({ browser }) => {
    const T = translator(lang);
    const dir = path.join(OUT, lang);
    fs.mkdirSync(dir, { recursive: true });
    const context = await browser.newContext({ baseURL: 'http://localhost:8080/', viewport: { width: 1280, height: 800 } });
    // The language as the studio's Settings → Appearance keeps it; also for the public share page (another origin).
    await context.addInitScript((l) => localStorage.setItem('lang', l), lang);
    const page = await context.newPage();
    const api = (method, url, data) => page.request.fetch(url, { method, data, headers: { 'X-Tenant': L.church } });
    const save = page.locator('.save-bar button.primary');
    const done = page.getByRole('heading', { name: T('Done'), exact: true });
    const shot = async (name, target = page) => {
      // The header and the Save bar stick to the window: in a picture of one part of the page they would sit on top of it.
      const style = target !== page ? await page.addStyleTag({ content: 'header, .save-bar { position: static !important; }' }) : null;
      await page.waitForTimeout(400); // a dialog fades in
      await target.screenshot({ path: path.join(dir, name + '.png') });
      await style?.evaluate((el) => el.remove());
    };

    // Earlier Sundays: three are done, with labels; the rest wait.
    for (const day of ['2026-09-06', '2026-09-13', '2026-09-20']) {
      await openEditor(page, L.church, `2026/${day}`);
      await clearProposed(page, T);
      await page.evaluate(() => window.__editor.addSection(8, 112));
      await save.click();
      await expect(done).toBeVisible({ timeout: 120_000 });
    }
    for (const [i, day] of ['2026-09-06', '2026-09-13', '2026-09-20'].entries()) {
      expect((await api('PUT', `api/labels?path=${encodeURIComponent('2026/' + day)}`, { labels: L.labels[i] })).ok()).toBe(true);
    }

    // The church's intro, as its default: a few seconds of tone.
    const clip = await page.request.post(`api/clips?name=${encodeURIComponent(L.clip + '.wav')}`, {
      data: fs.readFileSync(path.join(DATA, 'Welcome.wav')), headers: { 'X-Tenant': L.church } });
    expect(clip.ok()).toBe(true);
    let clipID = '';
    await expect.poll(async () => {
      const c = await (await api('GET', 'api/clips')).json();
      clipID = c.clips[0]?.id || '';
      return clipID;
    }, { timeout: 60_000 }).not.toBe('');
    expect((await api('PUT', 'api/clips/defaults', { intro: clipID, outro: '', pause: 0.5 })).ok()).toBe(true);

    // Upload: the date is taken from the file name, as the X32 names it.
    await page.goto(`#/${L.church}`);
    await page.locator('.upload input[type=file]').setInputFiles(path.join(DATA, 'R_20261018-104506.wav'));
    await expect(page.locator('input[name=date]')).toHaveValue('2026-10-18');
    await shot('upload', page.locator('.upload'));
    await page.locator('.upload button.primary').click();
    await expect(page).toHaveURL(new RegExp(`#/${L.church}/folder/2026/2026-10-18$`));
    await waitEditor(page);

    // The sermon: two sections, what is between them left out, and a cough cut out of the second.
    await clearProposed(page, T);
    await page.evaluate((p) => {
      window.__editor.addSection(6, 250, p[0]);
      window.__editor.addSection(298, 832, p[1]);
      window.__editor.addCut(483, 489, p[2]);
    }, L.parts);
    await expect(page.locator('.section')).toHaveCount(2);
    await toEdit(page);
    await shot('editor');

    const step = (name) => page.locator(`section[aria-labelledby=step-${name}]`);
    await shot('processing', step('processing'));
    await expect(step('clips').locator('select[name=intro]')).toHaveValue('default');
    await shot('intro-outro', step('clips'));

    // Output: FLAC and MP3. Save keeps the edit, then makes the files; the run is followed live.
    for (const [format, want] of [['flac', true], ['mp3', true], ['opus', false]]) {
      const pick = step('output').locator(`.pill[data-format=${format}] button.pick`);
      if ((await pick.getAttribute('aria-pressed')) !== String(want)) await pick.click();
    }
    await save.click();
    await expect(page.locator('progress').first()).toBeVisible({ timeout: 60_000 });
    await page.waitForTimeout(1500);
    await step('output').scrollIntoViewIfNeeded();
    await page.evaluate(() => window.scrollBy(0, 120));
    await shot('making');
    await expect(done).toBeVisible({ timeout: 600_000 });

    // The finished sermon on the recordings page, with its Download menu.
    expect((await api('PUT', `api/labels?path=${encodeURIComponent('2026/2026-10-18')}`, { labels: L.speech })).ok()).toBe(true);
    await page.goto(`#/${L.church}/recording/2026/2026-10-18`);
    const detail = page.locator('tr.detail-row').first();
    await expect(detail.locator('audio').first()).toBeVisible({ timeout: 30_000 });
    await detail.locator('details.download-menu summary').click();
    await expect(detail.locator('details.download-menu')).toHaveAttribute('open', '');
    await shot('files');
    await detail.locator('details.download-menu summary').click();

    // A download share for those who were not there, and the public page it gives.
    await detail.locator('button[name=share-download]').click();
    const dialog = page.getByRole('dialog');
    await dialog.locator('input[name=label]').fill(L.share);
    await dialog.locator('input[name=maxDownloads]').fill('20');
    await dialog.locator('input[name=expires]').fill('2026-12-31');
    await shot('share');
    await dialog.getByRole('button', { name: T('Make share'), exact: true }).click();
    const made = page.getByRole('dialog');
    await expect(made.locator('.share-link')).toBeVisible();
    const link = await made.locator('.share-link').textContent();
    await made.getByRole('button', { name: T('OK'), exact: true }).click();
    const guest = await context.newPage();
    await guest.goto(link);
    await expect(guest.locator('button').filter({ hasText: 'MP3' }).first()).toBeVisible();
    await guest.waitForTimeout(400);
    await guest.screenshot({ path: path.join(dir, 'share-page.png') });
    await guest.close();

    // The recordings page: years, status, labels.
    await page.goto(`#/${L.church}`);
    await expect(page.locator('tr.row').first()).toBeVisible();
    await shot('recordings');
    await context.close();
  });
}
