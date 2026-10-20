// Browser tests of the web UI against the real server and ffmpeg (see run.sh).
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
fs.mkdirSync(shots, { recursive: true });

// forget drops the saved and adjusted sermon of a folder from its
// recording.json; what is known about the recording and the outputs stays.
function forget(folder) {
  const file = path.join(DATA, 'root', folder, 'recording.json');
  if (!fs.existsSync(file)) return;
  const st = JSON.parse(fs.readFileSync(file, 'utf8'));
  fs.writeFileSync(file, JSON.stringify({ ...st, saved: null, savedAt: undefined, adjusted: null }));
}

// openEditor opens a date folder and waits until the waveform is ready. The
// saved sections and cuts of an earlier test are forgotten, unless keep is set.
async function openEditor(page, folder, { timeout = 60_000, keep = false } = {}) {
  if (!keep) forget(folder);
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

test('the recordings page shows the years and the recordings with their status', async ({ page }) => {
  await page.goto('');
  await expect(page.locator('button.year-head', { hasText: '2026' })).toHaveAttribute('aria-expanded', 'true'); // the newest year is open
  const row = page.locator('.year tr', { hasText: '2026-10-04' });
  await expect(row).toContainText('to do');
  await row.click();
  await expect(row.locator('xpath=following-sibling::tr[1]').getByRole('link', { name: 'Open in the editor' })).toBeVisible();
  await row.getByRole('button', { name: 'Show details' }).click();
  await expect(page.locator('tr.detail-row')).toHaveCount(0);
});

test('cut on the waveform: drag, fields, keys, playback', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await expect(page.locator('#minimap')).toBeVisible();
  // The raw WAV was converted to FLAC before the waveform was made.
  await expect(page.locator('.input-file')).toContainText('2026-10-04_raw.flac');

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
  await page.getByLabel(/holding Ctrl/).check();
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

  // A cut outside the sermon is allowed, with a warning by it that it does nothing; Save is not blocked.
  await editor(page, () => window.__editor.addCut(92, 95));
  await expect(page.locator('.cut.outside')).toHaveCount(1);
  await expect(page.locator('.cut.outside .cut-warn')).toContainText('does not have any effect as it is not in an included section');
  await expect(page.locator('.error')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();
  // One that sticks out of the sermon is partly in it.
  await editor(page, () => window.__editor.addCut(85, 93));
  await expect(page.locator('.cut.partly .cut-warn')).toContainText('is only partially part of a section');
  await page.locator('.cut.partly').getByRole('button', { name: 'Remove' }).click();
  // A label on a section and on a cut: kept with the edit.
  await page.getByLabel('Sermon label').fill('Main part');
  await page.getByLabel('Cut 1 label').fill('Song');
  await expect.poll(() => {
    const st = JSON.parse(fs.readFileSync(path.join(DATA, 'root/2026/2026-10-04/recording.json'), 'utf8'));
    const set = st.adjusted || st.saved;
    return [set.sections[0].label, set.cuts[0].label];
  }, { timeout: 10_000 }).toEqual(['Main part', 'Song']);
  await page.screenshot({ path: path.join(shots, 'cuts-warnings.png'), fullPage: true });
  await page.locator('.cut.outside').getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.cut')).toHaveCount(2);
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
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeEnabled();

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
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
});

test('recording.json keeps the adjustments; Save makes them the saved version, Revert goes back', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  const file = path.join(DATA, 'root/2026/2026-10-04/recording.json');
  const read = () => JSON.parse(fs.readFileSync(file, 'utf8'));
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
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

  // Save makes it the saved version (with no format chosen, it makes no files).
  for (const label of ['FLAC', 'MP3', 'Opus']) await pick(page, label).click();
  await expect(page.getByText('Save keeps the edit.')).toBeVisible();
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByText('Saved.', { exact: true })).toBeVisible();
  expect(read()).toMatchObject({ adjusted: null, saved: { sections: [{ start: 12, end: 88 }], fadeIn: 2 } });
  expect(read().savedAt).toBeTruthy();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await expect(page.getByText('Saved, and everything chosen is up to date.')).toBeVisible();

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

// pill is a format's pill on the Output; pick is its button.
const pill = (page, label) => page.locator(`.pill[data-format=${label.toLowerCase()}]`);
const pick = (page, label) => pill(page, label).locator('button.pick');

test('the page: Input file, Edit, Options, Output', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await expect(page.locator('section.step > h2')).toHaveText(['Input file', 'Edit', 'Options', 'Output']);

  // Input file: the recording, where it came from, its channels, a download.
  const input = page.locator('.input-file');
  await expect(input).toContainText('FLAC · 48 kHz · 24-bit · 2 channels');
  await expect(input).toContainText('converted from the original WAV (2026-10-04_raw.wav');
  await expect(input).toContainText('Mono (both channels are the same)', { timeout: 30_000 });
  await expect(input).toContainText('1:40');
  await expect(input.getByRole('link', { name: 'Download original' })).toHaveAttribute('href', /download=1/);

  // Edit has the fades; Options the mono/stereo switch, which warns on this mono-in-effect recording.
  await expect(page.locator('section.step').nth(1).getByLabel('Fade in (s)')).toBeVisible();
  const stereo = page.getByRole('switch', { name: 'Stereo' });
  await expect(stereo).not.toBeChecked();
  await expect(page.getByText(/stereo gives the same sound in bigger files/)).toBeHidden();
  await page.locator('label.switch').click();
  await expect(stereo).toBeChecked();
  await expect(page.getByText('⚠ The recording is mono (both channels are the same): stereo gives the same sound in bigger files. Choose mono.')).toBeVisible();
  await expect(page.getByLabel('Target loudness (LUFS)')).toHaveValue('-16'); // the stereo default
  await page.locator('section.step').nth(2).screenshot({ path: path.join(shots, 'edit-options.png') });
  await page.locator('label.switch').click();
  await expect(stereo).not.toBeChecked();

  // Output: a pill per format, all chosen, none made yet.
  await editor(page, () => window.__editor.addSection(21, 79));
  for (const label of ['FLAC', 'MP3', 'Opus']) {
    await expect(pick(page, label)).toHaveAttribute('aria-pressed', 'true');
    await expect(pill(page, label)).toContainText('will be made');
  }
  await pick(page, 'FLAC').click();
  await expect(pick(page, 'FLAC')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.getByText('Save keeps the edit, makes: MP3, Opus.')).toBeVisible();
  await page.locator('section.step').first().screenshot({ path: path.join(shots, 'edit-input.png') });
  await page.locator('section.step').last().screenshot({ path: path.join(shots, 'edit-output-new.png') });
  // The choice is this recording's own: not remembered, here or for other folders.
  await page.reload();
  await expect(pick(page, 'FLAC')).toHaveAttribute('aria-pressed', 'true', { timeout: 30_000 });
});

test('Save makes the chosen formats, and only what is missing or out of date', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await page.getByLabel('Sermon from').fill('0:21');
  await page.getByLabel('Sermon from').press('Enter');
  await page.getByLabel('Sermon to').fill('1:19');
  await page.getByLabel('Sermon to').press('Enter');
  await pick(page, 'FLAC').click();
  await pick(page, 'Opus').click();
  await page.getByLabel('Fade in (s)').fill('1');
  // Save keeps the edit, then makes the files; it asks nothing when it deletes nothing.
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 60_000 });
  await expect(pill(page, 'MP3')).toHaveClass(/exists/);
  await expect(pill(page, 'MP3')).toContainText('up to date');
  await expect(pill(page, 'FLAC')).toContainText('not made');
  await expect(page.getByText('Saved, and everything chosen is up to date.')).toBeVisible();
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  // The files are downloaded from the folder list, not here.
  await expect(page.locator('section.step').last().getByRole('link', { name: 'Download' })).toHaveCount(0);
  await expect(page.locator('.input-file')).toContainText('2026-10-04_raw.flac'); // moved into original/ by the run
  await page.screenshot({ path: path.join(shots, 'done.png'), fullPage: true });

  const files = fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04')).sort();
  expect(files).toEqual(['2026-10-04.mp3', 'original', 'recording.json']); // the state is kept, the recording is in original/
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04/original'))).toEqual(['2026-10-04_raw.flac']);
  const state = JSON.parse(fs.readFileSync(path.join(DATA, 'root/2026/2026-10-04/recording.json'), 'utf8'));
  expect(Object.keys(state.outputs)).toEqual(['mp3']);
  expect(state.convertedFrom.name).toBe('2026-10-04_raw.wav');

  // A change to the edit makes the MP3 out of date; choosing Opus adds it.
  await page.getByLabel('Fade in (s)').fill('2');
  await expect(pill(page, 'MP3')).toContainText('out of date, will be made again');
  await pick(page, 'Opus').click();
  await expect(page.getByText('Save keeps the edit, makes: MP3, Opus.')).toBeVisible();
  await page.locator('section.step').last().screenshot({ path: path.join(shots, 'edit-output-stale.png') });
  // Back as it was made: up to date again.
  await page.getByLabel('Fade in (s)').fill('1');
  await expect(pill(page, 'MP3')).toContainText('up to date');
  const stateFile = path.join(DATA, 'root/2026/2026-10-04/recording.json');
  await expect.poll(() => JSON.parse(fs.readFileSync(stateFile, 'utf8')).adjusted).toBeNull(); // kept: the same as saved

  // After a reload the recording's files decide what is chosen; the fade and the finished run stay.
  await page.reload();
  await expect(pick(page, 'FLAC')).toHaveAttribute('aria-pressed', 'false');
  await expect(page.getByLabel('Fade in (s)')).toHaveValue('1');
  await expect(pill(page, 'MP3')).toHaveClass(/exists/);
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible();
});

test('a run can be cancelled', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await page.getByLabel('Sermon from').fill('0:01');
  await page.getByLabel('Sermon from').press('Enter');
  await expect(pill(page, 'MP3')).toContainText('out of date, will be made again');
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await page.getByRole('button', { name: 'Cancel', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Cancelled' })).toBeVisible({ timeout: 30_000 });
  const left = fs.readdirSync(path.join(DATA, 'root/2026/2026-10-04')).filter((f) => f.includes('.partial') || f.startsWith('.'));
  expect(left).toEqual([]);
});

test('a format that is not chosen any more is deleted by Save, after asking', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04', { keep: true });
  // The sermon's files decide what is chosen, not the choice in another folder.
  await expect(pick(page, 'MP3')).toHaveAttribute('aria-pressed', 'true');
  await expect(pick(page, 'FLAC')).toHaveAttribute('aria-pressed', 'false');
  await pick(page, 'MP3').click();
  await expect(pill(page, 'MP3')).toContainText('will be deleted');
  await expect(page.getByText('Save deletes: MP3.')).toBeVisible();
  await page.locator('section.step').last().screenshot({ path: path.join(shots, 'edit-output-delete.png') });
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  const dialog = page.getByRole('dialog');
  await expect(dialog).toContainText('their files are deleted');
  await expect(dialog).toContainText('2026-10-04.mp3');
  await page.screenshot({ path: path.join(shots, 'dialog.png') });
  await dialog.getByRole('button', { name: 'Save and delete' }).click();
  await expect(pill(page, 'MP3')).toContainText('not made');
  const dir = path.join(DATA, 'root/2026/2026-10-04');
  expect(fs.existsSync(path.join(dir, '2026-10-04.mp3'))).toBe(false);
  expect(fs.existsSync(path.join(dir, 'original/2026-10-04_raw.flac'))).toBe(true); // the recording is never touched
  expect(JSON.parse(fs.readFileSync(path.join(dir, 'recording.json'), 'utf8')).outputs).toEqual({});
});

test('upload straight into a new date folder, in chunks', async ({ page }) => {
  await page.goto('');
  await page.evaluate(() => localStorage.setItem('uploadChunk', String(64 * 1024))); // several chunks
  await page.locator('input[type=file]').setInputFiles({
    name: 'Gudstjeneste 2026-09-27.flac',
    mimeType: 'audio/flac',
    buffer: fs.readFileSync(path.join(DATA, 'upload.flac')),
  });
  await expect(page.getByLabel('Date')).toHaveValue('2026-09-27');
  await expect(page.getByText('from the name')).toBeVisible();
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-09-27$/);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 });
  const dir = path.join(DATA, 'root/2026/2026-09-27');
  expect(fs.readdirSync(dir).filter((f) => f !== 'recording.json')).toEqual(['2026-09-27_raw.flac']);
  expect(fs.readFileSync(path.join(dir, '2026-09-27_raw.flac')).equals(fs.readFileSync(path.join(DATA, 'upload.flac')))).toBe(true);
  // It is on top of the first page, among the new ones, and opens there.
  await page.locator('a.brand').click();
  const recent = page.locator('section.recent');
  await expect(recent.getByRole('heading', { name: 'Newly imported' })).toBeVisible();
  const row = recent.locator('tr', { hasText: '2026-09-27' });
  await expect(row).toContainText('arrived today');
  await row.getByRole('button', { name: '2026-09-27', exact: true }).click();
  await expect(recent.getByRole('link', { name: 'Open in the editor' })).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'folders-new.png') });
  // The date now has a recording: a second upload to it is refused.
  await page.locator('input[type=file]').setInputFiles({ name: 'again 2026-09-27.flac', mimeType: 'audio/flac', buffer: Buffer.from('x') });
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page.getByText('already has a raw recording')).toBeVisible();
});

test('inbox: a recording lands in its date folder by itself', async ({ page }) => {
  await page.goto('#/');
  await expect(page.getByRole('link', { name: 'Inbox' })).toHaveCount(0);
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(DATA, 'inbox', 'Gudstjeneste 2026-09-20.flac'));
  // The server takes it once it has been quiet for a while; the list is read on load.
  await expect(async () => {
    await page.reload();
    await expect(page.locator('.year').getByRole('button', { name: '2026-09-20', exact: true })).toBeVisible({ timeout: 2_000 });
  }).toPass({ timeout: 60_000 });
  await page.locator('.year').getByRole('button', { name: '2026-09-20', exact: true }).click();
  await page.getByRole('link', { name: 'Open in the editor' }).click();
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 });
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-09-20')).filter((f) => f !== 'recording.json')).toEqual(['2026-09-20_raw.flac']);
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
  await expect(page.locator('button.year-head', { hasText: '2026' })).toBeVisible();
  await page.locator('#church').selectOption('+');
  await page.getByRole('dialog').getByLabel('Name of the new church').fill('Second church');
  await page.getByRole('dialog').getByRole('button', { name: 'Add church' }).click();
  await expect(page.locator('#church')).toHaveValue('second-church');
  await expect(page.getByText('No recordings here yet.')).toBeVisible();
  await page.reload();
  await expect(page.locator('#church')).toHaveValue('second-church');
  await expect(page).toHaveURL(/#\/second-church$/);
  await page.locator('#church').selectOption('first-church');
  await expect(page).toHaveURL(/#\/first-church$/);
  await expect(page.locator('button.year-head', { hasText: '2026' })).toBeVisible();
  // A link names its church: it opens that church, whatever this browser used last.
  await page.goto('#/second-church');
  await expect(page.locator('#church')).toHaveValue('second-church');
  await expect(page.getByText('No recordings here yet.')).toBeVisible();
  await page.goto('#/first-church');
  await expect(page.locator('#church')).toHaveValue('first-church');
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

test('two sermons the same day: upload with a name, then rename', async ({ page }) => {
  await page.goto('');
  await page.screenshot({ path: path.join(shots, 'folders-upload.png') });
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-11-01');
  await page.screenshot({ path: path.join(shots, 'folders-upload-chosen.png') });
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-11-01$/);

  await page.goto('');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-11-01');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page.getByText('give this one a name')).toBeVisible();
  await page.locator('input[aria-label=Name]').fill('Kveld');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-11-01%20Kveld$|#\/folder\/2026\/2026-11-01 Kveld$/);
  await expect(page.getByRole('heading', { level: 1 })).toContainText('2026-11-01 Kveld');

  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 }); // prepared: it can be renamed
  await page.getByRole('button', { name: 'Rename…' }).click();
  await page.locator('input[aria-label=Name]').fill('Aften');
  await page.getByRole('button', { name: 'Rename', exact: true }).click();
  await expect(page.getByRole('heading', { level: 1 })).toContainText('2026-11-01 Aften', { timeout: 10_000 });
  expect(fs.readdirSync(path.join(DATA, 'root/2026/2026-11-01 Aften'))).toContain('2026-11-01 Aften_raw.flac');
});

test('a sermon can be given a name on upload', async ({ page }) => {
  await page.goto('');
  await page.locator('input[type=file]').setInputFiles(path.join(DATA, 'upload.flac'));
  await page.locator('input[name=date]').fill('2026-12-06');
  await page.locator('input[aria-label=Name]').fill('Engangs');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-12-06/);
});

test('settings: an overlay over the page, closed by the cross, Esc or a click outside', async ({ page }) => {
  await page.goto('#/first-church');
  await expect(page.locator('.year').getByRole('button', { name: '2026-10-04', exact: true })).toBeVisible();
  const dialog = page.getByRole('dialog', { name: 'Settings' });
  await page.getByRole('link', { name: 'Settings' }).click();
  await expect(dialog.getByRole('heading', { name: 'Default tags' })).toBeVisible();
  await expect(page).toHaveURL(/#\/first-church$/); // still on the page it was opened over
  await page.screenshot({ path: path.join(shots, 'settings-overlay.png') });
  await dialog.getByRole('button', { name: 'Close settings' }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole('link', { name: 'Settings' }).click();
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await page.getByRole('link', { name: 'Settings' }).click();
  await page.mouse.click(5, 300); // the backdrop
  await expect(dialog).toBeHidden();
  await expect(page.locator('.year').getByRole('button', { name: '2026-10-04', exact: true })).toBeVisible();

  // The address opens it over the recordings; closing goes back to them.
  await page.goto('#/first-church/settings/shares');
  await expect(dialog.getByRole('heading', { name: 'Shares' })).toBeInViewport();
  await dialog.getByRole('button', { name: 'Close settings' }).click();
  await expect(page).toHaveURL(/#\/first-church$/);
  await expect(dialog).toBeHidden();
});

test('addresses: the church comes first; old ones are moved', async ({ page }) => {
  await page.goto('');
  await expect(page).toHaveURL(/#\/first-church$/);
  await page.goto('#/folders/2026');
  await expect(page).toHaveURL(/#\/first-church$/);
  await page.goto('#/folder/2026/2026-10-04');
  await expect(page).toHaveURL(/#\/first-church\/folder\/2026\/2026-10-04$/);
  await expect(page.getByRole('heading', { name: '2026-10-04' })).toBeVisible();
  await page.goto('#/shares');
  await expect(page).toHaveURL(/#\/first-church\/settings\/shares$/);
  await page.goto('#/no-such-church/folder/x');
  await expect(page.getByText('No such church: no-such-church')).toBeVisible();
});

test('header: log out is an icon and asks first', async ({ page }) => {
  await page.goto('');
  let posted = false;
  await page.route('**/logged-out', (r) => { posted = r.request().method() === 'POST'; r.fulfill({ body: 'logged out' }); });
  const out = page.locator('#logout button');
  await out.click();
  await expect(out).toHaveText('Log out?');
  await page.locator('header').screenshot({ path: path.join(shots, 'header-logout.png') });
  await page.locator('main').click(); // elsewhere: closes it
  await expect(page.getByText('Log out?')).toBeHidden();
  expect(posted).toBe(false);
  await out.click();
  await out.click();
  await expect(page.getByText('logged out')).toBeVisible();
  expect(posted).toBe(true);
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

test('settings: a church logo replaces the app logo in the header', async ({ page }) => {
  const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><circle cx="20" cy="20" r="18" fill="#b00020"/></svg>';
  await page.goto('#/settings');
  const brand = page.locator('header .brand img');
  await expect(brand).toHaveAttribute('src', 'icons/logo.svg');
  await page.locator('input[name=logo]').setInputFiles({ name: 'logo.svg', mimeType: 'image/svg+xml', buffer: Buffer.from(svg) });
  await expect(brand).toHaveAttribute('src', /api\/logo\?tenant=.*&v=/);
  await expect(page.locator('.logo-row img')).toBeVisible();
  await page.locator('input[name=logo]').setInputFiles({ name: 'x.png', mimeType: 'image/png', buffer: Buffer.from('not an image') });
  await expect(page.getByText('must be a PNG, JPEG, WebP or SVG')).toBeVisible();
  await page.locator('header').screenshot({ path: path.join(shots, 'header-logo.png') });
  await page.reload(); // kept
  await expect(brand).toHaveAttribute('src', /api\/logo/);
  await page.getByRole('button', { name: 'Remove' }).first().click();
  await expect(brand).toHaveAttribute('src', 'icons/logo.svg');
});

test('header: with one church there is no dropdown, only its name', async ({ page }) => {
  await page.route('**/api/tenants', (r) => r.fulfill({ json: {
    tenants: [{ id: 'first-church', name: 'Norkirken Melhus' }], default: 'first-church', canAdd: false, shares: false } }));
  await page.goto('');
  await expect(page.locator('#church-name')).toHaveText('Norkirken Melhus');
  await expect(page.locator('#church')).toBeHidden();
  await page.locator('header').screenshot({ path: path.join(shots, 'header-one-church.png') });
});

test('header: several churches: a quiet dropdown', async ({ page }) => {
  await page.goto('');
  await expect(page.locator('#church')).toBeVisible();
  await expect(page.locator('#church-name')).toBeHidden();
  await page.locator('#church').hover();
  await page.locator('header').screenshot({ path: path.join(shots, 'header-dropdown.png') });
});

test('shares: make a code in the studio, send a recording with the link', async ({ page, context }) => {
  await page.goto('#/shares'); // the old address leads to the shares in the settings
  await expect(page).toHaveURL(/#\/first-church\/settings\/shares$/);
  await expect(page.getByRole('heading', { name: 'Shares' })).toBeInViewport();
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
  // Holding the code copies it; Share (no share menu in this browser) copies the link.
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  const code = dialog.locator('.share-code');
  await code.hover();
  await page.mouse.down();
  await page.waitForTimeout(600);
  await page.mouse.up();
  await expect(dialog.getByText('Code copied.')).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(await code.textContent());
  await dialog.getByRole('button', { name: 'Share', exact: true }).click();
  await expect(dialog.getByText('Link copied.')).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(link);
  await dialog.getByRole('button', { name: 'OK' }).click();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toContainText('open');
  await page.locator('#shares').screenshot({ path: path.join(shots, 'settings-shares.png') });

  // The church's own logo, if it has one, is shown once the code is in (and not before).
  const svg = '<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40"><circle cx="20" cy="20" r="18" fill="#b00020"/></svg>';
  expect((await page.request.put('api/logo?tenant=first-church', { data: svg, headers: { 'Content-Type': 'image/svg+xml' } })).ok()).toBe(true);
  const entry = await context.newPage();
  await entry.goto('http://localhost:8081/');
  await expect(entry.locator('header .brand img')).toHaveAttribute('src', 'icons/logo.svg');
  await entry.close();

  // The link fills in the code, which then leaves the address bar.
  const up = await context.newPage();
  await up.goto(link);
  await expect(up.getByRole('heading', { name: 'Sound desk' })).toBeVisible();
  expect(up.url()).not.toContain('code');
  await expect(up.locator('header .brand img')).toHaveAttribute('src', /^blob:/);
  await expect.poll(() => up.locator('header .brand img').evaluate((img) => img.naturalWidth)).toBeGreaterThan(0);
  await up.screenshot({ path: path.join(shots, 'share-upload-logo.png') });
  expect((await page.request.delete('api/logo?tenant=first-church')).ok()).toBe(true);
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
  await page.getByRole('dialog', { name: 'Revoke share' }).getByRole('button', { name: 'Revoke' }).click();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toContainText('revoked');
  await up.reload();
  await expect(up.getByRole('alert')).toContainText('withdrawn');

  // An ended share can be cleaned away, one by one or all at once.
  await expect(page.getByRole('button', { name: 'Remove 1 ended' })).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'share-ended.png') });
  await page.locator('.share', { hasText: 'Sound desk' }).getByRole('button', { name: 'Remove', exact: true }).click();
  await page.getByRole('dialog', { name: 'Remove share' }).getByRole('button', { name: 'Remove' }).click();
  await expect(page.locator('.share', { hasText: 'Sound desk' })).toHaveCount(0);
  await expect(page.getByRole('button', { name: /ended/ })).toHaveCount(0);
});

test('a recording being imported shows that, not what is left to do', async ({ page }) => {
  // The server's list, with the first recording shown as being converted.
  await page.route('**/api/recordings*', async (route) => {
    const res = await route.fetch();
    const data = await res.json();
    Object.assign(data.recordings[0], { converting: true, saved: false, outputs: [] });
    await route.fulfill({ response: res, json: data });
  });
  await page.goto('');
  const row = page.locator('.year tr.row').first();
  await expect(row).toContainText('converting');
  await expect(row).not.toContainText('to do');
  await expect(row.locator('.badge')).toHaveCount(1);
});

test('the recording keeps the name it arrived with: in the conversion note and in original/', async ({ page }) => {
  // A 3 s WAV, 8 kHz mono: a recorder's file.
  const n = 8000 * 3;
  const wavFile = Buffer.alloc(44 + n * 2);
  wavFile.write('RIFF', 0); wavFile.writeUInt32LE(36 + n * 2, 4); wavFile.write('WAVEfmt ', 8);
  wavFile.writeUInt32LE(16, 16); wavFile.writeUInt16LE(1, 20); wavFile.writeUInt16LE(1, 22);
  wavFile.writeUInt32LE(8000, 24); wavFile.writeUInt32LE(16000, 28); wavFile.writeUInt16LE(2, 32); wavFile.writeUInt16LE(16, 34);
  wavFile.write('data', 36); wavFile.writeUInt32LE(n * 2, 40);
  for (let i = 0; i < n; i++) wavFile.writeInt16LE(Math.round(8000 * Math.sin(i / 8)), 44 + i * 2);

  await page.goto('');
  await page.locator('input[type=file]').setInputFiles({ name: 'R_20270101-101010.wav', mimeType: 'audio/wav', buffer: wavFile });
  await page.locator('input[name=date]').fill('2027-01-01');
  await page.getByRole('button', { name: 'Upload' }).click();
  await expect(page).toHaveURL(/#\/first-church\/folder\/2027\/2027-01-01$/);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('.input-file')).toContainText('converted from the original WAV (R_20270101-101010.wav', { timeout: 30_000 });

  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 30_000 });
  await page.evaluate(() => window.__editor.addSection(0.2, 2.8));
  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 60_000 });
  const folder = path.join(DATA, 'root/2027/2027-01-01');
  expect(fs.readdirSync(path.join(folder, 'original'))).toEqual(['R_20270101-101010.flac']);
  await expect(page.locator('.input-file')).toContainText('R_20270101-101010.flac');
  await expect(page.locator('.input-file').getByRole('link', { name: 'Download original' })).toHaveAttribute('href', /R_20270101-101010\.flac/);
  expect(JSON.parse(fs.readFileSync(path.join(folder, 'recording.json'), 'utf8')).sourceName).toBe('R_20270101-101010.wav');

test('split a section, put in a pause, and choose what Play plays', async ({ page }) => {
  await openEditor(page, '2026/2026-10-04');
  await editor(page, () => window.__editor.addSection(10, 90));
  await editor(page, () => window.__editor.ws.setTime(50));
  await page.getByRole('button', { name: 'Split here' }).click();
  expect(await sections(page)).toEqual([{ start: 10, end: 50 }, { start: 50, end: 90 }]); // two that touch stay two
  await expect(page.getByLabel('Section 1 to')).toHaveValue('0:50.00');
  await expect(page.getByLabel('Section 2 from')).toHaveValue('0:50.00');
  // Splitting where there is no section (or at its edge) does nothing.
  await editor(page, () => window.__editor.ws.setTime(95));
  await page.getByRole('button', { name: 'Split here' }).click();
  expect((await sections(page)).length).toBe(2);

  // A pause after the first section: in the length, on the waveform, and kept. The last section has none.
  await expect(page.getByLabel('Section 2 pause')).toHaveCount(0);
  await page.getByLabel('Section 1 pause').fill('2');
  await expect(page.getByText('Length 1:22.0 (2 sections and 1 pause)')).toBeVisible();
  await page.addStyleTag({ content: 'header { position: static !important; }' });
  await page.locator('section.step').nth(1).screenshot({ path: path.join(shots, 'edit-split-pause.png') });
  await expect.poll(() => editor(page, () => window.__editor.regions.getRegions().filter((r) => r.id.startsWith('pause-')).map((r) => r.start))).toEqual([50]);
  await expect.poll(() => {
    const st = JSON.parse(fs.readFileSync(path.join(DATA, 'root/2026/2026-10-04/recording.json'), 'utf8'));
    const set = st.adjusted || st.saved;
    return set && JSON.stringify(set.sections.map((x) => [x.start, x.end, x.pause || 0]));
  }, { timeout: 15_000 }).toBe(JSON.stringify([[10, 50, 2], [50, 90, 0]]));
  await page.reload();
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 60_000 });
  await expect(page.getByLabel('Section 1 pause')).toHaveValue('2');
  expect((await sections(page)).length).toBe(2);

  // What will be published is the default: playback waits out the pause. The recording plays on.
  const playing = () => editor(page, () => window.__editor.ws.isPlaying());
  await expect(page.getByRole('button', { name: 'The published parts' })).toHaveAttribute('aria-pressed', 'true');
  await editor(page, () => window.__editor.ws.setTime(49.4));
  await page.locator('h1').click();
  await page.keyboard.press(' ');
  await expect.poll(playing, { timeout: 5000 }).toBe(true);
  await expect.poll(playing, { timeout: 5000 }).toBe(false); // the pause
  await expect.poll(playing, { timeout: 5000 }).toBe(true); // and on
  await page.keyboard.press(' ');

  await page.getByRole('button', { name: 'The recording' }).click();
  await expect(page.getByRole('button', { name: 'The recording' })).toHaveAttribute('aria-pressed', 'true');
  expect(await page.evaluate(() => localStorage.getItem('playMode'))).toBe('recording');
  await editor(page, () => window.__editor.ws.setTime(49.4));
  await page.keyboard.press(' ');
  await page.waitForFunction(() => window.__editor.ws.getCurrentTime() > 51, null, { timeout: 8000 });
  expect(await playing()).toBe(true); // no pause
  await page.keyboard.press(' ');
  await page.evaluate(() => localStorage.removeItem('playMode'));
});
