// Deleting a recording moves it to the trash, from where it can be restored or removed for good.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');

// openEditor opens a recording's edit page and waits until it is there, and the recording is prepared (until
// then it cannot be deleted).
async function openEditor(page, folder, { prepared = true } = {}) {
  await page.goto('#/folder/' + folder);
  await expect(page.getByRole('heading', { name: 'Delete' })).toBeVisible();
  if (prepared) await expect(page.locator('#waveform')).toBeVisible({ timeout: 60_000 });
}

test('delete a recording on its edit page: confirm, the trash in settings, restore, remove for good', async ({ page }) => {
  // Its own recording, so the others stay as they are.
  const dir = path.join(DATA, 'root', '2027', '2027-03-07');
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2027-03-07_raw.flac'));

  await openEditor(page, '2027/2027-03-07');
  // Without files made, only the recording can be deleted, not its original.
  await expect(page.getByRole('button', { name: 'Delete original…' })).toHaveCount(0);
  await page.locator('section.step').last().screenshot({ path: path.join(shots, 'edit-delete.png') });
  await page.getByRole('button', { name: 'Delete recording…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete 2027-03-07?' });
  await expect(dialog).toContainText('removed for good after 30 days');
  await page.screenshot({ path: path.join(shots, 'delete-confirm.png') });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  expect(fs.existsSync(dir)).toBe(true); // asking is not deleting

  await page.getByRole('button', { name: 'Delete recording…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Recordings' })).toBeVisible(); // back on the recordings page
  await expect(page.locator('tr.row', { hasText: '2027-03-07' })).toHaveCount(0);
  expect(fs.existsSync(dir)).toBe(false);

  await page.goto('#/settings');
  const item = page.locator('.trash-item', { hasText: '2027-03-07' });
  await expect(item).toContainText('30 days left');
  await page.locator('#trash').screenshot({ path: path.join(shots, 'settings-trash.png') });

  // Restore: back among the recordings, with its file.
  await item.getByRole('button', { name: 'Restore' }).click();
  await expect(page.getByText('2027-03-07 is back among the recordings.')).toBeVisible();
  await expect(page.locator('.trash-item', { hasText: '2027-03-07' })).toHaveCount(0);
  expect(fs.existsSync(path.join(dir, '2027-03-07_raw.flac'))).toBe(true);

  // Delete again, and remove it for good (after asking).
  await openEditor(page, '2027/2027-03-07');
  await page.getByRole('button', { name: 'Delete recording…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Recordings' })).toBeVisible();
  await expect.poll(() => fs.existsSync(dir)).toBe(false);
  await page.goto('#/settings');
  await page.locator('.trash-item', { hasText: '2027-03-07' }).getByRole('button', { name: 'Delete now' }).click();
  await page.getByRole('dialog', { name: 'Delete 2027-03-07 for good?' }).getByRole('button', { name: 'Delete now' }).click();
  await expect(page.getByText('The trash is empty.')).toBeVisible();
  expect(fs.readdirSync(path.join(DATA, 'root', '.trash'))).toEqual([]);
});

// The recordings of these tests go, also when one fails, so the other tests see the recordings they expect.
test.afterEach(() => {
  for (const d of ['2027-03-07', '2027-03-14']) fs.rmSync(path.join(DATA, 'root', '2027', d), { recursive: true, force: true });
  const year = path.join(DATA, 'root', '2027');
  if (fs.existsSync(year) && fs.readdirSync(year).length === 0) fs.rmdirSync(year);
});

test('delete the original: the files stay, the sermon can no longer be edited', async ({ page, request }) => {
  const folder = '2027/2027-03-14';
  const dir = path.join(DATA, 'root', folder);
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2027-03-14_raw.flac'));
  const q = '?path=' + encodeURIComponent(folder);
  const settings = { sections: [{ start: 3, end: 25 }], cuts: [], fadeIn: 0, fadeOut: 0 };
  expect((await request.put('api/sermon' + q, { data: settings })).ok()).toBe(true);
  expect((await request.post('api/sermon/save' + q)).ok()).toBe(true);
  const run = await request.post('api/run' + q, {
    data: { ...settings, flac: false, mp3: true, opus: false, stereo: false, noiseReduction: 0, targetLufs: -16, truePeak: -1.5, mp3Bitrate: '128k', opusBitrate: '64k', force: false },
  });
  expect(run.ok(), await run.text()).toBe(true);
  const { id } = await run.json();
  await expect.poll(async () => (await (await request.get('api/jobs/' + id)).json()).state, { timeout: 90_000 }).toBe('done');
  const original = path.join(dir, 'original', '2027-03-14_raw.flac');
  expect(fs.existsSync(original)).toBe(true);

  await openEditor(page, folder);
  await expect(page.locator('#waveform')).toBeVisible({ timeout: 60_000 });
  // Next to Download original, and in Delete at the end.
  await expect(page.locator('section.step').first().getByRole('button', { name: 'Delete original…' })).toBeVisible();
  await page.locator('section.step').last().screenshot({ path: path.join(shots, 'edit-delete-original.png') });
  await page.locator('section.step').last().getByRole('button', { name: 'Delete original…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete the original of 2027-03-14?' });
  await expect(dialog).toContainText('deleted for good, not moved to the trash');
  await expect(dialog).toContainText('can no longer be edited or regenerated');
  await dialog.screenshot({ path: path.join(shots, 'delete-original-confirm.png') });
  await dialog.getByRole('button', { name: 'Delete original' }).click();

  // The page says so instead of the waveform; the file made is still there.
  await expect(page.locator('section.step').first()).toContainText('The original recording was deleted');
  await expect(page.locator('#waveform')).toHaveCount(0);
  await expect(page.locator('section.step > h2')).toHaveText(['Input file', 'Output', 'Delete']); // nothing to edit
  await expect(page.locator('.save-bar')).toHaveCount(0);
  await expect(page.getByRole('button', { name: 'Delete original…' })).toHaveCount(0);
  await page.screenshot({ path: path.join(shots, 'edit-original-deleted.png') });
  expect(fs.existsSync(original)).toBe(false);
  expect(fs.existsSync(path.join(dir, '2027-03-14.mp3'))).toBe(true);

  // In the recordings, it says so, the file made downloads, and the others cannot be made.
  await page.goto('#/first-church/recording/' + folder);
  const detail = page.locator('tr.detail-row');
  await expect(detail).toContainText('Original deleted');
  await detail.locator('summary[aria-label=Download]').click();
  await expect(detail.getByRole('link', { name: 'Download MP3' })).toBeVisible();
  await expect(detail.locator('button[name=make-flac]')).toBeDisabled();
  await expect(detail.getByRole('button', { name: /^Delete/ })).toHaveCount(0); // that is on the edit page

  // Clean up: into the trash and out of it.
  await openEditor(page, folder, { prepared: false });
  await page.getByRole('button', { name: 'Delete recording…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Recordings' })).toBeVisible();
  for (const d of fs.readdirSync(path.join(DATA, 'root', '.trash'))) fs.rmSync(path.join(DATA, 'root', '.trash', d), { recursive: true });
});
