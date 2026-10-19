// Browser tests of the web UI against the real server and ffmpeg (see run.sh).
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });

// openEditor opens a date folder and waits until the waveform is ready. The
// saved sections and cuts of an earlier test are forgotten, unless keep is set.
async function openEditor(page, folder, { timeout = 60_000, keep = false } = {}) {
  if (!keep) fs.rmSync(path.join(DATA, 'root', folder, 'sermon.json'), { force: true });
  await page.goto('#/folder/' + folder);
  await expect(page.locator('#waveform')).toBeVisible({ timeout });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout });
}

const editor = (page, fn, arg) => page.evaluate(fn, arg);
const currentTime = (page) => editor(page, () => window.__editor.ws.getCurrentTime());
const sections = (page) => editor(page, () => window.__editor.sections);
// selection is the only section, or null.
const selection = async (page) => {
  const list = await sections(page);
  expect(list.length).toBeLessThanOrEqual(1);
  return list[0] || null;
};

test('folder list shows the date folders with their status', async ({ page }) => {
  await page.goto('');
  await page.getByRole('link', { name: '2026/' }).click();
  await expect(page.getByRole('heading', { name: '2026' })).toBeVisible();
  const row = page.locator('tr', { hasText: '2026-10-04' });
  await expect(row).toContainText('raw');
  await row.click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-10-04$/);
});

test('cut on the waveform: drag, fields, keys, playback', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await expect(page.locator('#minimap')).toBeVisible();
  // The raw WAV was converted to FLAC before the waveform was made.
  await expect(page.getByText('Recording: 2026-10-04_raw.flac')).toBeVisible();

  // Dragging on the waveform selects; the fields follow.
  const box = await page.locator('#waveform').boundingBox();
  const x = (t) => box.x + (t / 100) * box.width;
  await page.mouse.move(x(30), box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(x(50), box.y + box.height / 2, { steps: 8 });
  await page.mouse.move(x(70), box.y + box.height / 2, { steps: 8 });
  await page.mouse.up();
  let sel = await selection(page);
  expect(sel.start).toBeGreaterThan(27);
  expect(sel.start).toBeLessThan(33);
  expect(sel.end).toBeGreaterThan(67);
  expect(sel.end).toBeLessThan(73);
  await expect(page.getByLabel('Sermon from')).toHaveValue(/^0:[23]\d\.\d\d$/);

  // Typing in the fields moves the selection.
  await page.getByLabel('Sermon from').fill('0:21');
  await page.getByLabel('Sermon from').press('Enter');
  await page.getByLabel('Sermon to').fill('79.5');
  await page.getByLabel('Sermon to').press('Enter');
  sel = await selection(page);
  expect(sel.start).toBeCloseTo(21, 2);
  expect(sel.end).toBeCloseTo(79.5, 2);
  await expect(page.getByText('Length 0:58.5', { exact: true })).toBeVisible();
  await page.getByLabel('Sermon to').fill('nonsense');
  await page.getByLabel('Sermon to').press('Enter');
  await expect(page.getByLabel('Sermon to')).toHaveClass(/invalid/);
  expect((await selection(page)).end).toBeCloseTo(79.5, 2);

  // Keys: ] jumps to silence edges, s/e mark at the playhead...
  await page.locator('h1').click(); // focus out of the fields
  await page.keyboard.press(']');
  expect(await currentTime(page)).toBeCloseTo(19, 0);
  await page.keyboard.press(']');
  expect(await currentTime(page)).toBeCloseTo(21, 0);
  await page.keyboard.press('ArrowRight');
  await page.keyboard.press('s');
  expect((await selection(page)).start).toBeCloseTo(22, 0);
  // ...but not while typing in a field.
  await page.getByLabel('Sermon from').focus();
  const before = await currentTime(page);
  await page.keyboard.press(']');
  expect(await currentTime(page)).toBe(before);

  // Headless Chromium plays and seeks the MP3 preview.
  await page.locator('h1').click();
  await editor(page, () => window.__editor.ws.setTime(50));
  await page.keyboard.press(' ');
  await page.waitForFunction(() => window.__editor.ws.getCurrentTime() > 50.5, null, { timeout: 10_000 });
  await page.keyboard.press(' ');
  await page.screenshot({ path: path.join(shots, 'editor.png'), fullPage: true });
});

test('scroll zooms at the mouse, sideways moves along; settings can make it need ctrl', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const box = await page.locator('#waveform').boundingBox();
  const mx = box.x + box.width * 0.6, my = box.y + box.height / 2;
  const zoom = async () => +(await page.getByLabel('Zoom').inputValue());
  const scroll = () => editor(page, () => window.__editor.ws.getScroll());
  const timeAt = (cx) => editor(page, (cx) => {
    const ws = window.__editor.ws, b = ws.getWrapper().getBoundingClientRect();
    return ((cx - b.left) / b.width) * ws.getDuration();
  }, cx);

  // A plain scroll up zooms in, keeping the time under the mouse.
  await page.mouse.move(mx, my);
  const under = await timeAt(mx);
  for (let i = 0; i < 6; i++) await page.mouse.wheel(0, -100);
  await expect.poll(zoom).toBeGreaterThan(25);
  expect(await timeAt(mx)).toBeCloseTo(under, 0);
  expect(await scroll()).toBeGreaterThan(0);

  // Sideways scrolling moves along the waveform without zooming.
  const z = await zoom(), left = await scroll();
  await page.mouse.wheel(-200, 0);
  await expect.poll(scroll).toBeLessThan(left);
  expect(await zoom()).toBe(z);

  // Scroll down zooms back out.
  for (let i = 0; i < 10; i++) await page.mouse.wheel(0, 100);
  await expect.poll(zoom).toBe(0);

  // With the setting, a plain scroll leaves the zoom alone; ctrl+scroll zooms.
  await page.goto('#/settings');
  await page.getByLabel(/Hold Ctrl/).check();
  await openEditor(page, '2026/2026-10-04');
  await expect(page.getByText('+scroll zooms at the mouse')).toBeVisible();
  await page.mouse.move(mx, my);
  await page.mouse.wheel(0, -300);
  await page.waitForTimeout(200);
  expect(await zoom()).toBe(0);
  await page.keyboard.down('Control');
  await page.mouse.wheel(0, -300);
  await page.keyboard.up('Control');
  await expect.poll(zoom).toBeGreaterThan(0);
});

test('cut parts out of the sermon: button, fields, shift-drag, checked and removed', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const cuts = () => editor(page, () => window.__editor.cuts);
  // Navigation and cutting are separate groups; cutting needs the sermon first.
  await expect(page.locator('.group', { hasText: 'Navigate' }).getByRole('button', { name: 'silence ▶' })).toBeVisible();
  await expect(page.locator('.group', { hasText: 'Sermon' }).getByRole('button', { name: 'Set start' })).toBeVisible();
  const cutOut = page.locator('.group', { hasText: 'Cut out' });
  await expect(cutOut.getByRole('button', { name: 'Cut out here' })).toBeDisabled();

  await editor(page, () => window.__editor.addSection(10, 90));
  await expect(cutOut.getByRole('button', { name: 'Cut out here' })).toBeEnabled();
  await expect(page.getByText('Length 1:20.0', { exact: true })).toBeVisible();

  // The button cuts out 10 s from the playhead; the fields adjust it.
  await editor(page, () => window.__editor.ws.setTime(30));
  await cutOut.getByRole('button', { name: 'Cut out here' }).click();
  expect((await cuts())[0].start).toBeCloseTo(30, 0);
  expect((await cuts())[0].end).toBeCloseTo(40, 0);
  await expect(page.getByText('Length 1:10.0 (1 cut)')).toBeVisible();
  await page.getByLabel('Cut 1 to').fill('0:45');
  await page.getByLabel('Cut 1 to').press('Enter');
  expect((await cuts())[0].end).toBeCloseTo(45, 2);

  // Shift-drag on the waveform draws another one; the list stays in time order.
  const box = await page.locator('#waveform').boundingBox();
  const x = (t) => box.x + (t / 100) * box.width;
  await page.keyboard.down('Shift');
  await page.mouse.move(x(60), box.y + box.height / 2);
  await page.mouse.down();
  await page.mouse.move(x(65), box.y + box.height / 2, { steps: 6 });
  await page.mouse.move(x(70), box.y + box.height / 2, { steps: 6 });
  await page.mouse.up();
  await page.keyboard.up('Shift');
  await expect(page.getByLabel('Cut 2 from')).toBeVisible();
  const both = await cuts();
  expect(both).toHaveLength(2);
  expect(both[1].start).toBeGreaterThan(57);
  expect(both[1].end).toBeLessThan(73);
  // The section is untouched.
  expect(await selection(page)).toEqual({ start: 10, end: 90 });

  // Playback jumps over a cut.
  await editor(page, () => window.__editor.ws.setTime(28));
  await page.locator('h1').click();
  await page.keyboard.press(' ');
  await page.waitForFunction(() => window.__editor.ws.getCurrentTime() > 45.2, null, { timeout: 15_000 });
  await page.keyboard.press(' ');

  // A cut outside the sermon is flagged and the run refused with the reason.
  await editor(page, () => window.__editor.addCut(92, 95));
  await expect(page.locator('.cut.outside')).toHaveCount(1);
  await expect(page.locator('.error')).toContainText('inside the sermon');
  await page.locator('.cut.outside').getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.cut')).toHaveCount(2);
  await expect(page.getByRole('button', { name: 'Run' })).toBeEnabled();
  await page.screenshot({ path: path.join(shots, 'cuts.png'), fullPage: true });

  await page.locator('.cut').first().getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.cut')).toHaveCount(1);
  expect(await cuts()).toHaveLength(1);
});

test('several sections: drag to add, gaps left out, overlaps merge, keys, removed', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const box = await page.locator('#waveform').boundingBox();
  const x = (t) => box.x + (t / 100) * box.width;
  const drag = async (from, to) => {
    await page.mouse.move(x(from), box.y + box.height / 2);
    await page.mouse.down();
    await page.mouse.move(x((from + to) / 2), box.y + box.height / 2, { steps: 6 });
    await page.mouse.move(x(to), box.y + box.height / 2, { steps: 6 });
    await page.mouse.up();
  };

  // Each drag on an empty part adds a section; the list is in time order.
  await drag(50, 70);
  await drag(10, 30);
  await expect(page.getByLabel('Section 2 from')).toBeVisible();
  let list = await sections(page);
  expect(list).toHaveLength(2);
  expect(list[0].start).toBeCloseTo(10, 0);
  expect(list[1].start).toBeCloseTo(50, 0);
  await page.getByLabel('Section 1 from').fill('10');
  await page.getByLabel('Section 1 from').press('Enter');
  await page.getByLabel('Section 1 to').fill('30');
  await page.getByLabel('Section 1 to').press('Enter');
  await page.getByLabel('Section 2 from').fill('50');
  await page.getByLabel('Section 2 from').press('Enter');
  await page.getByLabel('Section 2 to').fill('70');
  await page.getByLabel('Section 2 to').press('Enter');
  await expect(page.getByText('Length 0:40.0 (2 sections)')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run' })).toBeEnabled();

  // A cut in a section counts; the run check sees the gap as left out.
  await editor(page, () => window.__editor.addCut(60, 65));
  await expect(page.getByText('Length 0:35.0 (2 sections and 1 cut)')).toBeVisible();

  // Playing the sermon jumps over the gap between the sections.
  await editor(page, () => window.__editor.ws.setTime(29));
  await page.locator('h1').click();
  await page.keyboard.press(' ');
  await page.waitForFunction(() => window.__editor.ws.getCurrentTime() > 50.2, null, { timeout: 15_000 });
  await page.keyboard.press(' ');

  // Between sections, s starts the next one at the playhead and e ends the one before.
  await editor(page, () => window.__editor.ws.setTime(40));
  await page.keyboard.press('s');
  await editor(page, () => window.__editor.ws.setTime(35));
  await page.keyboard.press('e');
  expect(await sections(page)).toEqual([{ start: 10, end: 35 }, { start: 40, end: 70 }]);

  // Sections that come to overlap become one.
  await page.getByLabel('Section 1 to').fill('45');
  await page.getByLabel('Section 1 to').press('Enter');
  expect(await sections(page)).toEqual([{ start: 10, end: 70 }]);
  await expect(page.getByLabel('Sermon from')).toHaveValue('0:10.00');

  // Removing the last section leaves nothing selected, and nothing to run.
  await page.locator('.section').getByRole('button', { name: 'Remove' }).click();
  expect(await sections(page)).toEqual([]);
  await expect(page.getByText('Drag on the waveform to select the sermon, or')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Run' })).toBeDisabled();
});

test('sermon.json keeps the adjustments; Save makes them the saved version, Revert goes back', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const file = path.join(DATA, 'root/2026/2026-10-04/sermon.json');
  const read = () => JSON.parse(fs.readFileSync(file, 'utf8'));
  await expect(page.getByRole('button', { name: 'Save…' })).toBeDisabled();
  await expect(page.getByText('Nothing is saved yet.')).toBeVisible();

  // Adjusting is kept as you go, but nothing is saved yet.
  await editor(page, () => { window.__editor.addSection(12, 88); window.__editor.addCut(30, 40); window.__editor.addCut(60, 66); });
  await page.getByLabel('Fade in (s)').fill('2');
  await expect(page.getByText('Changes kept.')).toBeVisible();
  await expect.poll(() => read().adjusted?.fadeIn).toBe(2);
  expect(read()).toMatchObject({
    saved: null,
    adjusted: { sections: [{ start: 12, end: 88 }], cuts: [{ start: 30, end: 40 }, { start: 60, end: 66 }], fadeIn: 2, fadeOut: 0 },
  });

  // Another visit brings the adjustments back: the section, both cuts and the fade.
  await openEditor(page, '2026/2026-10-04', { keep: true });
  await page.reload();
  await page.waitForFunction(() => window.__editor && window.__editor.cuts.length === 2, null, { timeout: 30_000 });
  expect(await selection(page)).toEqual({ start: 12, end: 88 });
  await expect(page.getByLabel('Cut 2 to')).toHaveValue('1:06.00');
  await expect(page.getByLabel('Fade in (s)')).toHaveValue('2');
  await expect(page.getByText('Length 1:00.0 (2 cuts)')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Revert…' })).toBeDisabled(); // nothing to go back to yet

  // Save asks first; Cancel changes nothing.
  await page.getByRole('button', { name: 'Save…' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('The saved version will be');
  await expect(dialog).toContainText('2 cuts');
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(read().saved).toBeNull();
  await page.getByRole('button', { name: 'Save…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Saved.', { exact: true })).toBeVisible();
  expect(read()).toMatchObject({ adjusted: null, saved: { sections: [{ start: 12, end: 88 }], fadeIn: 2 } });
  expect(read().savedAt).toBeTruthy();
  await expect(page.getByRole('button', { name: 'Save…' })).toBeDisabled();

  // Adjust again: the saved version stays as it was, next to the adjustments.
  await page.locator('.cut').first().getByRole('button', { name: 'Remove' }).click();
  await page.getByLabel('Fade in (s)').fill('0');
  await expect.poll(() => read().adjusted?.cuts.length).toBe(1);
  expect(read().saved.cuts).toHaveLength(2);
  await expect(page.getByText('Adjusted, not saved:')).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'version.png'), fullPage: true });

  // Revert asks first, then everything is back as it was saved.
  await page.getByRole('button', { name: 'Revert…' }).click();
  await expect(page.getByRole('dialog')).toContainText('Your adjustments are discarded');
  await page.getByRole('dialog').getByRole('button', { name: 'Cancel' }).click();
  expect(await editor(page, () => window.__editor.cuts.length)).toBe(1);
  await page.getByRole('button', { name: 'Revert…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Revert' }).click();
  await expect(page.getByLabel('Cut 2 to')).toHaveValue('1:06.00');
  expect(await editor(page, () => window.__editor.cuts.length)).toBe(2);
  await expect(page.getByLabel('Fade in (s)')).toHaveValue('2');
  expect(read().adjusted).toBeNull();
  await expect(page.getByRole('button', { name: 'Revert…' })).toBeDisabled();
});

test('run with chosen outputs and follow it live', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await page.getByLabel('Sermon from').fill('0:21');
  await page.getByLabel('Sermon from').press('Enter');
  await page.getByLabel('Sermon to').fill('1:19');
  await page.getByLabel('Sermon to').press('Enter');
  await page.getByLabel('FLAC master').uncheck();
  await page.getByLabel('Opus', { exact: true }).uncheck();
  await page.getByLabel('Fade in (s)').fill('1');
  await expect(page.getByRole('button', { name: 'Run' })).toBeEnabled();
  await page.getByRole('button', { name: 'Run' }).click();
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 60_000 });
  await expect(page.locator('.outputs')).toContainText('2026-10-04.mp3');
  await expect(page.locator('.outputs audio')).toHaveCount(1);
  // Downloads straight from the page.
  const [download] = await Promise.all([page.waitForEvent('download'), page.locator('.outputs').getByRole('link', { name: 'Download' }).click()]);
  expect(download.suggestedFilename()).toBe('2026-10-04.mp3');
  await expect(page.getByText('Original: 2026-10-04_raw.flac')).toBeVisible(); // moved into original/ by the run
  await page.screenshot({ path: path.join(shots, 'done.png'), fullPage: true });

  const files = fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04')).sort();
  expect(files).toEqual(['2026-10-04.mp3', 'original', 'sermon.json']); // the settings are kept, the recording is in original/
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04/original'))).toEqual(['2026-10-04_raw.flac']);
  // The options are remembered for the next folder (not Force or the cut).
  await page.reload();
  await expect(page.getByLabel('FLAC master')).not.toBeChecked();
  await expect(page.getByLabel('Fade in (s)')).toHaveValue('1');
  // The files stay listed, and the finished run is shown again.
  await expect(page.locator('.outputs')).toContainText('2026-10-04.mp3');
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible();
});

test('a run can be cancelled', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await page.getByLabel('Sermon from').fill('0:01');
  await page.getByLabel('Sermon from').press('Enter');
  await page.getByLabel('FLAC master').check();
  // Making everything again asks first; Cancel in the dialog changes nothing.
  await expect(page.getByLabel('Force')).toHaveCount(0);
  await page.getByRole('button', { name: 'Save and generate' }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('These files are replaced');
  await expect(dialog).toContainText('2026-10-04.mp3');
  await page.screenshot({ path: path.join(shots, 'dialog.png') });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(dialog).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Processing…' })).toHaveCount(0);
  await page.getByRole('button', { name: 'Save and generate' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Save and generate' }).click();
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Cancelled' })).toBeVisible({ timeout: 30_000 });
  const left = fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04')).filter((f) => f.includes('.partial') || f.startsWith('.'));
  expect(left).toEqual([]);
});

test('upload straight into a new date folder, in chunks', async ({ page }) => {
  await page.goto('');
  await page.evaluate(() => localStorage.setItem('uploadChunk', String(64 * 1024))); // several chunks
  await page.getByRole('link', { name: 'Upload' }).click();
  await page.locator('input[type=file]').setInputFiles({
    name: 'Gudstjeneste 2026-09-27.flac',
    mimeType: 'audio/flac',
    buffer: fs.readFileSync(path.join(DATA, 'upload.flac')),
  });
  await expect(page.getByLabel('Date')).toHaveValue('2026-09-27');
  await expect(page.getByText('from the name')).toBeVisible();
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-09-27$/);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 });
  const dir = path.join(DATA, 'root/2026/2026-09-27');
  expect(fs.readdirSync(dir)).toEqual(['2026-09-27_raw.flac']);
  expect(fs.readFileSync(path.join(dir, '2026-09-27_raw.flac')).equals(fs.readFileSync(path.join(DATA, 'upload.flac')))).toBe(true);
  // The date now has a recording: a second upload to it is refused.
  await page.getByRole('link', { name: 'Upload' }).click();
  await page.locator('input[type=file]').setInputFiles({ name: 'again 2026-09-27.flac', mimeType: 'audio/flac', buffer: Buffer.from('x') });
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page.getByText('already has a raw recording')).toBeVisible();
});

test('inbox: a recording lands in its date folder by itself', async ({ page }) => {
  await page.goto('#/');
  await expect(page.getByRole('link', { name: 'Inbox' })).toHaveCount(0);
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(DATA, 'inbox', 'Gudstjeneste 2026-09-20.flac'));
  await page.goto('#/folders/2026');
  // The server takes it once it has been quiet for a while; the list is read on load.
  await expect(async () => {
    await page.reload();
    await expect(page.getByRole('link', { name: '2026-09-20' })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
  await page.getByRole('link', { name: '2026-09-20' }).click();
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 });
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-09-20'))).toEqual(['2026-09-20_raw.flac']);
  expect(fs.readdirSync(path.join(DATA, 'inbox'))).toEqual([]);
});

test('a 90-minute recording stays responsive', async ({ page }) => {
  test.setTimeout(300_000);
  const started = Date.now();
  await openEditor(page, '2026/2026-08-30', { timeout: 240_000 });
  console.log(`analysis + first render: ${((Date.now() - started) / 1000).toFixed(1)} s`);
  expect(await editor(page, () => window.__editor.ws.getDuration())).toBeCloseTo(5400, 0);

  // Zoom all the way in, seek near the end, select, and play: each step quick.
  const timed = async (label, fn) => {
    const t0 = Date.now();
    await fn();
    const ms = Date.now() - t0;
    console.log(`${label}: ${ms} ms`);
    expect(ms, label).toBeLessThan(3000);
  };
  await page.locator('h1').click();
  await timed('zoom in fully', async () => {
    for (let i = 0; i < 10; i++) await page.keyboard.press('+');
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  });
  await timed('seek to 1:20:00', async () => {
    await editor(page, () => window.__editor.ws.setTime(4800));
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(r)));
  });
  await timed('select 1:00:00 to 1:25:00', async () => {
    await page.getByLabel('Sermon from').fill('1:00:00');
    await page.getByLabel('Sermon from').press('Enter');
    await page.getByLabel('Sermon to').fill('1:25:00');
    await page.getByLabel('Sermon to').press('Enter');
  });
  expect((await selection(page)).start).toBeCloseTo(3600, 1);
  await page.locator('h1').click();
  await page.keyboard.press(' ');
  await page.waitForFunction(() => window.__editor.ws.getCurrentTime() > 4800.5, null, { timeout: 15_000 });
  await page.keyboard.press(' ');
  await page.screenshot({ path: path.join(shots, 'long.png') });
});

test('churches: add one, folders are separate, the choice is remembered', async ({ page }) => {
  await page.goto('#/');
  await expect(page.locator('#church')).toHaveValue('first-church');
  await expect(page.getByRole('link', { name: '2026/' })).toBeVisible();
  await page.locator('#church').selectOption('+');
  await page.getByRole('dialog').getByLabel('Name of the new church').fill('Second church');
  await page.getByRole('dialog').getByRole('button', { name: 'Add church' }).click();
  await expect(page.locator('#church')).toHaveValue('second-church');
  await expect(page.getByText('No folders here yet.')).toBeVisible();
  await page.reload();
  await expect(page.locator('#church')).toHaveValue('second-church');
  await page.locator('#church').selectOption('first-church');
  await expect(page.getByRole('link', { name: '2026/' })).toBeVisible();
});

test('settings: default tags are saved and used on the sermon page', async ({ page }) => {
  await page.goto('#/settings');
  await expect(page.locator('input[name=title]')).toHaveValue('{yyyy}-{mm}-{dd}');
  await page.locator('input[name=title]').fill('{nope}');
  await expect(page.getByText('unknown placeholder {nope}')).toBeVisible();
  await expect(page.locator('button[name=save-tags]')).toBeDisabled();
  await page.locator('input[name=title]').fill('{date} {name}');
  await page.locator('input[name=artist]').fill('Norkirken Melhus');
  await expect(page.locator('.tags').last()).toContainText('→ Norkirken Melhus');
  await page.locator('button[name=save-tags]').click();
  await expect(page.locator('.tags').last().getByText('Saved.')).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'settings.png') });

  await page.goto('#/folder/2026/2026-10-04');
  await expect(page.locator('input[name=title]')).toHaveValue('2026-10-04');
  await expect(page.locator('input[name=artist]')).toHaveValue('Norkirken Melhus');
});

test('a second sermon the same day: upload with a name, then rename', async ({ page }) => {
  await page.goto('#/upload');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-11-01');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-11-01$/);

  await page.goto('#/upload');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-11-01');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page.getByText('give it a name')).toBeVisible();
  await page.locator('input[aria-label=Name]').fill('Kveld');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-11-01%20Kveld$|#\/folder\/2026\/2026-11-01 Kveld$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('2026-11-01 Kveld');

  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 }); // prepared: it can be renamed
  await page.getByRole('button', { name: 'Rename…' }).click();
  await page.locator('input[aria-label=Name]').fill('Aften');
  await page.getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toContainText('2026-11-01 Aften', { timeout: 10_000 });
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-11-01 Aften'))).toContain('2026-11-01 Aften_raw.flac');
});

test('names: remembered, suggested, and tidied in settings', async ({ page }) => {
  // Naming a sermon on upload remembers the name (others may be there from earlier tests).

  await page.goto('#/upload');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-12-06');
  await page.locator('input[aria-label=Name]').fill('Engangs');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-12-06/);

  await page.goto('#/upload');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await expect(page.locator('datalist option[value=Engangs]')).toHaveCount(1);

  await page.goto('#/settings');
  await expect(page.locator('ul.names')).toContainText('Engangs');
  await page.screenshot({ path: path.join(shots, 'settings-names.png') });
  await page.getByRole('button', { name: 'Remove Engangs' }).click();
  await expect(page.locator('ul.names')).not.toContainText('Engangs');
  await page.getByLabel('Suggest names').uncheck();
  await page.reload();
  await expect(page.getByLabel('Suggest names')).not.toBeChecked();
  await page.getByLabel('Suggest names').check();
});

test('settings: rename the church; the dropdown follows', async ({ page }) => {
  await page.goto('#/settings');
  await page.locator('input[name=church]').fill('Norkirken Melhus');
  await page.locator('button[name=save-church]').click();
  await expect(page.getByText('Saved.')).toBeVisible();
  await expect(page.locator('#church option:checked')).toHaveText('Norkirken Melhus');
  await page.reload();
  await expect(page.locator('input[name=church]')).toHaveValue('Norkirken Melhus');
});

test('shares: make a code in the studio, send a recording with the link', async ({ page, context }) => {
  await page.goto('#/shares');
  await page.locator('input[name=label]').fill('Sound desk');
  await page.locator('input[name=maxUploads]').fill('3');
  await page.locator('input[name=expires]').fill('2099-02-30');
  await page.getByRole('button', { name: 'Make share' }).click();
  await expect(page.getByText('Write the expiry as yyyy-mm-dd')).toBeVisible();
  await page.locator('input[name=expires]').fill('2099-12-31');
  await page.getByRole('button', { name: 'Make share' }).click();
  const dialog = page.getByRole('dialog');
  const link = await dialog.locator('.share-link').textContent();
  expect(link).toMatch(/^http:\/\/localhost:8081\/#code=[A-Z2-9]{4}(-[A-Z2-9]{4}){3}$/);
  await page.screenshot({ path: path.join(shots, 'share-made.png') });
  await dialog.getByRole('button', { name: 'OK' }).click();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toContainText('open');

  // The link fills in the code, which then leaves the address bar.
  const up = await context.newPage();
  await up.goto(link);
  await expect(up.getByRole('heading', { name: 'Sound desk' })).toBeVisible();
  expect(up.url()).not.toContain('code');
  await expect(up.getByText('3 more files · until 2099-12-31 23:59')).toBeVisible();
  await up.locator('input[type=file]').setInputFiles({
    name: 'Gudstjeneste 2026-09-13.flac', mimeType: 'audio/flac',
    buffer: fs.readFileSync(path.join(DATA, 'upload.flac')),
  });
  await expect(up.getByText('Received. Thank you!')).toBeVisible({ timeout: 30_000 });
  await expect(up.getByText('2 more files')).toBeVisible();
  await up.screenshot({ path: path.join(shots, 'share-upload.png') });
  const raw = path.join(DATA, 'root', '2026', '2026-09-13', '2026-09-13_raw.flac');
  await expect.poll(() => fs.existsSync(raw), { timeout: 30_000 }).toBe(true);
  expect(fs.readFileSync(raw).equals(fs.readFileSync(path.join(DATA, 'upload.flac')))).toBe(true);

  // The studio lists the upload.
  await page.reload();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toContainText('1 file received');

  // A wrong code is refused.
  const other = await context.newPage();
  await other.goto('http://localhost:8081/');
  await other.getByLabel('Code').fill('AAAA-BBBB-CCCC-DDDD');
  await other.getByRole('button', { name: 'Continue' }).click();
  await expect(other.getByRole('alert')).toContainText('unknown code');

  // Revoked: the code no longer works.
  page.once('dialog', () => {});
  await page.locator('.share', { hasText: 'Sound desk' }).getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Revoke' }).click();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toContainText('revoked');
  await up.reload();
  await expect(up.getByRole('alert')).toContainText('withdrawn');
});
