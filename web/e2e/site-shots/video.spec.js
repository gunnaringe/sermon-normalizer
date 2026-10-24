// The website's video: the studio in use, in English, 1280×720, recorded by Playwright. A visible pointer is put
// in the page (Playwright's own is not recorded) and moved to each control before it is clicked, with a caption
// for each step. run.sh cuts the blank start (video-start: seconds) and makes the MP4, the WebM and the poster
// (from video-poster: seconds). Run with scripts/site-shots.sh video.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import { waitEditor, clearProposed, translator } from './lib.js';

const DATA = process.env.DATA;
const OUT = process.env.OUT;
const SIZE = { width: 1280, height: 720 };
const T = translator('en');

// The pointer and the caption, in a popover so they stay above the studio's dialogs (the top layer): raise() puts
// them back on top when a dialog opens.
function overlay() {
  const css = `
    #demo-pointer { position: fixed; inset: auto; left: 0; top: 0; margin: 0; padding: 0; border: 0; background: none; overflow: visible;
      width: 28px; height: 28px; pointer-events: none; transform: translate(-100px, -100px); filter: drop-shadow(0 2px 3px rgba(0,0,0,.45)); }
    #demo-pointer::after { content: ''; position: absolute; left: -14px; top: -14px; width: 28px; height: 28px; border-radius: 50%;
      background: rgba(5, 150, 105, .35); transform: scale(0); transition: transform .25s, opacity .4s; opacity: 0; }
    #demo-pointer.down::after { transform: scale(1); opacity: 1; transition: none; }
    #demo-caption { position: fixed; inset: 6px 0 auto 0; margin: 0 auto; width: max-content; max-width: 56vw; padding: 8px 22px;
      border: 0; border-radius: 12px; background: rgba(6, 51, 38, .94); color: #fff; font: 600 21px/1.3 Inter, system-ui, sans-serif; text-align: center;
      box-shadow: 0 10px 30px rgba(0,0,0,.35); pointer-events: none; opacity: 0; transition: opacity .3s; }
    #demo-caption.on { opacity: 1; }
    #demo-caption small { display: block; font-weight: 400; font-size: 15px; opacity: .88; }`;
  const add = () => {
    const style = document.createElement('style');
    style.textContent = css;
    document.head.append(style);
    const pointer = document.createElement('div');
    pointer.id = 'demo-pointer';
    pointer.popover = 'manual';
    pointer.innerHTML = '<svg width="28" height="28" viewBox="0 0 28 28"><path d="M3 2 L3 22 L8.5 17 L12.5 26 L16 24.5 L12 15.8 L19.5 15.8 Z" fill="#fff" stroke="#111" stroke-width="1.6" stroke-linejoin="round"/></svg>';
    const caption = document.createElement('div');
    caption.id = 'demo-caption';
    caption.popover = 'manual';
    document.body.append(pointer, caption);
    let open = null;
    const raise = () => {
      const top = [...document.querySelectorAll('dialog[open]')].pop() || null;
      if (top === open && pointer.matches(':popover-open')) return;
      open = top;
      for (const el of [caption, pointer]) {
        if (el.matches(':popover-open')) el.hidePopover();
        el.showPopover();
      }
    };
    raise();
    setInterval(raise, 50);
    addEventListener('mousemove', (e) => { pointer.style.transform = `translate(${e.clientX - 3}px, ${e.clientY - 2}px)`; }, true);
    addEventListener('mousedown', () => pointer.classList.add('down'), true);
    addEventListener('mouseup', () => setTimeout(() => pointer.classList.remove('down'), 150), true);
    window.__caption = (html) => {
      sessionStorage.setItem('demo-caption', html);
      caption.innerHTML = html;
      caption.classList.toggle('on', Boolean(html));
    };
    window.__caption(sessionStorage.getItem('demo-caption') || '');
  };
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', add);
  else add();
}

test('the studio in use', async ({ browser }) => {
  // Before the video: three earlier Sundays done, so the recordings page is not empty.
  const setup = await browser.newContext({ baseURL: 'http://localhost:8080/', viewport: SIZE });
  const sp = await setup.newPage();
  for (const day of ['2026-09-20', '2026-09-27', '2026-10-04']) {
    await sp.goto(`#/grace-chapel/folder/2026/${day}`);
    await waitEditor(sp);
    await clearProposed(sp, T);
    await sp.evaluate(() => window.__editor.addSection(8, 112));
    await sp.locator('.save-bar button.primary').click();
    await expect(sp.getByRole('heading', { name: 'Done', exact: true })).toBeVisible({ timeout: 120_000 });
  }
  await setup.close();

  const context = await browser.newContext({ baseURL: 'http://localhost:8080/', viewport: SIZE, recordVideo: { dir: OUT, size: SIZE } });
  const began = Date.now();
  await context.addInitScript(overlay);
  await context.addInitScript(() => localStorage.setItem('lang', 'en'));
  const page = await context.newPage();
  const at = () => (Date.now() - began) / 1000;
  const pause = (ms = 900) => page.waitForTimeout(ms);
  const caption = async (title, line = '') => {
    await page.evaluate(([a, b]) => window.__caption(a ? `${a}${b ? `<small>${b}</small>` : ''}` : ''), [title, line]);
  };
  let mouse = { x: 640, y: 360 };
  // point moves the pointer to the middle of a control, at a pace that can be followed.
  const point = async (locator) => {
    await locator.scrollIntoViewIfNeeded();
    const box = await locator.boundingBox();
    const to = { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    const steps = Math.max(12, Math.round(Math.hypot(to.x - mouse.x, to.y - mouse.y) / 18));
    await page.mouse.move(to.x, to.y, { steps });
    mouse = to;
    await pause(350);
  };
  const click = async (locator) => {
    await point(locator);
    await page.mouse.down();
    await page.mouse.up();
  };
  const choose = async (select, value) => {
    await point(select);
    await page.mouse.down();
    await page.mouse.up();
    await page.keyboard.press('Escape'); // the native list is not in the video; the value changes in view
    await select.selectOption(value);
  };

  await page.goto('#/grace-chapel');
  await expect(page.locator('tr.row').first()).toBeVisible();
  await page.mouse.move(mouse.x, mouse.y);
  fs.writeFileSync(path.join(OUT, 'video-start'), String(at()));
  await caption('Sermon Studio', 'From the recording of a service to sermon files');
  await pause(3000);

  // 1. Upload the recording.
  await caption('1. Upload the recording of the service');
  await pause(1200);
  const chooser = page.waitForEvent('filechooser');
  await click(page.locator('.upload .drop button.link'));
  await (await chooser).setFiles(path.join(DATA, 'R_20261018-104506.wav'));
  await expect(page.locator('input[name=date]')).toHaveValue('2026-10-18');
  await caption('1. Upload the recording of the service', 'The date comes from the file name');
  await pause(1500);
  await click(page.locator('.upload button.primary'));
  await waitEditor(page);
  await pause(800);

  // 2. Mark where the sermon starts and ends.
  await caption('2. Mark where the sermon starts and ends', 'Type its start and end, or drag its edges');
  // The waveform and the sermon's row with its times, both in view.
  const toSermon = () => page.evaluate(() => {
    const row = document.querySelector('.section');
    const wave = document.querySelector('#waveform');
    const top = row ? row.getBoundingClientRect().bottom + scrollY - 660 : wave.getBoundingClientRect().top + scrollY - 70;
    window.scrollTo({ top, behavior: 'smooth' });
  });
  await toSermon();
  await pause(1500);
  // The section the editor starts with (the recording without its silent ends): set to the sermon.
  if (!(await page.evaluate(() => window.__editor.sections.length))) await page.evaluate(() => window.__editor.addSection(0, 180));
  const times = page.locator('.section').first().locator('label input');
  for (const [i, time] of [[0, '0:22'], [1, '2:42']]) {
    await click(times.nth(i));
    await page.keyboard.press('ControlOrMeta+a');
    await page.keyboard.type(time, { delay: 140 });
    await page.keyboard.press('Enter');
    await pause(900);
  }
  const [made] = await page.evaluate(() => window.__editor.sections);
  expect(Math.round(made.start)).toBe(22);
  expect(Math.round(made.end)).toBe(162);
  await pause(1200);
  fs.writeFileSync(path.join(OUT, 'video-poster'), String(at()));
  await pause(1000);

  // 3. Settings → Processing.
  await caption('3. Settings → Processing', 'How the sound is made: here, stronger leveling');
  await click(page.locator('header [data-nav=settings]'));
  const settings = page.locator('dialog.settings-dialog');
  const processing = settings.locator('section#processing');
  await expect(processing.locator('select[name=leveling]')).toBeVisible();
  await processing.locator('select[name=leveling]').evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  await pause(1300);
  await choose(processing.locator('select[name=leveling]'), 'strong');
  await pause(900);
  await click(processing.locator('button[name=save-processing]'));
  await expect(processing.getByText('Saved.')).toBeVisible();
  await pause(1300);

  // 4. Settings → Intro and outro: upload an intro.
  await caption('4. Settings → Intro and outro', 'Upload a short intro for the church');
  const clips = settings.locator('section#clips');
  await clips.locator('button[name=upload-clip]').evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  await pause(1300);
  const clipChooser = page.waitForEvent('filechooser');
  await click(clips.locator('button[name=upload-clip]'));
  await (await clipChooser).setFiles(path.join(DATA, 'Welcome.wav'));
  await expect(clips.locator('.clip:not(.pending)').filter({ hasText: 'Welcome' })).toBeVisible({ timeout: 60_000 });
  await pause(1500);
  await click(settings.locator('button.close'));
  await expect(settings).toHaveCount(0);
  // The editor takes the church's new processing when it is opened again.
  await page.reload();
  await waitEditor(page);
  await caption('4. Choose it as the sermon\'s intro');
  const clipStep = page.locator('section[aria-labelledby=step-clips]');
  await clipStep.evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  await pause(1300);
  const intro = clipStep.locator('select[name=intro]');
  const welcome = await intro.locator('option', { hasText: 'Welcome' }).first().getAttribute('value');
  await choose(intro, welcome);
  await expect(clipStep).toContainText('With the intro and outro:');
  await pause(2000);

  // 5. Save: make the FLAC and the MP3.
  await caption('5. Save: make the FLAC and the MP3');
  const output = page.locator('section[aria-labelledby=step-output]');
  await output.evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  await pause(1200);
  for (const [format, want] of [['flac', true], ['mp3', true], ['opus', false]]) {
    const pick = output.locator(`.pill[data-format=${format}] button.pick`);
    if ((await pick.getAttribute('aria-pressed')) !== String(want)) {
      await click(pick);
      await pause(500);
    }
  }
  await pause(800);
  await click(page.locator('.save-bar button.primary'));
  await expect(page.getByRole('heading', { name: 'Done', exact: true })).toBeVisible({ timeout: 120_000 });
  await page.getByRole('heading', { name: 'Done', exact: true }).evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
  await pause(2200);

  // The files, on the recordings page.
  await caption('5. Download the files', 'With the intro, or the sermon only');
  await click(page.locator('header .brand'));
  const row = page.locator('tr.row', { hasText: '2026-10-18' }).first();
  await expect(row).toBeVisible();
  await pause(900);
  await click(row.getByRole('button', { name: '2026-10-18', exact: true }));
  const menu = page.locator('tr.detail-row details.download-menu').first();
  await expect(menu).toBeVisible({ timeout: 30_000 });
  await pause(1000);
  await click(menu.locator('summary'));
  await expect(menu).toHaveAttribute('open', '');
  await expect(menu).toContainText('FLAC');
  await expect(menu).toContainText('MP3');
  // Over the menu's items, slowly.
  for (const item of await menu.locator('a, button').all()) {
    if (await item.isVisible()) {
      await point(item);
      await pause(300);
    }
  }
  await pause(2500);
  await caption('');
  await pause(800);

  const video = page.video();
  await context.close();
  fs.copyFileSync(await video.path(), path.join(OUT, 'video.webm'));
});
