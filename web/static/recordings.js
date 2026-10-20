// The recordings page: every recording of the church in one list. The ones that
// arrived lately on top ("Newly imported"), then the years, each folded open
// on its own; a search field over names, dates and labels. A row opens the
// details of the recording (sermons.js) right where it is.
import { api, h, debounce, formatDate, formatDateTime } from './util.js';
import { sermonDetail } from './sermons.js';
import { t, tn } from './i18n.js';

function badges(e) {
  const out = [];
  if (e.job) out.push(h('span', { class: 'badge busy' }, t('processing')));
  if (e.converting) out.push(h('span', { class: 'badge busy' }, t('converting')));
  if (e.preparing) out.push(h('span', { class: 'badge busy' }, t('preparing')));
  // Done is cut and saved (or made before there was saving); the files follow.
  const done = e.saved || e.outputs.length > 0;
  if (e.raw && !done) out.push(h('span', { class: 'badge warn', title: t('Not cut yet: open it in the editor') }, t('to do')));
  if (done) out.push(h('span', { class: 'badge on', title: t('Cut and saved') }, t('done')));
  if (!e.job && e.saved && e.outputs.length === 0) {
    out.push(h('span', { class: 'badge warn', title: t('No files made: open it in the editor and press Save') }, t('no files')));
  }
  if (!e.job && e.outdated) {
    out.push(h('span', { class: 'badge warn', title: t('Made from an older cut: open it in the editor and press Save') }, t('files out of date')));
  }
  return out;
}

// arrived says when a recording came: today/yesterday with the time, else the date.
function arrived(when) {
  const d = new Date(when);
  const time = formatDateTime(d).slice(11);
  if (formatDate(d) === formatDate(Date.now())) return t('arrived today {time}', { time });
  if (formatDate(d) === formatDate(Date.now() - 864e5)) return t('arrived yesterday {time}', { time });
  return t('arrived {date}', { date: formatDate(d) });
}

const yearOf = (e) => e.name.slice(0, 4);

// recordingsView fills view with the page: top (the upload) first, then the
// search and the list. It returns { open(path) }, which opens one recording
// (what a link to it does). emptyNote is shown when there is none yet.
export async function recordingsView(view, { top, church, emptyNote }) {
  const data = await api('GET', 'api/recordings');
  const all = data.recordings;
  const newMs = data.newFor * 1000;

  // Which years are open is the browser's, per church; the newest one at first.
  const storeKey = 'openYears:' + church;
  const stored = JSON.parse(localStorage.getItem(storeKey) || 'null');
  const openYears = new Set(stored ?? (all.length ? [yearOf(all[0])] : []));
  const remember = () => localStorage.setItem(storeKey, JSON.stringify([...openYears]));

  let query = '';
  const rows = new Map();  // path → what is needed to open its row, in the year list
  const chips = new Map(); // path → the places where its labels are shown

  // The labels in use, the most used first: what the label field suggests.
  const suggestions = () => {
    const n = new Map();
    for (const e of all) for (const l of e.labels) n.set(l, (n.get(l) || 0) + 1);
    return [...n].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([l]) => l);
  };

  function chipsFor(e) {
    const box = h('span', { class: 'chips' });
    const draw = () => box.replaceChildren(...e.labels.map((l) => h('button', {
      type: 'button', class: 'chip', title: t('Search for {label}', { label: l }),
      onclick: (ev) => { ev.stopPropagation(); setQuery(l); },
    }, l)));
    draw();
    if (!chips.has(e.path)) chips.set(e.path, new Set());
    chips.get(e.path).add(draw);
    return box;
  }

  function row(e, { when = false, register = false } = {}) {
    let detail = null;
    const toggle = h('button', { class: 'twisty', 'aria-expanded': 'false', title: t('Show details'), 'aria-label': t('Show details of {name}', { name: e.name }) }, '▸');
    const flip = (open = !detail) => {
      if (open === !!detail) return;
      if (open) {
        detail = h('tr', { class: 'detail-row' }, h('td', { colspan: 2 }, sermonDetail(e, {
          suggestions,
          changed: () => { for (const draw of chips.get(e.path) || []) draw(); },
        })));
        tr.after(detail);
      } else {
        detail.remove();
        detail = null;
      }
      toggle.setAttribute('aria-expanded', String(open));
      toggle.textContent = open ? '▾' : '▸';
    };
    toggle.addEventListener('click', (ev) => { ev.stopPropagation(); flip(); });
    const tr = h('tr', { class: 'row', onclick: () => flip() },
      h('td', {}, toggle, h('button', { class: 'sermon-name' }, e.name)), // its click reaches the row
      h('td', {}, badges(e), when && e.imported ? h('span', { class: 'muted arrived' }, arrived(e.imported)) : '', chipsFor(e)));
    if (register) rows.set(e.path, { tr, flip });
    return tr;
  }

  const table = (entries, opts) => h('table', { class: 'list' }, h('tbody', {}, entries.map((e) => row(e, opts))));

  function yearSection(year, entries) {
    const sec = h('section', { class: 'year' });
    const draw = () => {
      const open = openYears.has(year);
      sec.replaceChildren(
        h('button', { class: 'year-head', 'aria-expanded': String(open), onclick: () => {
          if (open) openYears.delete(year); else openYears.add(year);
          remember();
          draw();
        } }, h('span', { class: 'tw' }, open ? '▾' : '▸'), year, h('span', { class: 'muted' }, ` · ${tn(entries.length, '{n} recording', '{n} recordings')}`)),
        open ? table(entries, { register: true }) : '');
    };
    draw();
    return sec;
  }

  const matches = (e, tokens) => {
    const hay = (e.name + ' ' + e.labels.join(' ')).toLowerCase();
    return tokens.every((t) => hay.includes(t));
  };

  const list = h('div', { class: 'recordings' });
  function render() {
    rows.clear();
    chips.clear();
    const tokens = query.toLowerCase().split(/\s+/).filter(Boolean);
    if (tokens.length) {
      const found = all.filter((e) => matches(e, tokens));
      list.replaceChildren(
        h('p', { class: 'muted results', role: 'status' }, found.length ? tn(found.length, '{n} recording', '{n} recordings') : t('No recording matches.')),
        found.length ? table(found, { register: true }) : '');
      return;
    }
    if (!all.length) {
      list.replaceChildren(h('p', { class: 'muted' }, emptyNote));
      return;
    }
    const lately = all.filter((e) => e.imported && Date.now() - Date.parse(e.imported) < newMs)
      .sort((a, b) => Date.parse(b.imported) - Date.parse(a.imported));
    const years = new Map();
    for (const e of all) {
      if (!years.has(yearOf(e))) years.set(yearOf(e), []);
      years.get(yearOf(e)).push(e);
    }
    list.replaceChildren(
      lately.length ? h('section', { class: 'recent' }, h('h2', {}, t('Newly imported')), table(lately, { when: true })) : '',
      ...[...years].map(([year, entries]) => yearSection(year, entries)));
  }

  const input = h('input', { type: 'search', name: 'search', class: 'search', placeholder: t('Search name, date or label'),
    'aria-label': t('Search recordings'), autocomplete: 'off' });
  input.addEventListener('input', debounce(() => { query = input.value; render(); }, 120));
  function setQuery(v) {
    input.value = query = v;
    render();
  }
  const note = h('p', { class: 'muted', role: 'status' });

  view.replaceChildren(h('h1', {}, t('Recordings')), top, h('div', { class: 'searchbar' }, input), note, list);
  render();

  return {
    // open shows one recording: its year open, its row expanded, scrolled to.
    open(path) {
      const e = all.find((x) => x.path === path);
      note.textContent = e ? '' : t('That recording was not found.');
      if (!e) return;
      if (query) setQuery('');
      if (!openYears.has(yearOf(e))) {
        openYears.add(yearOf(e));
        remember();
        render();
      }
      const r = rows.get(path);
      if (!r) return;
      r.flip(true);
      r.tr.scrollIntoView({ block: 'center' });
      r.tr.classList.add('target');
      setTimeout(() => r.tr.classList.remove('target'), 2500);
    },
  };
}
