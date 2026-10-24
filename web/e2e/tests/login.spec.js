// The pages of the studio's own login (--oidc-issuer): an invite link, and
// after logging out. The test server has no login, so the invite's API is
// answered here, as the studio would with one.
import { test, expect } from '@playwright/test';
import path from 'node:path';

const shots = path.join(import.meta.dirname, '..', 'screenshots');

test('an invite link says what it is for, and accepting opens the studio', async ({ page }) => {
  let accepted = null;
  await page.route('**/api/invite**', async (route) => {
    const church = { id: 'norkirken-melhus', name: 'Norkirken Melhus' };
    if (route.request().method() === 'POST') {
      accepted = route.request().postDataJSON();
      return route.fulfill({ json: { church } });
    }
    expect(new URL(route.request().url()).searchParams.get('token')).toBe('norkirken-melhus.secret');
    return route.fulfill({ json: { church, role: 'admin', member: false } });
  });
  await page.goto('invite.html#norkirken-melhus.secret');
  await expect(page.getByRole('heading', { name: 'Invitation' })).toBeVisible();
  await expect(page.getByText('You are invited to Norkirken Melhus as an admin.')).toBeVisible();
  await page.screenshot({ path: path.join(shots, 'invite.png') });
  await page.getByRole('button', { name: 'Accept' }).click();
  await expect(page).toHaveURL(/#\/norkirken-melhus/);
  expect(accepted).toEqual({ token: 'norkirken-melhus.secret' });
});

test('an invite that is not valid says so, in the chosen language', async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem('lang', 'nn'));
  await page.route('**/api/invite**', (route) => route.fulfill({ status: 404,
    json: { error: 'this invite link is not valid: it may have been used, or have expired. Ask for a new one' } }));
  await page.goto('invite.html#kirke.used');
  await expect(page.getByText('denne invitasjonslenkja er ikkje gyldig')).toBeVisible();
});

test('without a session, the invite goes to the login and back', async ({ page }) => {
  await page.route('**/api/invite**', (route) => route.fulfill({ status: 401, json: { error: 'log in first', login: true } }));
  await page.route('**/auth/login**', (route) => route.fulfill({ body: 'login' }));
  await page.goto('invite.html#kirke.abc');
  await expect(page).toHaveURL(/auth\/login\?next=%2Finvite\.html%23kirke\.abc$/);
});

test('the logged-out page offers to log in again', async ({ page }) => {
  await page.goto('loggedout.html');
  await expect(page.getByRole('heading', { name: 'You are logged out' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Log in' })).toHaveAttribute('href', 'auth/login');
  await page.screenshot({ path: path.join(shots, 'logged-out.png') });
  await page.goto('loggedout.html?failed');
  await expect(page.getByText('The login did not work.')).toBeVisible();
});
