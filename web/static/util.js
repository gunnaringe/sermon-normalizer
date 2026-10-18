// Shared helpers: API calls, times, and building DOM without innerHTML.

// api calls the server. URLs are relative ("api/..."), so the UI also
// works behind a proxy under a sub-path.
export async function api(method, url, body) {
  const opts = { method, headers: {} };
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(url, opts);
  const data = await resp.json().catch(() => ({}));
  if (!resp.ok) throw new Error(data.error || `${resp.status} ${resp.statusText}`);
  return data;
}

export const q = (path) => encodeURIComponent(path);

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

// typing reports whether a key event belongs to a form field, where
// shortcuts must not fire.
export function typing(e) {
  const t = e.target;
  return t instanceof HTMLElement && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName));
}

export function debounce(fn, ms) {
  let timer;
  return (...args) => {
    clearTimeout(timer);
    timer = setTimeout(() => fn(...args), ms);
  };
}

// crumbs links each level of a slash path to its folder list.
export function crumbs(path, last) {
  const parts = path ? path.split('/') : [];
  const items = [h('a', { href: '#/' }, 'All')];
  parts.forEach((p, i) => {
    items.push(' / ');
    const sub = parts.slice(0, i + 1).join('/');
    items.push(i === parts.length - 1 && !last ? p : h('a', { href: '#/folders/' + sub }, p));
  });
  if (last) items.push(' / ', last);
  return h('div', { class: 'crumbs' }, items);
}
