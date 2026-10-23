// The church's intro and outro clips, in the settings: upload, defaults, archive, delete with the warning.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');
const church = () => JSON.parse(fs.readFileSync(path.join(DATA, 'churches', 'first-church', 'church.json'), 'utf8'));

// wav is a mono 16-bit WAV of a tone (amp 0 = silence), seconds long.
function wav(seconds, amp = 0.3, rate = 8000) {
  const n = Math.round(seconds * rate);
  const b = Buffer.alloc(44 + 2 * n);
  b.write('RIFF', 0); b.writeUInt32LE(36 + 2 * n, 4); b.write('WAVE', 8);
  b.write('fmt ', 12); b.writeUInt32LE(16, 16); b.writeUInt16LE(1, 20); b.writeUInt16LE(1, 22);
  b.writeUInt32LE(rate, 24); b.writeUInt32LE(2 * rate, 28); b.writeUInt16LE(2, 32); b.writeUInt16LE(16, 34);
  b.write('data', 36); b.writeUInt32LE(2 * n, 40);
  for (let i = 0; i < n; i++) b.writeInt16LE(Math.round(amp * 32767 * Math.sin(2 * Math.PI * 440 * i / rate)), 44 + 2 * i);
  return b;
}

test('settings: intro and outro clips, the defaults, archive and delete', async ({ page }) => {
  await page.goto('#/settings');
  const box = page.locator('section#clips');
  await expect(box.getByRole('heading', { name: 'Intro and outro' })).toBeVisible();
  await expect(box).toContainText('No clips yet.');
  const upload = (name, buffer) => box.locator('input[name=clip]').setInputFiles({ name, mimeType: 'audio/wav', buffer });
  const clip = (name) => box.locator('.clip[data-clip]', { hasText: name });

  await upload('Jingle.wav', wav(4));
  await expect(clip('Jingle')).toContainText('0:04');
  await upload('Takk for at du lyttet.wav', wav(2));
  await expect(clip('Takk for at du lyttet')).toContainText('not used');
  await upload('Stille.wav', wav(2, 0));
  await expect(box.locator('.clip.pending', { hasText: 'Stille' })).toContainText('the clip is silent');
  await box.locator('.clip.pending', { hasText: 'Stille' }).getByRole('button', { name: 'Remove' }).click();
  await expect(box.locator('.clip.pending')).toHaveCount(0);

  await box.locator('select[name=default-intro]').selectOption({ label: 'Jingle' });
  await box.locator('select[name=default-outro]').selectOption({ label: 'Takk for at du lyttet' });
  await box.locator('input[name=clip-pause]').fill('1');
  await box.getByRole('button', { name: 'Save' }).click();
  await expect(box.getByText('Saved.')).toBeVisible();
  const ids = Object.fromEntries(church().clips.clips.map((c) => [c.name, c.id]));
  expect(church().clips).toMatchObject({ intro: ids.Jingle, outro: ids['Takk for at du lyttet'], pause: 1 });
  await expect(clip('Jingle')).toContainText('default intro');

  // A sermon that chose the outro; the default intro cannot be archived.
  const dir = path.join(DATA, 'root', '2027', '2027-05-02');
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2027-05-02_raw.flac'));
  fs.writeFileSync(path.join(dir, 'recording.json'), JSON.stringify({ saved: { sections: [{ start: 1, end: 4 }], cuts: [], fadeIn: 0, fadeOut: 0, outro: ids['Takk for at du lyttet'] } }));
  await page.reload();
  await expect(clip('Takk for at du lyttet')).toContainText('used by 1 sermon');
  await clip('Jingle').getByRole('button', { name: 'Archive' }).click();
  const alert = page.getByRole('dialog', { name: 'Something went wrong' });
  await expect(alert).toContainText('this clip is the default intro: choose another default intro (or none) first');
  await alert.getByRole('button', { name: 'OK' }).click();
  await box.screenshot({ path: path.join(shots, 'settings-clips.png') });

  await box.locator('select[name=default-intro]').selectOption({ label: 'None' });
  await box.locator('select[name=default-outro]').selectOption({ label: 'None' });
  await box.getByRole('button', { name: 'Save' }).click();
  await clip('Takk for at du lyttet').getByRole('button', { name: 'Archive' }).click();
  await box.locator('details.archived summary').click();
  await expect(box.locator('details.archived')).toContainText('Archived (1)');
  await expect(box.locator('details.archived .clip')).toContainText('used by 1 sermon');
  await expect(box.locator('select[name=default-outro] option')).toHaveText(['None', 'Jingle']); // archived: not offered
  await box.locator('details.archived').getByRole('button', { name: 'Delete…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete Takk for at du lyttet?' });
  await expect(dialog).toContainText("Used by 1 sermon. Files already made stay as they are, but this sermon can't be regenerated.");
  await page.screenshot({ path: path.join(shots, 'settings-clip-delete.png') });
  await dialog.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(clip('Takk for at du lyttet')).toHaveCount(0);
  expect(JSON.parse(fs.readFileSync(path.join(dir, 'recording.json'), 'utf8')).saved.outro).toBe(ids['Takk for at du lyttet']); // the sermon is kept

  await clip('Jingle').getByRole('button', { name: 'Delete…' }).click();
  const unused = page.getByRole('dialog', { name: 'Delete Jingle?' });
  await expect(unused).toContainText('No sermon uses it.');
  await unused.getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(box).toContainText('No clips yet.');
  expect(fs.readdirSync(path.join(DATA, 'churches', 'first-church', 'clips'))).toEqual([]);
  fs.rmSync(dir, { recursive: true });
});

test('editor: the intro and outro of a sermon, its own clip, and a deleted one', async ({ page, request }) => {
  // A church clip as the default intro.
  expect((await request.post('api/clips?name=Jingle.wav', { data: wav(3) })).ok()).toBe(true);
  await expect.poll(async () => (await (await request.get('api/clips')).json()).clips.length).toBe(1);
  const jingle = (await (await request.get('api/clips')).json()).clips[0].id;
  expect((await request.put('api/clips/defaults', { data: { intro: jingle, outro: '', pause: 0.5 } })).ok()).toBe(true);

  const folder = '2027/2027-06-06';
  const dir = path.join(DATA, 'root', folder);
  const draft = () => (fs.existsSync(path.join(dir, 'draft.json')) ? JSON.parse(fs.readFileSync(path.join(dir, 'draft.json'), 'utf8')).sermon : {});
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2027-06-06_raw.flac'));
  await page.goto('#/folder/' + folder);
  await page.waitForFunction(() => window.__editor && window.__editor.ws.getDuration() > 0, null, { timeout: 60_000 });
  await page.evaluate(() => { if (!window.__editor.sections.length) window.__editor.addSection(3, 25); });

  const step = page.locator('section[aria-labelledby=step-clips]');
  await expect(step.getByRole('heading', { name: 'Intro and outro' })).toBeVisible();
  const intro = step.locator('select[name=intro]'), outro = step.locator('select[name=outro]');
  await expect(intro).toHaveValue('default');
  await expect(intro.locator('option:checked')).toHaveText('Church default: Jingle');
  await expect(outro.locator('option:checked')).toHaveText('Church default: none');
  await expect(step).toContainText('With the intro and outro:');

  // The sermon's own outro.
  const chooser = page.waitForEvent('filechooser');
  await outro.selectOption({ label: 'Upload for this sermon only…' });
  await (await chooser).setFiles({ name: 'Takk.wav', mimeType: 'audio/wav', buffer: wav(2) });
  await expect(outro).toHaveValue('own');
  await expect(outro.locator('option:checked')).toHaveText("This sermon's own: Takk");
  await expect.poll(() => draft().outro).toBe('own');
  await step.screenshot({ path: path.join(shots, 'editor-intro-outro.png') });

  await page.getByRole('button', { name: 'Save', exact: true }).click();
  await expect(page.getByRole('heading', { name: 'Done' })).toBeVisible({ timeout: 90_000 });
  for (const f of ['2027-06-06.mp3', '2027-06-06_sermon.mp3', '2027-06-06.flac', '2027-06-06_sermon.flac']) expect(fs.existsSync(path.join(dir, f)), f).toBe(true);
  expect(fs.existsSync(path.join(dir, 'clips', 'outro.flac'))).toBe(true);

  await step.getByRole('button', { name: "Add to the church's clips" }).click();
  await expect(step).toContainText("Takk is among the church's clips now.");
  await expect(step.getByRole('button', { name: "Add to the church's clips" })).toHaveCount(0); // once

  // The intro chosen by name, then deleted: the editor says so, and the run is refused until another is chosen.
  await intro.selectOption({ label: 'Jingle' });
  await expect.poll(() => draft().intro).toBe(jingle);
  expect((await request.put('api/clips/defaults', { data: { intro: '', outro: '' } })).ok()).toBe(true);
  expect((await request.post('api/clips/' + jingle + '/delete')).ok()).toBe(true);
  await page.reload();
  await expect(intro.locator('option:checked')).toHaveText('A deleted clip');
  await expect(step).toContainText('The intro this sermon used has been deleted. Choose another intro, or none.');
  await expect(page.locator('.save-bar')).toContainText('the intro this sermon used has been deleted');
  await expect(page.getByRole('button', { name: 'Save', exact: true })).toBeDisabled();
  await step.screenshot({ path: path.join(shots, 'editor-intro-deleted.png') });
  await intro.selectOption({ label: 'None' });
  await expect(page.locator('.save-bar')).not.toContainText('has been deleted');

  for (const c of (await (await request.get('api/clips')).json()).clips) await request.post('api/clips/' + c.id + '/delete');
  fs.rmSync(dir, { recursive: true });
});
