// Translations. The English text is the key: t('Save') is "Save" in English and
// whatever locales.js has for it in the other languages (an English text with
// no entry is shown as it is). Parameters are written {name}. TestLocales keeps
// the dictionaries complete.
import { nb, nn } from './locales.js';

export const LANGS = [['en', 'English'], ['nb', 'Norsk bokmål'], ['nn', 'Norsk nynorsk']];
const DICTS = { en: {}, nb, nn };

// The language is the one chosen in this browser, else the first of the
// browser's own that we have (Norwegian "no" is bokmål).
function detect() {
  const stored = localStorage.getItem('lang');
  if (stored in DICTS) return stored;
  for (const tag of navigator.languages || [navigator.language || 'en']) {
    const base = tag.toLowerCase().split('-')[0];
    if (base === 'nn') return 'nn';
    if (base === 'nb' || base === 'no') return 'nb';
    if (base === 'en') return 'en';
  }
  return 'en';
}

export const lang = detect();
const dict = DICTS[lang];
document.documentElement.lang = lang;

export function setLang(code) {
  localStorage.setItem('lang', code);
}

export function t(text, params) {
  const out = dict[text] ?? text;
  return params ? out.replace(/\{(\w+)\}/g, (_, k) => params[k] ?? '') : out;
}

// tr translates a message from the server (always English). Those with a value
// in them (a size, a name) are found by their dictionary entry with {name}
// where the value is: "the file is larger than the {size} this code allows".
const patterns = Object.entries(dict).filter(([k]) => k.includes('{') && k.replace(/\{\w+\}/g, '').length >= 12).map(([k, v]) => {
  const names = [];
  const source = k.split(/\{(\w+)\}/).map((part, i) => {
    if (i % 2 === 0) return part.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    names.push(part);
    return '(.+?)';
  }).join('');
  return { re: new RegExp('^' + source + '$', 's'), names, text: v };
});
export function tr(message) {
  if (dict[message] !== undefined) return dict[message];
  for (const p of patterns) {
    const m = message.match(p.re);
    if (m) return p.text.replace(/\{(\w+)\}/g, (_, k) => tr(m[p.names.indexOf(k) + 1] ?? ''));
  }
  return message;
}

// tn picks between two texts by the number n, which is also {n} in them.
export const tn = (n, one, other, params) => t(n === 1 ? one : other, { n, ...params });

// The theme: 'auto' follows the device; saved in this browser.
export const getTheme = () => localStorage.getItem('theme') || 'auto';
export function setTheme(theme) {
  if (theme === 'light' || theme === 'dark') {
    localStorage.setItem('theme', theme);
    document.documentElement.dataset.theme = theme;
  } else {
    localStorage.removeItem('theme');
    delete document.documentElement.dataset.theme;
  }
}

// translatePage translates the fixed text of the HTML page: data-i18n (its
// text), data-i18n-title and data-i18n-aria-label (those attributes).
export function translatePage(root = document) {
  for (const el of root.querySelectorAll('[data-i18n]')) el.textContent = t(el.dataset.i18n);
  for (const attr of ['title', 'aria-label']) {
    for (const el of root.querySelectorAll(`[data-i18n-${attr}]`)) el.setAttribute(attr, t(el.getAttribute(`data-i18n-${attr}`)));
  }
}
translatePage();
