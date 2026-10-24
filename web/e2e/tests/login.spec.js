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

test('with signup, an invite without a login offers Log in and Create an account', async ({ page }) => {
  await page.route('**/api/invite**', (route) => route.fulfill({ status: 401,
    json: { error: 'log in first', login: true, signup: 'https://id.example.org/signup' } }));
  await page.route('**/auth/login**', (route) => route.fulfill({ body: 'login' }));
  await page.goto('invite.html#kirke.abc');
  await expect(page.getByText('You are invited to Sermon Studio.')).toBeVisible();
  const create = page.getByRole('link', { name: 'Create an account' });
  await expect(create).toHaveAttribute('href', 'https://id.example.org/signup');
  await expect(create).toHaveAttribute('target', '_blank');
  await page.screenshot({ path: path.join(shots, 'invite-signup.png') });
  await page.getByRole('button', { name: 'Log in' }).click();
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

// Members in the settings (with the login). The test server has none, so the church list and the
// members' API are answered here, as the studio would with one.
const church = { id: 'first-church', name: 'Norkirken Melhus' };
const tenants = (role) => ({ tenants: [{ ...church, role }], default: church.id, canAdd: false, shares: false, login: true });

test('an admin sees the members, changes a role and makes an invite link', async ({ page }) => {
  const members = [
    { id: 'a1', name: 'Kari Nordmann', email: 'kari@example.org', role: 'admin', joined: '2026-10-24T06:25:00Z', you: true },
    { id: 'm1', name: 'Ola Nordmann', email: 'ola@example.org', role: 'member', joined: '2026-10-25T10:00:00Z', invitedBy: 'Kari Nordmann' },
  ];
  const invites = [{ id: 'i1', role: 'member', createdBy: 'Kari Nordmann', created: '2026-10-25T09:00:00Z', expires: '2026-11-01T09:00:00Z' }];
  const calls = [];
  await page.route('**/api/tenants', (r) => r.fulfill({ json: tenants('admin') }));
  await page.route('**/api/members**', (r) => {
    if (r.request().method() !== 'GET') calls.push([r.request().method(), new URL(r.request().url()).pathname, r.request().postDataJSON()]);
    return r.fulfill({ json: { members, invites } });
  });
  await page.route('**/api/invites**', (r) => {
    calls.push([r.request().method(), new URL(r.request().url()).pathname, r.request().postDataJSON()]);
    return r.fulfill({ status: 201, json: { token: 'first-church.secret',
      invite: { id: 'i2', role: 'admin', createdBy: 'Kari Nordmann', created: '2026-10-25T09:00:00Z', expires: '2026-10-26T09:00:00Z' } } });
  });
  await page.goto('#/first-church/settings/members');
  const section = page.locator('#members');
  await expect(section.getByRole('heading', { name: 'Members' })).toBeVisible();
  await expect(section.getByText('Ola Nordmann')).toBeVisible();
  await expect(section.getByText('(you)')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Default tags' })).toBeVisible(); // an admin's settings
  await section.locator('h2').evaluate((el) => el.scrollIntoView({ block: 'center' }));
  await page.screenshot({ path: path.join(shots, 'settings-members.png') });

  await section.getByRole('combobox', { name: 'Role of Ola Nordmann' }).selectOption('admin');
  await expect.poll(() => calls.length).toBe(1);
  expect(calls[0]).toEqual(['PUT', '/api/members/m1', { role: 'admin' }]);

  await section.locator('select[name=invite-role]').selectOption('admin');
  await section.locator('select[name=invite-days]').selectOption('1');
  await section.getByRole('button', { name: 'Make invite link' }).click();
  const dialog = page.getByRole('dialog', { name: 'Invite link made' });
  await expect(dialog.getByText(/invite\.html#first-church\.secret$/)).toBeVisible();
  await expect(dialog.getByText('as an admin')).toBeVisible();
  await dialog.screenshot({ path: path.join(shots, 'invite-made.png') });
  expect(calls[1]).toEqual(['POST', '/api/invites', { role: 'admin', days: 1 }]);
  await dialog.getByRole('button', { name: 'OK' }).click();

  await section.getByRole('button', { name: 'Revoke' }).click();
  await page.getByRole('dialog', { name: 'Revoke this invite?' }).getByRole('button', { name: 'Revoke' }).click();
  await expect.poll(() => calls.length).toBe(3);
  expect(calls[2].slice(0, 2)).toEqual(['POST', '/api/invites/i1/revoke']);
});

test('a member sees no church settings, and can leave', async ({ page }) => {
  let left = false;
  await page.route('**/api/tenants', (r) => r.fulfill({ json: tenants('member') }));
  await page.route('**/api/members/leave**', (r) => { left = true; return r.fulfill({ json: {} }); });
  await page.goto('#/first-church/settings/members');
  await expect(page.getByText('You are a member of Norkirken Melhus.')).toBeVisible();
  await expect(page.getByRole('heading', { name: 'Trash' })).toBeVisible();
  for (const name of ['Church', 'Default tags']) await expect(page.getByRole('heading', { name, exact: true })).toHaveCount(0);
  await expect(page.locator('#clips')).toHaveCount(0);
  await page.getByRole('button', { name: 'Leave this church…' }).click();
  await page.getByRole('dialog', { name: 'Leave Norkirken Melhus?' }).getByRole('button', { name: 'Leave' }).click();
  await expect.poll(() => left).toBe(true);
});

// With church hosts (--church-host), the login host's start is the church picker, and a church
// is shown at its own address. The church list is answered here, as the studio would with them.
test('the church picker lists the churches, and goes straight to the only one', async ({ page }) => {
  const two = { tenants: [{ id: 'kirke-a', name: 'Kirke A', url: 'http://kirke-a.localhost:8080/' },
    { id: 'kirke-b', name: 'Kirke B', url: 'http://kirke-b.localhost:8080/' }], default: 'kirke-a', login: true };
  await page.route('**/api/tenants', (r) => r.fulfill({ json: two }));
  await page.goto('churches.html');
  await expect(page.getByRole('heading', { name: 'Your churches' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Kirke B' })).toHaveAttribute('href', 'http://kirke-b.localhost:8080/#/kirke-b');
  await page.screenshot({ path: path.join(shots, 'churches.png') });

  await page.route('**/api/tenants', (r) => r.fulfill({ json: { ...two, tenants: two.tenants.slice(0, 1) } }));
  await page.route('http://kirke-a.localhost:8080/**', (r) => r.fulfill({ body: 'kirke a' }));
  await page.goto('churches.html');
  await expect(page).toHaveURL('http://kirke-a.localhost:8080/#/kirke-a');
});

test('a church that has its own address is shown there', async ({ page }) => {
  await page.route('**/api/tenants', (r) => r.fulfill({ json: { tenants: [
    { id: 'first-church', name: 'Norkirken Melhus', role: 'admin', url: 'http://first-church.localhost:8080/' }],
  default: 'first-church', canAdd: false, shares: false, login: true } }));
  await page.route('http://first-church.localhost:8080/**', (r) => r.fulfill({ body: 'its own address' }));
  await page.goto('#/first-church/settings');
  await expect(page).toHaveURL('http://first-church.localhost:8080/#/first-church/settings');
});
