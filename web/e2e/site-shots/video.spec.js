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

  // The waveform as the editor measures it (its own timeAt: the wrapper's box, the recording's length), so a
  // time is a pixel at the zoom and scroll shown. The page is scrolled at once and left to settle first: a box
  // measured while the page still moves puts the drag somewhere else.
  const settle = () => page.waitForFunction(() => new Promise((done) => {
    const y = scrollY;
    setTimeout(() => done(scrollY === y), 150);
  }));
  const scrollTo = async (selector, top) => {
    await page.evaluate(([sel, at]) => window.scrollTo({ top: document.querySelector(sel).getBoundingClientRect().top + scrollY - at, behavior: 'instant' }), [selector, top]);
    await settle();
  };
  const toRows = async () => {
    await page.evaluate(() => {
      const rows = [...document.querySelectorAll('.section, .cut')].map((el) => el.getBoundingClientRect().bottom);
      const bar = document.querySelector('.save-bar').getBoundingClientRect().top;
      const wave = document.querySelector('#waveform').getBoundingClientRect().top;
      const by = Math.min(Math.max(...rows) - (bar - 12), wave - 60); // the waveform stays below the caption
      window.scrollBy({ top: by, behavior: 'smooth' });
    });
    await pause(700);
    await settle();
  };
  const scrollSmooth = async (locator) => {
    await locator.evaluate((el) => el.scrollIntoView({ behavior: 'smooth', block: 'center' }));
    await pause(1100);
    await settle();
  };
  const wave = () => page.evaluate(() => {
    const ws = window.__editor.ws, b = ws.getWrapper().getBoundingClientRect();
    return { left: b.left, top: b.top, width: b.width, height: b.height, duration: ws.getDuration(), scroll: ws.getScroll() };
  });
  // drag moves the pointer across the waveform from one time to another (in seconds), slowly; with shift, it
  // draws a cut.
  const drag = async (from, to, shift = false) => {
    const w = await wave();
    expect(w.scroll).toBe(0); // not zoomed or moved along: x follows from the box alone
    const x = (s) => w.left + (s / w.duration) * w.width;
    const y = w.top + Math.min(w.height, 180) / 2;
    await page.mouse.move(x(from), y, { steps: 20 });
    await pause(500);
    if (shift) await page.keyboard.down('Shift');
    await page.mouse.down();
    for (let k = 1; k <= 6; k++) {
      await page.mouse.move(x(from + ((to - from) * k) / 6), y, { steps: 12 });
      await pause(120);
    }
    await page.mouse.up();
    if (shift) await page.keyboard.up('Shift');
    mouse = { x: x(to), y };
  };
  // seconds reads a time field ("2:41.97").
  const seconds = async (field) => (await field.inputValue()).split(':').reduce((a, b) => a * 60 + Number(b), 0);

  // 2. Mark the sermon by dragging across it on the waveform.
  await caption('2. Mark the sermon', 'Drag across it on the waveform');
  // The waveform and the rows under it (the sermon's and the cuts' times) in view together, above the save bar.
  await scrollTo('#minimap', 92);
  await pause(1200);
  await toRows();
  await pause(600);
  // The editor starts with a guess, the whole recording without its silent ends. A drag that starts on it would
  // move it, not make a new section, so it goes first.
  const sectionRow = page.locator('.section').first();
  if (await page.evaluate(() => window.__editor.sections.length)) {
    await caption('2. Mark the sermon', 'Remove the guess, then drag across the sermon');
    await click(sectionRow.getByRole('button', { name: T('Remove'), exact: true }));
    await expect.poll(() => page.evaluate(() => window.__editor.sections.length)).toBe(0);
    await settle();
    await pause(900);
  }
  await drag(22, 162);
  await expect.poll(() => page.evaluate(() => window.__editor.sections.length)).toBe(1);
  const [made] = await page.evaluate(() => window.__editor.sections);
  expect(Math.abs(made.start - 22)).toBeLessThan(1);
  expect(Math.abs(made.end - 162)).toBeLessThan(1);
  // The fields under the waveform say the same.
  expect(Math.abs((await seconds(page.getByLabel('Section from', { exact: true }))) - 22)).toBeLessThan(1);
  expect(Math.abs((await seconds(page.getByLabel('Section to', { exact: true }))) - 162)).toBeLessThan(1);
  await pause(2200);

  // 3. A cough inside the sermon: shift-drag over it, then give the cut a label.
  await caption('3. Cut out a cough', 'Hold shift and drag across it');
  await pause(1500);
  await drag(126, 130, true); // to the right of the caption, so the label on the waveform is seen
  await expect.poll(() => page.evaluate(() => window.__editor.cuts.length)).toBe(1);
  const [cut] = await page.evaluate(() => window.__editor.cuts);
  expect(Math.abs(cut.start - 126)).toBeLessThan(1);
  expect(Math.abs(cut.end - 130)).toBeLessThan(1);
  const [still] = await page.evaluate(() => window.__editor.sections);
  expect([still.start, still.end]).toEqual([made.start, made.end]); // the section is not moved
  await toRows();
  await pause(900);
  await caption('3. Cut out a cough', 'A label says what it was');
  await click(page.getByLabel('Cut 1 label', { exact: true }));
  await page.keyboard.type('cough', { delay: 160 });
  await expect(page.locator('#waveform')).toContainText('cough'); // on the cut in the waveform too
  await pause(1500);
  fs.writeFileSync(path.join(OUT, 'video-poster'), String(at()));
  await pause(1500);

  // 4. This sermon's own processing, in the editor.
  await caption('4. Choose how it sounds', 'In the Processing step: stronger leveling');
  const leveling = page.locator('section[aria-labelledby=step-processing] select[name=leveling]');
  await scrollSmooth(leveling);
  await pause(900);
  await choose(leveling, 'strong');
  await expect(leveling).toHaveValue('strong');
  await pause(2000);

  // 5. An intro for this sermon only: a few seconds of tone (run.sh makes it).
  await caption('5. Add an intro', 'Upload for this sermon only');
  const clipStep = page.locator('section[aria-labelledby=step-clips]');
  const intro = clipStep.locator('select[name=intro]');
  await scrollSmooth(intro);
  await pause(900);
  const clipChooser = page.waitForEvent('filechooser');
  await choose(intro, '__upload');
  await (await clipChooser).setFiles(path.join(DATA, 'Welcome.wav'));
  await expect(intro).toHaveValue('own', { timeout: 60_000 });
  await expect(intro.locator('option:checked')).toHaveText("This sermon's own: Welcome");
  await expect(clipStep).toContainText('With the intro and outro:');
  await pause(2500);

  // 6. Save: make the FLAC and the MP3, and watch the run's steps.
  await caption('6. Save: make the FLAC and the MP3');
  const output = page.locator('section[aria-labelledby=step-output]');
  await scrollSmooth(output);
  for (const [format, want] of [['flac', true], ['mp3', true], ['opus', false]]) {
    const pick = output.locator(`.pill[data-format=${format}] button.pick`);
    if ((await pick.getAttribute('aria-pressed')) !== String(want)) {
      await click(pick);
      await pause(500);
    }
  }
  await pause(800);
  await click(page.locator('.save-bar button.primary'));
  await caption('6. Save: make the FLAC and the MP3', 'The run\'s steps, one after another');
  const steps = page.locator('ul.steps');
  await expect(steps).toBeVisible({ timeout: 30_000 });
  await scrollSmooth(steps);
  await expect(page.getByRole('heading', { name: 'Done', exact: true })).toBeVisible({ timeout: 120_000 });
  await scrollSmooth(steps);
  await pause(2500);

  // 7. The files, on the recordings page.
  await caption('7. Download the files', 'With the intro, or the sermon only');
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
  // The sermon only, without the intro: the same two files.
  await click(menu.getByRole('button', { name: 'Sermon only', exact: true }));
  await expect(menu).toContainText('MP3');
  await pause(2200);
  await caption('');
  await pause(800);

  const video = page.video();
  await context.close();
  fs.copyFileSync(await video.path(), path.join(OUT, 'video.webm'));

  // What was saved is what the video shows: the drag, the cough, the processing and the intro, and the files.
  const dir = path.join(DATA, 'churches/grace-chapel/Taler/2026/2026-10-18');
  const saved = JSON.parse(fs.readFileSync(path.join(dir, 'recording.json'), 'utf8')).saved;
  expect(saved.sections).toHaveLength(1);
  expect(Math.abs(saved.sections[0].start - 22)).toBeLessThan(1);
  expect(Math.abs(saved.sections[0].end - 162)).toBeLessThan(1);
  expect(saved.cuts.map((c) => c.label)).toEqual(['cough']);
  expect(saved.sound.leveling).toBe('strong');
  expect(saved.intro).toBe('own');
  for (const f of ['clips/intro.flac', '2026-10-18.flac', '2026-10-18.mp3', '2026-10-18_sermon.flac', '2026-10-18_sermon.mp3']) {
    expect(fs.existsSync(path.join(dir, f)), f).toBe(true);
  }
});
