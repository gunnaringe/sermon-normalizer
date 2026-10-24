// What the website's screenshots and video share: the studio's own translations, to find its buttons by
// their text in each language, and waiting for the editor.
import { expect } from '@playwright/test';
import fs from 'node:fs';

// The studio's dictionaries (web/static/locales.js, mounted at /static), loaded as a module from its text.
const source = fs.readFileSync('/static/locales.js', 'utf8');
const { nb, nn } = await import('data:text/javascript;base64,' + Buffer.from(source).toString('base64'));
const DICTS = { en: {}, nb, nn };

// translator gives what i18n.js's t() gives in one language: the English text is the key, 'context|Text' has a context.
export function translator(lang) {
  const dict = DICTS[lang];
  return (text) => dict[text] ?? (text.includes('|') ? text.slice(text.indexOf('|') + 1) : text);
}

// openEditor opens a recording in the editor and waits until its waveform is there.
export async function openEditor(page, church, folder, timeout = 300_000) {
  await page.goto(`#/${church}/folder/${folder}`);
  await waitEditor(page, timeout);
}
export async function waitEditor(page, timeout = 300_000) {
  await expect(page.locator('#waveform')).toBeVisible({ timeout });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout });
  await expect(page.locator('.section-hint')).not.toHaveText('', { timeout });
}

// toEdit scrolls so the waveform and what is under it fill the window.
export const toEdit = (page) => page.evaluate(() => window.scrollTo(0, document.querySelectorAll('section.step')[1].offsetTop - 70));

// clearProposed removes the section the editor starts an uncut recording with.
export async function clearProposed(page, T) {
  while (await page.evaluate(() => window.__editor.sections.length) > 0) {
    await page.locator('.section').first().getByRole('button', { name: T('Remove'), exact: true }).click();
  }
}
