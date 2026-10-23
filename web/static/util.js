// Shared helpers: API calls, times, and building DOM without innerHTML.
import { t, tr } from './i18n.js';

// The church the UI is working with; every request to the server names it.
let tenant = localStorage.getItem('tenant') || '';
export const currentTenant = () => tenant;
export function setTenant(id) {
  tenant = id;
  localStorage.setItem('tenant', id);
}

// Whether the scroll wheel zooms the waveform only with ctrl/cmd held. A
// choice of this browser, not of the church: kept in localStorage.
export const wheelZoomNeedsKey = () => localStorage.getItem('wheelZoom') === 'key';
export function setWheelZoomNeedsKey(on) {
  if (on) localStorage.setItem('wheelZoom', 'key');
  else localStorage.removeItem('wheelZoom');
}

// href is the address of a page of the current church: '#/<church>/<section>/<path>'.
export function href(section = '', path = '') {
  return '#/' + tenant + (section ? '/' + section : '') + (path ? '/' + path : '');
}

// forTenant adds the church to a URL that is not fetched through api():
// audio, downloads, event streams.
export function forTenant(url) {
  return tenant ? url + (url.includes('?') ? '&' : '?') + 'tenant=' + encodeURIComponent(tenant) : url;
}

// api calls the server. URLs are relative ("api/..."), so the UI also
// works behind a proxy under a sub-path.
export async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url.startsWith('api/tenants') || url === 'api/session' ? url : forTenant(url), opts);
  const data = await resp.json().catch(() => ({}));
  if (resp.status === 401 && data.login) {
    toLogin();
    return new Promise(() => {}); // the page is leaving
  }
  if (!resp.ok) throw new Error(tr(data.error || `${resp.status} ${resp.statusText}`));
  return data;
}

// toLogin goes to the studio's login (the session has ended, or there was
// none), which comes back to this page.
export function toLogin() {
  location.href = 'auth/login?next=' + encodeURIComponent(location.pathname + location.search + location.hash);
}

export const q = (path) => encodeURIComponent(path);

// INPUT_TYPES are the types a recording may come as: normalizer.InputTypes
// (TestInputTypesInJS keeps them the same). WAV and AIFF become FLAC as they
// arrive, a video becomes its sound (.mka); the others are kept as they are.
export const INPUT_TYPES = ['.flac', '.mka', '.wav', '.aiff', '.aif', '.mp3', '.m4a', '.aac', '.ogg', '.opus', '.wma', '.caf',
  '.mp4', '.mov', '.mkv', '.webm'];
export const isInputType = (name) => INPUT_TYPES.includes(name.slice(name.lastIndexOf('.')).toLowerCase());
export const isVideo = (name) => /\.(mp4|mov|mkv|webm)$/i.test(name);
export const converts = (name) => isVideo(name) || /\.(wav|aiff?)$/i.test(name);

// parseTime parses 1:12:05, 41:20, 2480 or 41:20.5 as seconds.
export function parseTime(s) {
  s = s.trim();
  const parts = s.split(':');
  if (s === '' || parts.length > 3) return NaN;
  let total = 0;
  for (let i = 0; i < parts.length; i++) {
    const last = i === parts.length - 1;
    if (!/^\d+(\.\d+)?$/.test(parts[i])) return NaN;
    const v = Number(parts[i]);
    if ((!last && !Number.isInteger(v)) || (i > 0 && v >= 60)) return NaN;
    total = total * 60 + v;
  }
  return total;
}

// formatTime shows seconds as h:mm:ss.cc (m:ss.cc under an hour).
export function formatTime(t, decimals = 2) {
  t = Math.max(0, t);
  const scale = 10 ** decimals;
  let rounded = Math.round(t * scale) / scale;
  const h = Math.floor(rounded / 3600);
  const m = Math.floor((rounded % 3600) / 60);
  const s = (rounded % 60).toFixed(decimals).padStart(decimals ? 3 + decimals : 2, '0');
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${m}:${s}`;
}

// Dates are shown as 2026-10-04 and times as 2026-10-04 18:30, whatever the
// browser's locale.
const pad2 = (n) => String(n).padStart(2, '0');
export function formatDate(d) {
  d = new Date(d);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())}`;
}
export function formatDateTime(d) {
  d = new Date(d);
  return `${formatDate(d)} ${pad2(d.getHours())}:${pad2(d.getMinutes())}`;
}

// dateInput is a date field with the browser's date picker; its value is yyyy-mm-dd.
export function dateInput(attrs = {}) {
  return h('input', { type: 'date', class: 'date', ...attrs });
}

export function formatSize(bytes) {
  if (bytes >= 1 << 30) return (bytes / (1 << 30)).toFixed(1) + ' GB';
  if (bytes >= 1 << 20) return (bytes / (1 << 20)).toFixed(1) + ' MB';
  return Math.max(1, Math.round(bytes / 1024)) + ' kB';
}

// h builds an element: h('a', {href: '#'}, 'text', child). Strings become
// text nodes, so nothing from the server is parsed as HTML.
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v === undefined || v === null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else if (k === 'class') el.className = v;
    else if (v === true) el.setAttribute(k, '');
    else el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c === null || c === undefined || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// kbdText turns the <kbd>key</kbd> marks of a (translated) text into <kbd>
// elements; the rest stays text.
export function kbdText(text) {
  return text.split(/<kbd>(.*?)<\/kbd>/).map((part, i) => (i % 2 ? h('kbd', {}, part) : part));
}

// typing reports whether a key event belongs to a form field, where
// shortcuts must not fire.
export function typing(e) {
  const t = e.target;
  return t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));
}

export function debounce(fn, ms) {
  let timer;
  const later = (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
  later.cancel = () => clearTimeout(timer); // forget a call that is waiting
  return later;
}
