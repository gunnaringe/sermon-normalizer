// The recordings page: years that fold, search, labels, and links to one recording.
import { test, expect } from '@playwright/test';
import path from 'node:path';

const shots = path.join(import.meta.dirname, '..', 'screenshots');
const year = (page, y) => page.locator('button.year-head', { hasText: String(y) });
const rows = (page) => page.locator('tr.row');

test('years fold: the newest is open, the others closed, and the browser remembers', async ({ page }) => {
  await page.goto('');
  await expect(year(page, 2026)).toHaveAttribute('aria-expanded', 'true');
  await expect(year(page, 2025)).toHaveAttribute('aria-expanded', 'false');
  await expect(year(page, 2025)).toContainText('1 recording');
  await expect(page.locator('.year').getByRole('button', { name: '2025-12-21', exact: true })).toHaveCount(0);
  await year(page, 2025).click();
  await expect(page.locator('.year').getByRole('button', { name: '2025-12-21', exact: true })).toBeVisible();
  await year(page, 2026).click();
  await expect(page.locator('.year').getByRole('button', { name: '2026-10-04', exact: true })).toHaveCount(0);
  await page.reload();
  await expect(year(page, 2025)).toHaveAttribute('aria-expanded', 'true');
  await expect(year(page, 2026)).toHaveAttribute('aria-expanded', 'false');
});

test('search finds names, dates and labels in every year; labels are added and removed on a recording', async ({ page }) => {
  await page.goto('');
  const sermon = page.locator('.year tr.row', { hasText: '2026-10-04' });
  await sermon.click();
  const detail = page.locator('tr.detail-row');
  const add = detail.getByLabel('Add a label');
  await add.fill('Easter');
  await add.press('Enter');
  await detail.getByLabel('Add a label').fill('guest   speaker');
  await detail.getByLabel('Add a label').press('Enter');
  await expect(detail.locator('.chip.own')).toHaveText(['Easter×', 'guest speaker×']);
  await expect(sermon.locator('.chip')).toHaveText(['Easter', 'guest speaker']); // also in the row

  // Search: a label, a date (in a year that is folded), several words, nothing.
  const search = page.getByLabel('Search recordings');
  await search.fill('easter');
  await expect(page.locator('.results')).toHaveText('1 recording');
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('2026-10-04');
  await page.screenshot({ path: path.join(shots, 'recordings-search.png') });
  await search.fill('2025-12');
  await expect(rows(page)).toHaveCount(1);
  await expect(rows(page).first()).toContainText('2025-12-21');
  await search.fill('GUEST easter');
  await expect(rows(page)).toHaveCount(1);
  await search.fill('nothing like this');
  await expect(page.getByText('No recording matches.')).toBeVisible();
  await search.fill('');
  await expect(page.locator('.year')).toHaveCount(2);

  // A chip in a row is a search for it.
  await page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('.chip', { hasText: 'guest speaker' }).click();
  await expect(search).toHaveValue('guest speaker');
  await expect(rows(page)).toHaveCount(1);
  await search.fill('');

  // The labels are kept with the recording, and go again with the cross.
  await page.reload();
  await expect(page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('.chip')).toHaveText(['Easter', 'guest speaker']);
  await page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('button.sermon-name').click();
  await page.screenshot({ path: path.join(shots, 'recordings-labels.png') });
  await detail.getByRole('button', { name: 'Remove label Easter' }).click();
  await detail.getByRole('button', { name: 'Remove label guest speaker' }).click();
  await expect(detail.locator('.chip.own')).toHaveCount(0);
  await expect(page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('.chip')).toHaveCount(0);
  // Too long: refused with a message.
  await detail.getByLabel('Add a label').fill('x'.repeat(30));
  await detail.getByLabel('Add a label').press('Enter');
  await expect(detail.locator('.chip.own')).toHaveCount(1);
  await detail.getByRole('button', { name: /Remove label x/ }).click();
});

test('a label is added with the + button, a comma, by leaving the field, or from the suggestions', async ({ page }) => {
  await page.goto('');
  await page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('button.sermon-name').click();
  const detail = page.locator('tr.detail-row');
  const field = detail.getByLabel('Add a label');
  await expect(detail.getByRole('button', { name: 'Add the label' })).toBeHidden(); // nothing typed yet
  await field.fill('Easter');
  await detail.getByRole('button', { name: 'Add the label' }).click();
  await field.fill('guest,');
  await expect(detail.locator('.chip.own')).toHaveText(['Easter×', 'guest×']);
  await field.fill('baptism');
  await page.locator('h1').click(); // leaving the field adds it
  await expect(detail.locator('.chip.own')).toHaveText(['Easter×', 'guest×', 'baptism×']);

  // Another recording: the labels in use are offered, narrowed as you type.
  await page.locator('.year tr.row', { hasText: '2026-10-04' }).locator('button.sermon-name').click();
  await page.locator('.year tr.row', { hasText: '2026-08-30' }).locator('button.sermon-name').click();
  const other = page.locator('tr.detail-row').getByLabel('Add a label');
  await other.focus();
  await expect(page.locator('.suggest li')).toHaveText(['baptism', 'Easter', 'guest']);
  await other.fill('ea');
  await expect(page.locator('.suggest li')).toHaveText(['Easter']);
  await page.screenshot({ path: path.join(shots, 'recordings-label-suggest.png') });
  await page.locator('.suggest li', { hasText: 'Easter' }).click();
  await expect(page.locator('tr.detail-row .chip.own')).toHaveText(['Easter×']);
  await expect(page.locator('.suggest li')).toHaveText(['baptism', 'guest']); // not the one it has

  for (const l of ['Easter']) await page.getByRole('button', { name: 'Remove label ' + l }).click();
});

test('a link opens the recordings page at one recording; Copy link makes it', async ({ page, context }) => {
  await context.grantPermissions(['clipboard-read', 'clipboard-write']);
  await page.goto('#/first-church/recording/2025/2025-12-21');
  await expect(year(page, 2025)).toHaveAttribute('aria-expanded', 'true'); // its year opens
  const row = page.locator('.year tr.row', { hasText: '2025-12-21' });
  await expect(row.locator('xpath=following-sibling::tr[1]').getByRole('link', { name: 'Open in the editor' })).toBeVisible();
  await expect(row).toBeInViewport();
  await expect(page).toHaveURL(/#\/first-church\/recording\/2025\/2025-12-21$/);
  await page.screenshot({ path: path.join(shots, 'recordings-link.png') });

  await page.getByRole('button', { name: 'Copy link' }).click();
  await expect(page.getByRole('button', { name: 'Link copied' })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(new URL('#/first-church/recording/2025/2025-12-21', page.url()).href);

  await page.goto('#/first-church/recording/2030/nothing');
  await expect(page.getByText('That recording was not found.')).toBeVisible();
});

test('labels added quickly, while the server is slow, are all kept', async ({ page }) => {
  // Each label change answers late: a second label is added before the first is saved.
  await page.route('**/api/labels?**', async (route) => {
    if (route.request().method() === 'PUT') await new Promise((r) => setTimeout(r, 400));
    await route.continue();
  });
  await page.goto('');
  const sermon = page.locator('.year tr.row', { hasText: '2025-12-21' });
  await page.locator('button.year-head', { hasText: '2025' }).click();
  await sermon.locator('button.sermon-name').click();
  const detail = page.locator('tr.detail-row');
  const add = detail.getByLabel('Add a label');
  await add.fill('One');
  await add.press('Enter');
  await add.fill('Two');
  await add.press('Enter');
  await expect(detail.locator('.chip.own')).toHaveText(['One×', 'Two×']);
  for (const name of ['One', 'Two']) await detail.getByRole('button', { name: `Remove label ${name}` }).click();
  await expect(detail.locator('.chip.own')).toHaveCount(0);
});
