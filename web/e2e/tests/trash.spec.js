// Deleting a recording moves it to the trash, from where it can be restored or removed for good.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const DATA = process.env.DATA;
const shots = path.join(import.meta.dirname, '..', 'screenshots');

test('delete a recording: confirm, the trash in settings, restore, remove for good', async ({ page }) => {
  // Its own recording, so the others stay as they are.
  const dir = path.join(DATA, 'root', '2027', '2027-03-07');
  fs.mkdirSync(dir, { recursive: true });
  fs.copyFileSync(path.join(DATA, 'upload.flac'), path.join(dir, '2027-03-07_raw.flac'));

  await page.goto('');
  const year = page.locator('button.year-head', { hasText: '2027' });
  if ((await year.getAttribute('aria-expanded')) !== 'true') await year.click();
  const row = page.locator('tr.row', { hasText: '2027-03-07' });
  await row.getByRole('button', { name: '2027-03-07', exact: true }).click();
  await page.getByRole('button', { name: 'Delete…' }).click();
  const dialog = page.getByRole('dialog', { name: 'Delete 2027-03-07?' });
  await expect(dialog).toContainText('removed for good after 30 days');
  await page.screenshot({ path: path.join(shots, 'delete-confirm.png') });
  await dialog.getByRole('button', { name: 'Cancel' }).click();
  await expect(row).toBeVisible(); // asking is not deleting
  expect(fs.existsSync(dir)).toBe(true);

  await page.getByRole('button', { name: 'Delete…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
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
  await page.goto('');
  if ((await year.getAttribute('aria-expanded')) !== 'true') await year.click();
  await page.locator('tr.row', { hasText: '2027-03-07' }).getByRole('button', { name: '2027-03-07', exact: true }).click();
  await page.getByRole('button', { name: 'Delete…' }).click();
  await page.getByRole('dialog').getByRole('button', { name: 'Delete', exact: true }).click();
  await expect(page.locator('tr.row', { hasText: '2027-03-07' })).toHaveCount(0);
  await page.goto('#/settings');
  await page.locator('.trash-item', { hasText: '2027-03-07' }).getByRole('button', { name: 'Delete now' }).click();
  await page.getByRole('dialog', { name: 'Delete 2027-03-07 for good?' }).getByRole('button', { name: 'Delete now' }).click();
  await expect(page.getByText('The trash is empty.')).toBeVisible();
  expect(fs.readdirSync(path.join(DATA, 'root', '.trash'))).toEqual([]);
});
