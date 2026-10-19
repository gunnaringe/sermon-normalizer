// Browser tests of the web UI against the real server and ffmpeg (see run.sh).
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });

// openEditor opens a date folder and waits until the waveform is ready.
async function openEditor(page, folder, timeout = 60_000) {
  await page.goto('#/folder/' + folder);
  await expect(page.locator('#waveform')).toBeVisible({ timeout });
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout });
}

const editor = (page, fn, arg) => page.evaluate(fn, arg);
const currentTime = (page) => editor(page, () => window.__editor.ws.getCurrentTime());
const selection = (page) => editor(page, () => {
  const s = window.__editor.selection;
  return s ? { start: s.start, end: s.end } : null;
});

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
  await expect(page.getByLabel('Start')).toHaveValue(/^0:[23]\d\.\d\d$/);

  // Typing in the fields moves the selection.
  await page.getByLabel('Start').fill('0:21');
  await page.getByLabel('Start').press('Enter');
  await page.getByLabel('End').fill('79.5');
  await page.getByLabel('End').press('Enter');
  sel = await selection(page);
  expect(sel.start).toBeCloseTo(21, 2);
  expect(sel.end).toBeCloseTo(79.5, 2);
  await expect(page.getByText('Length 0:58.5')).toBeVisible();
  await page.getByLabel('End').fill('nonsense');
  await page.getByLabel('End').press('Enter');
  await expect(page.getByLabel('End')).toHaveClass(/invalid/);
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
  await page.getByLabel('Start').focus();
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

test('cut parts out of the sermon: button, fields, shift-drag, checked and removed', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const cuts = () => editor(page, () => window.__editor.cuts);
  // Navigation and cutting are separate groups; cutting needs the sermon first.
  await expect(page.locator('.group', { hasText: 'Navigate' }).getByRole('button', { name: 'silence ▶' })).toBeVisible();
  await expect(page.locator('.group', { hasText: 'Sermon' }).getByRole('button', { name: 'Set start' })).toBeVisible();
  const cutOut = page.locator('.group', { hasText: 'Cut out' });
  await expect(cutOut.getByRole('button', { name: 'Cut out here' })).toBeDisabled();

  await editor(page, () => window.__editor.setSelection(10, 90));
  await expect(cutOut.getByRole('button', { name: 'Cut out here' })).toBeEnabled();
  await expect(page.getByText('Length 1:20.0')).toBeVisible();

  // The button cuts out 10 s from the playhead; the fields adjust it.
  await editor(page, () => window.__editor.ws.setTime(30));
  await cutOut.getByRole('button', { name: 'Cut out here' }).click();
  expect((await cuts())[0].start).toBeCloseTo(30, 0);
  expect((await cuts())[0].end).toBeCloseTo(40, 0);
  await expect(page.getByText('Length 1:10.0 after the cuts')).toBeVisible();
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
  // The selection is untouched.
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
  await expect(page.locator('.error')).toContainText('inside the selected sermon');
  await page.locator('.cut.outside').getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.cut')).toHaveCount(2);
  await expect(page.getByRole('button', { name: 'Run' })).toBeEnabled();
  await page.screenshot({ path: path.join(shots, 'cuts.png'), fullPage: true });

  await page.locator('.cut').first().getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.cut')).toHaveCount(1);
  expect(await cuts()).toHaveLength(1);
});

test('run with chosen outputs and follow it live', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await page.getByLabel('Start').fill('0:21');
  await page.getByLabel('Start').press('Enter');
  await page.getByLabel('End').fill('1:19');
  await page.getByLabel('End').press('Enter');
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
  await expect(page.getByText('Sources: 2026-10-04_raw.flac')).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'done.png'), fullPage: true });

  const files = fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04')).sort();
  expect(files).toEqual(['2026-10-04.mp3', '2026-10-04_raw.flac']);
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
  await page.getByLabel('Start').fill('0:01');
  await page.getByLabel('Start').press('Enter');
  await page.getByLabel('FLAC master').check();
  await page.getByLabel('Force (redo existing)').check();
  await page.getByRole('button', { name: 'Run' }).click();
  await page.getByRole('button', { name: 'Cancel' }).click();
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
  await openEditor(page, '2026/2026-08-30', 240_000);
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
    await page.getByLabel('Start').fill('1:00:00');
    await page.getByLabel('Start').press('Enter');
    await page.getByLabel('End').fill('1:25:00');
    await page.getByLabel('End').press('Enter');
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
  page.once('dialog', (d) => d.accept('Second church'));
  await page.locator('#church').selectOption('+');
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
  await page.locator('input[type=date]').fill('2026-11-01');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/folder\/2026\/2026-11-01$/);

  await page.goto('#/upload');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[type=date]').fill('2026-11-01');
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
  await page.locator('input[type=date]').fill('2026-12-06');
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
