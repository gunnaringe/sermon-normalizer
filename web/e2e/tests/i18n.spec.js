// Languages (English, bokmål, nynorsk) and themes (automatic, light, dark).
import { test, expect } from '@playwright/test';
import path from 'node:path';

const shots = path.join(import.meta.dirname, '..', 'screenshots');
const REPO = 'https://github.com/gunnaringe/sermon-studio';
const bg = (page) => page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--bg').trim());

test('the language is chosen in Settings, kept, and changes the whole page', async ({ page }) => {
  await page.goto('');
  await expect(page.getByRole('heading', { name: 'Recordings' })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'en');
  await expect(page.getByRole('link', { name: 'Source code on GitHub' })).toHaveAttribute('href', REPO);

  await page.getByRole('link', { name: 'Settings' }).click();
  await expect(page.locator('select[name=language]')).toHaveValue('en');
  await page.locator('select[name=language]').selectOption('nb');
  await expect(page.getByRole('heading', { name: 'Utseende' })).toBeVisible(); // Settings opens again, in bokmål
  await expect(page.locator('html')).toHaveAttribute('lang', 'nb');
  await expect(page.getByRole('heading', { name: 'Innstillinger' })).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'language-nb-settings.png') });
  await page.getByRole('button', { name: 'Lukk innstillinger' }).click();
  await expect(page.getByRole('heading', { name: 'Opptak' })).toBeVisible();
  await expect(page.getByPlaceholder('Søk i navn, dato eller merkelapp')).toBeVisible();
  await expect(page.getByRole('link', { name: 'Kildekode på GitHub' })).toHaveAttribute('href', REPO);
  await page.screenshot({ path: path.join(shots, 'language-nb.png') });

  // The choice is kept, and nynorsk differs from bokmål.
  await page.reload();
  await expect(page.getByRole('heading', { name: 'Opptak' })).toBeVisible();
  await page.getByRole('link', { name: 'Innstillinger' }).click();
  await page.locator('select[name=language]').selectOption('nn');
  await expect(page.getByRole('heading', { name: 'Innstillingar' })).toBeVisible();
  await expect(page.locator('html')).toHaveAttribute('lang', 'nn');
  await page.getByRole('button', { name: 'Lukk innstillingar' }).click();
  await expect(page.getByRole('link', { name: 'Kjeldekode på GitHub' })).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'language-nn.png') });

  // The editor too: a recording in the new language.
  await page.locator('tr.row', { hasText: '2026-10-04' }).click();
  await expect(page.getByRole('link', { name: 'Opne i redigeringa' })).toBeVisible();
  await page.getByRole('link', { name: 'Opne i redigeringa' }).click();
  await expect(page.getByRole('heading', { name: 'Kjeldefil' }).or(page.getByRole('heading', { name: 'Opptaksfil' }))).toBeVisible();
  await expect(page.getByRole('button', { name: 'Lagre', exact: true })).toBeVisible();
});

test.describe('the browser language', () => {
  test.use({ locale: 'nn-NO' });
  test('nynorsk is used when the browser asks for it', async ({ page }) => {
    await page.goto('');
    await expect(page.getByRole('heading', { name: 'Opptak' })).toBeVisible();
    await expect(page.locator('html')).toHaveAttribute('lang', 'nn');
  });
});

test.describe('the browser language: Norwegian', () => {
  test.use({ locale: 'no' });
  test('"no" is bokmål', async ({ page }) => {
    await page.goto('');
    await expect(page.locator('html')).toHaveAttribute('lang', 'nb');
  });
});

test.describe('another language falls back to English', () => {
  test.use({ locale: 'de-DE' });
  test('English', async ({ page }) => {
    await page.goto('');
    await expect(page.getByRole('heading', { name: 'Recordings' })).toBeVisible();
  });
});

test('the theme is automatic, or light, or dark', async ({ page }) => {
  await page.emulateMedia({ colorScheme: 'light' });
  await page.goto('');
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
  expect(await bg(page)).toBe('#f4f5fb');
  await page.emulateMedia({ colorScheme: 'dark' }); // automatic follows the device
  expect(await bg(page)).toBe('#0d0e16');

  await page.getByRole('link', { name: 'Settings' }).click();
  await page.locator('select[name=theme]').selectOption('light'); // chosen: the device does not matter
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');
  expect(await bg(page)).toBe('#f4f5fb');
  await page.emulateMedia({ colorScheme: 'light' });
  await page.locator('select[name=theme]').selectOption('dark');
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  expect(await bg(page)).toBe('#0d0e16');
  await page.screenshot({ path: path.join(shots, 'theme-dark.png') });

  // Kept, and there from the first paint.
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  await page.getByRole('link', { name: 'Settings' }).click();
  await expect(page.locator('select[name=theme]')).toHaveValue('dark');
  await page.locator('select[name=theme]').selectOption('auto');
  await expect(page.locator('html')).not.toHaveAttribute('data-theme', /./);
  expect(await bg(page)).toBe('#f4f5fb'); // the device is light again
});

test.describe('the public upload page', () => {
  test.use({ locale: 'nb-NO' });
  test('is translated, with its own language choice and the link to the repo', async ({ page }) => {
    await page.goto('http://localhost:8081/');
    await expect(page.getByRole('heading', { name: 'Skriv inn koden' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Fortsett' })).toBeVisible();
    await expect(page.getByRole('link', { name: 'Kildekode på GitHub' })).toHaveAttribute('href', REPO);
    await page.getByLabel('Kode').fill('AAAA-BBBB-CCCC-DDDD');
    await page.getByRole('button', { name: 'Fortsett' }).click();
    await expect(page.getByRole('alert')).toContainText('ukjent kode'); // the server's message, translated
    await page.screenshot({ path: path.join(shots, 'share-upload-nb.png') });
    await page.getByLabel('Språk').selectOption('en');
    await expect(page.getByRole('heading', { name: 'Enter your code' })).toBeVisible();
  });
});
