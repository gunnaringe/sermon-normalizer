// The website (site/), served as Cloudflare serves it (serve.js): every page in every language, the language
// selector, the same keys in every language, every internal link and picture, the video, and Log in hidden.
// Saves screenshots of the pages (web/e2e/screenshots/site-*.png) for the PR. Run with scripts/test.sh site.
import { test, expect } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';

const SITE = process.env.SITE || '/site';
const LANGS = ['en', 'nb', 'nn'];
const PAGES = ['/', '/guide.html', '/privacy.html'];
const dicts = Object.fromEntries(LANGS.map((l) => [l, JSON.parse(fs.readFileSync(path.join(SITE, 'lang', l + '.json'), 'utf8'))]));
const shots = path.join(import.meta.dirname, '..', 'screenshots');
const ORIGIN = 'http://127.0.0.1:8090';

// watch fails the test on a script error, a request to another host, a failed request or a CSP violation.
function watch(page) {
  const problems = [];
  page.on('pageerror', (e) => problems.push('error: ' + e.message));
  page.on('console', (m) => { if (m.type() === 'error') problems.push('console: ' + m.text()); });
  page.on('request', (r) => { if (!r.url().startsWith(ORIGIN) && !r.url().startsWith('data:')) problems.push('request to another host: ' + r.url()); });
  page.on('response', (r) => { if (r.status() >= 400) problems.push(`${r.status()}: ${r.url()}`); });
  return problems;
}
const ready = (page) => expect(page.locator('html.ready')).toHaveCount(1);
// eager loads the lazy pictures, so the page is whole.
async function eager(page) {
  await page.evaluate(() => document.querySelectorAll('img[loading=lazy]').forEach((i) => { i.loading = 'eager'; }));
  await page.waitForFunction(() => [...document.images].every((i) => i.complete && i.naturalWidth > 0));
}

test('every language has the same keys, and the pages use only those keys', () => {
  const keys = (l) => Object.keys(dicts[l]).sort();
  for (const l of LANGS) {
    expect(keys(l), `${l}.json`).toEqual(keys('en'));
    for (const [k, v] of Object.entries(dicts[l])) expect(v.trim(), `${l}: ${k}`).not.toBe('');
  }
  const used = new Set();
  for (const file of fs.readdirSync(SITE).filter((f) => f.endsWith('.html'))) {
    const html = fs.readFileSync(path.join(SITE, file), 'utf8');
    for (const m of html.matchAll(/data-t(?:-[a-z-]+)?="([^"]+)"/g)) {
      expect(dicts.en, `${file}: ${m[1]}`).toHaveProperty([m[1]]);
      used.add(m[1]);
    }
  }
  expect(Object.keys(dicts.en).filter((k) => !used.has(k)), 'keys no page uses').toEqual([]);
});

for (const lang of LANGS) {
  for (const p of PAGES) {
    test(`${p} in ${lang}`, async ({ page, request }) => {
      const problems = watch(page);
      await page.goto(p + '?lang=' + lang);
      await ready(page);
      await expect(page.locator('html')).toHaveAttribute('lang', lang);
      await expect(page.locator('#lang')).toHaveValue(lang);
      const h1 = page.locator('h1');
      await expect(h1).toBeVisible();
      expect((await h1.textContent()).trim()).not.toBe('');
      // Every text is filled in, from this language.
      for (const el of await page.locator('body [data-t]:not(noscript *)').all()) {
        const key = await el.getAttribute('data-t');
        expect((await el.innerHTML()).trim(), key).not.toBe('');
      }
      expect(await page.title()).toBe(dicts[lang][await page.locator('title').getAttribute('data-t')]);
      // Log in is hidden, with no address, until site.js turns it on.
      await expect(page.locator('#login')).toBeHidden();
      expect(await page.locator('#login').getAttribute('href')).toBeNull();

      // Every picture is this language's, and loads.
      await eager(page);
      for (const src of await page.locator('img[data-shot]').evaluateAll((imgs) => imgs.map((i) => i.getAttribute('src')))) {
        expect(src).toMatch(new RegExp(`^img/shots/${lang}/[a-z-]+\\.webp$`));
      }
      // Every link: inside the site it resolves; outside it is GitHub or the contact address.
      for (const href of new Set(await page.locator('a[href]').evaluateAll((as) => as.map((a) => a.getAttribute('href'))))) {
        if (/^mailto:/.test(href)) expect(href).toBe('mailto:contact@sermon-studio.app');
        else if (/^https?:/.test(href)) expect(href).toMatch(/^https:\/\/github\.com\/gunnaringe\/sermon-studio(\/|$)/);
        else {
          const url = new URL(href, page.url());
          expect((await request.get(url.pathname)).status(), href).toBe(200);
          if (url.hash && url.pathname === new URL(page.url()).pathname) await expect(page.locator(url.hash), href).toHaveCount(1);
        }
      }
      expect(problems).toEqual([]);
    });
  }
}

test('the language selector switches the page and is remembered', async ({ page }) => {
  const problems = watch(page);
  await page.goto('/');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.en['home.h1']);
  await page.locator('#lang').selectOption('nn');
  await expect(page.locator('h1')).toHaveText(dicts.nn['home.h1']);
  await expect(page.locator('html')).toHaveAttribute('lang', 'nn');
  await expect(page.locator('img[data-shot=editor]').first()).toHaveAttribute('src', 'img/shots/nn/editor.webp');
  await page.reload();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['home.h1']);
  await page.locator('nav a[href="guide.html"]').click();
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nn['guide.h1']);
  // ?lang= is a choice too; the selector then takes it out of the address.
  await page.goto('/privacy.html?lang=nb');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.nb['privacy.h1']);
  await page.locator('#lang').selectOption('en');
  await expect(page).toHaveURL(/\/privacy\.html$/);
  await page.goto('/');
  await ready(page);
  await expect(page.locator('h1')).toHaveText(dicts.en['home.h1']);
  expect(problems).toEqual([]);
});

for (const [locale, lang] of [['nb-NO', 'nb'], ['no', 'nb'], ['nn-NO', 'nn'], ['en-GB', 'en'], ['de-DE', 'en']]) {
  test(`a browser in ${locale} gets ${lang} by itself`, async ({ browser }) => {
    const context = await browser.newContext({ locale });
    const page = await context.newPage();
    await page.goto(ORIGIN + '/');
    await ready(page);
    await expect(page.locator('html')).toHaveAttribute('lang', lang);
    await expect(page.locator('h1')).toHaveText(dicts[lang]['home.h1']);
    await context.close();
  });
}

test('the video has its sources and poster, and they are small', async ({ page, request }) => {
  await page.goto('/?lang=nb');
  await ready(page);
  const video = page.locator('video');
  await expect(video).toHaveAttribute('poster', 'media/demo-poster.webp');
  for (const attr of ['controls', 'muted', 'playsinline']) await expect(video).toHaveAttribute(attr, '');
  const sources = await video.locator('source').evaluateAll((s) => s.map((x) => [x.getAttribute('src'), x.getAttribute('type')]));
  expect(sources).toEqual([['media/demo.webm', 'video/webm'], ['media/demo.mp4', 'video/mp4']]);
  await expect(page.locator('#video .note')).toHaveText(dicts.nb['home.video.note']);
  let total = 0;
  for (const src of ['media/demo.webm', 'media/demo.mp4', 'media/demo-poster.webp']) {
    const r = await request.get(src);
    expect(r.status(), src).toBe(200);
    const size = (await r.body()).length;
    expect(size, src).toBeGreaterThan(10_000);
    total += size;
  }
  expect(total).toBeLessThan(10 * 1024 * 1024);
  // It plays (the browser here has no H.264, so WebM is the one it picks).
  await video.evaluate((v) => { v.preload = 'auto'; v.load(); });
  await expect.poll(() => video.evaluate((v) => v.readyState), { timeout: 15_000 }).toBeGreaterThanOrEqual(1);
  expect(await video.evaluate((v) => Math.round(v.duration))).toBeGreaterThanOrEqual(60);
});

test('an unknown address is the not-found page, and the Worker files are not served', async ({ page, request }) => {
  const r = await page.goto('/no/such/page');
  expect(r.status()).toBe(404);
  await ready(page);
  await expect(page.locator('h1')).not.toHaveText('');
  for (const f of ['/wrangler.jsonc', '/README.md', '/.assetsignore', '/_headers']) expect((await request.get(f)).status(), f).toBe(404);
  expect((await request.get('/guide')).status()).toBe(200);
});

test('screenshots for the PR', async ({ browser }) => {
  fs.mkdirSync(shots, { recursive: true });
  for (const lang of LANGS) {
    const context = await browser.newContext({ viewport: { width: 1280, height: 1000 } });
    const page = await context.newPage();
    await page.goto(`${ORIGIN}/?lang=${lang}`);
    await ready(page);
    await eager(page);
    await page.screenshot({ path: path.join(shots, `site-home-${lang}.png`) });
    if (lang === 'en') {
      await page.screenshot({ path: path.join(shots, 'site-home-full.png'), fullPage: true });
      for (const [p, name] of [['guide.html', 'site-guide'], ['privacy.html', 'site-privacy']]) {
        await page.goto(`${ORIGIN}/${p}`);
        await ready(page);
        await eager(page);
        await page.screenshot({ path: path.join(shots, name + '.png'), fullPage: name === 'site-privacy' });
      }
    }
    await context.close();
  }
  for (const scheme of ['light', 'dark']) {
    const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 1, isMobile: true, hasTouch: true, colorScheme: scheme });
    const page = await phone.newPage();
    await page.goto(`${ORIGIN}/?lang=nb`);
    await ready(page);
    await eager(page);
    await page.screenshot({ path: path.join(shots, `site-phone-${scheme}.png`) });
    await phone.close();
  }
});
