// The app: a hash router. Every address starts with the church: #/<church> (the
// recordings), #/<church>/recording/<path> (the recordings, that one opened),
// #/<church>/folder/<path> (the editor), #/<church>/settings (the settings over the recordings). Settings also opens as an overlay from the gear.
import { api, q, h, href, formatSize, formatDate, formatDateTime, dateInput, forTenant, currentTenant, setTenant, debounce, suggestNames, wheelZoomNeedsKey, setWheelZoomNeedsKey } from './util.js';
import { editorView } from './editor.js';
import { alertDialog, promptDialog } from './dialog.js';
import { sharesView } from './shares.js';
import { recordingsView } from './recordings.js';

const view = document.getElementById('view');
let config = { inbox: false, defaults: {} };
let churches = { tenants: [], default: '', canAdd: false, shares: false };
let teardown = null; // cleanup of the current view (player, event stream, keys)
let shown = '';      // the page in the view, so the settings overlay can open over it without redoing it
let page = null;     // the recordings page when it is shown: { open(path) }
let settingsDialog = null;

// Addresses from before the church was in them: the first part names a page.
const OLD = { folder: 'folder', folders: '', settings: 'settings', shares: 'settings/shares', upload: '' };

async function route() {
  if (teardown) teardown();
  teardown = null;
  const segs = decodeURIComponent(location.hash.slice(1)).split('/').filter(Boolean);
  if (!segs.length || segs[0] in OLD) {
    const keepRest = segs.length > 1 && !['folders', 'upload'].includes(segs[0]); // those pages are the recordings page now
    const old = segs.length ? OLD[segs[0]] + (keepRest ? '/' + segs.slice(1).join('/') : '') : '';
    return location.replace('#/' + currentTenant() + (old ? '/' + old.replace(/^\//, '') : ''));
  }
  const [church, section = '', ...rest] = segs;
  const path = rest.join('/');
  if (!churches.tenants.some((t) => t.id === church)) {
    shown = '';
    view.replaceChildren(h('p', { class: 'error' }, `No such church: ${church}. `, h('a', { href: '#/' }, 'Go to the recordings')));
    return;
  }
  if (church !== currentTenant()) {
    setTenant(church);
    fillPicker();
    config = await api('GET', 'api/config');
  }
  if (settingsDialog && section !== 'settings') settingsDialog.close();
  try {
    if (section === 'folder') {
      shown = '';
      teardown = await editorView(view, path, config);
    } else if (section === '' || section === 'recording' || section === 'settings') {
      if (shown !== church) {
        shown = '';
        page = await foldersView();
        shown = church;
      }
      if (section === 'recording') page.open(path);
      if (section === 'settings') await openSettings(path);
    } else {
      shown = '';
      view.replaceChildren(h('p', { class: 'error' }, 'No such page.'));
    }
  } catch (err) {
    shown = '';
    view.replaceChildren(h('p', { class: 'error' }, err.message));
  }
}

// The recordings page: the upload on top (a recording goes into its date
// folder anyway), then recordings.js.
async function foldersView() {
  return recordingsView(view, {
    top: uploadBox(),
    church: currentTenant(),
    emptyNote: 'No recordings here yet. Upload one above' + (config.inbox ? ', or put one in the inbox folder.' : '.'),
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// suggestDate guesses the recording date from the file name, else the
// file's own date.
function suggestDate(file) {
  for (const m of file.name.matchAll(/(20\d\d)[-_.]?(\d\d)[-_.]?(\d\d)/g)) {
    const d = `${m[1]}-${m[2]}-${m[3]}`;
    if (!Number.isNaN(Date.parse(d))) return { date: d, source: 'from the name' };
  }
  return { date: formatDate(file.lastModified), source: "from the file's date" };
}

// sendFile uploads file into the date folder in chunks: each request stays
// small (proxies limit request bodies), and after a network error or a
// server restart it continues where the server has it.
async function sendFile(file, date, sermon, onProgress) {
  const chunk = Number(localStorage.getItem('uploadChunk')) || Math.min(32 << 20, config.chunk);
  let offset = 0;
  let failures = 0;
  let sent = false; // a chunk may have arrived even if its answer did not
  for (;;) {
    const url = forTenant(`api/upload?date=${q(date)}&sermon=${q(sermon)}&name=${q(file.name)}&size=${file.size}&offset=${offset}&modified=${file.lastModified}`);
    let resp;
    let data = {};
    try {
      resp = await fetch(url, { method: 'POST', body: file.slice(offset, offset + chunk) });
      data = await resp.json().catch(() => ({}));
    } catch (e) {
      sent = true;
      if (++failures > 6) throw new Error('the connection keeps failing: ' + e.message);
      await sleep(1000 * 2 ** failures);
      continue;
    }
    if (resp.status === 201) return data.path;
    if (resp.ok) {
      offset = data.offset;
      sent = true;
      failures = 0;
      onProgress(offset / file.size);
      continue;
    }
    if (resp.status === 409 && typeof data.offset === 'number' && (sent || offset > 0)) {
      offset = data.offset; // continue from what the server has
      continue;
    }
    if (resp.status >= 500 && ++failures <= 6) {
      await sleep(1000 * 2 ** failures);
      continue;
    }
    throw new Error(data.error || `${resp.status} ${resp.statusText}`);
  }
}

// uploadBox is a one-line drop zone; a chosen file opens the form for its
// date and name below it.
function uploadBox() {
  const input = h('input', { type: 'file', accept: '.wav,.flac,audio/wav,audio/flac', hidden: true });
  const chosen = h('div', {});
  const status = h('p', { class: 'muted' });
  const progress = h('progress', { max: 1, value: 0, hidden: true });
  const drop = h('div', { class: 'drop' },
    h('strong', {}, 'Upload a recording: '), 'drop a .wav or .flac file here, or ',
    h('button', { class: 'link', type: 'button', onclick: () => input.click() }, 'choose a file'), '.');

  function choose(file) {
    if (!file) return;
    if (!/\.(wav|flac)$/i.test(file.name)) {
      status.className = 'error';
      status.textContent = `${file.name}: only .wav and .flac recordings are accepted.`;
      return;
    }
    const guess = suggestDate(file);
    const date = dateInput({ name: 'date', value: guess.date, required: true, 'aria-label': 'Date' });
    const sermon = h('input', { type: 'text', maxlength: 60, placeholder: 'optional, e.g. Evening', 'aria-label': 'Name' });
    const button = h('button', { class: 'primary', onclick: async () => {
      if (!date.value) return;
      button.disabled = date.disabled = sermon.disabled = true;
      progress.hidden = false;
      progress.value = 0;
      status.className = 'muted';
      status.textContent = `Uploading ${file.name} (${formatSize(file.size)})…`;
      try {
        const path = await sendFile(file, date.value, sermon.value.trim(), (p) => { progress.value = p; });
        location.hash = href('folder', path);
      } catch (e) {
        status.className = 'error';
        status.textContent = `${file.name}: ${e.message}`;
        button.disabled = date.disabled = sermon.disabled = false;
        progress.hidden = true;
      }
    } }, 'Upload');
    status.textContent = '';
    chosen.replaceChildren(h('div', { class: 'panel' },
      h('p', {}, h('strong', {}, file.name), ' · ', formatSize(file.size), ' ',
        h('button', { class: 'link', type: 'button', onclick: () => { chosen.replaceChildren(); status.textContent = ''; } }, 'cancel')),
      h('p', {}, h('label', {}, 'Date ', date), ' ', h('span', { class: 'muted' }, guess.source)),
      h('p', {}, h('label', {}, 'Name ', sermon), ' ', h('span', { class: 'muted' }, 'Only needed for a second sermon on the same day.')),
      h('p', {}, button)));
    suggestNames(sermon);
  }

  input.addEventListener('change', () => choose(input.files[0]));
  drop.addEventListener('dragover', (e) => { e.preventDefault(); drop.classList.add('over'); });
  drop.addEventListener('dragleave', () => drop.classList.remove('over'));
  drop.addEventListener('drop', (e) => {
    e.preventDefault();
    drop.classList.remove('over');
    choose(e.dataTransfer.files[0]);
  });
  return h('div', { class: 'upload' }, drop, input, chosen, status, progress);
}

// openSettings shows the settings as an overlay on the page, closed with the
// cross, Esc or a click outside. Opened from an address (#/<church>/settings), closing
// it goes back to #/<church>.
async function openSettings(part = '') {
  if (settingsDialog) {
    settingsDialog.querySelector('#shares')?.scrollIntoView();
    return;
  }
  const dialog = h('dialog', { class: 'dialog settings-dialog', 'aria-label': 'Settings' });
  const body = h('div', { class: 'settings' });
  dialog.append(
    h('div', { class: 'settings-head' }, h('h1', {}, 'Settings'),
      h('button', { type: 'button', class: 'close', title: 'Close', 'aria-label': 'Close settings', onclick: () => dialog.close() }, '✕')),
    body);
  dialog.addEventListener('click', (e) => { if (e.target === dialog) dialog.close(); }); // the backdrop
  dialog.addEventListener('close', () => {
    dialog.remove();
    settingsDialog = null;
    if (/\/settings(\/|$)/.test(decodeURIComponent(location.hash))) location.hash = href();
  });
  document.body.append(dialog);
  dialog.showModal();
  settingsDialog = dialog;
  try {
    await settingsView(body, part);
  } catch (err) {
    body.replaceChildren(h('p', { class: 'error' }, err.message));
  }
}

// settingsView edits the church: its name, default tags (templates with
// placeholders, shown filled in for today, with and without a sermon name),
// sermon names, and its shares when the public upload page is on.
// part 'shares' scrolls to the shares.
async function settingsView(box, part) {
  const data = await api('GET', 'api/settings');
  const fields = [['title', 'Title'], ['artist', 'Artist'], ['album', 'Album']];
  const inputs = {};
  const previews = {};
  const status = h('span', { class: 'muted' });
  const save = h('button', { class: 'primary', name: 'save-tags' }, 'Save');
  const rows = fields.map(([key, label]) => {
    inputs[key] = h('input', { type: 'text', name: key, value: data.meta[key], maxlength: 200, oninput: () => { status.textContent = ''; preview(); } });
    previews[key] = h('span', { class: 'muted' });
    return h('label', {}, h('span', { class: 'name' }, label), inputs[key], previews[key]);
  });
  const values = () => Object.fromEntries(fields.map(([key]) => [key, inputs[key].value]));
  const preview = debounce(async () => {
    try {
      const plain = await api('POST', 'api/meta/preview', values());
      const named = await api('POST', 'api/meta/preview', { ...values(), name: 'Evening' });
      for (const [key] of fields) {
        previews[key].classList.remove('error');
        previews[key].textContent = plain[key] || named[key] ? '→ ' + plain[key] + (named[key] !== plain[key] ? '   (with a name: ' + named[key] + ')' : '') : 'empty: no tag';
      }
      save.disabled = false;
    } catch (err) {
      for (const [key] of fields) previews[key].textContent = '';
      previews.title.textContent = err.message;
      previews.title.classList.add('error');
      save.disabled = true;
    }
  }, 250);
  save.addEventListener('click', async () => {
    try {
      await api('PUT', 'api/settings', { meta: values() });
      status.textContent = 'Saved.';
    } catch (err) { status.textContent = err.message; }
  });
  // Remembered sermon names: suggested when naming a sermon; delete one-offs here.
  const names = await api('GET', 'api/names');
  const namesBox = h('div', { class: 'panel' });
  const saveNames = async () => {
    try {
      Object.assign(names, await api('PUT', 'api/names', names));
    } catch (err) { alertDialog({ body: err.message }); }
    showNames();
  };
  function showNames() {
    namesBox.replaceChildren(
      h('label', { class: 'check' }, h('input', {
        type: 'checkbox', name: 'autocomplete', checked: names.enabled,
        onchange: (e) => { names.enabled = e.target.checked; saveNames(); },
      }), 'Suggest names'),
      names.list.length
        ? h('ul', { class: 'names' }, names.list.map((n) => h('li', {}, n, ' ',
          h('button', { class: 'link', title: 'Remove ' + n, 'aria-label': 'Remove ' + n,
            onclick: () => { names.list = names.list.filter((x) => x !== n); saveNames(); } }, '✕'))))
        : h('p', { class: 'muted' }, names.enabled
          ? 'No names yet. Names you use are saved here.' : 'Names are not saved.'));
  }
  showNames();

  // The church's name (kept in church.json in its folder).
  const tenants = await api('GET', 'api/tenants');
  const nameInput = h('input', { type: 'text', name: 'church', maxlength: 80, value: tenants.tenants.find((t) => t.id === currentTenant())?.name || '' });
  const nameStatus = h('span', { class: 'muted' });
  const saveName = h('button', { class: 'primary', name: 'save-church', onclick: async () => {
    try {
      await api('PUT', 'api/church', { name: nameInput.value });
      nameStatus.textContent = 'Saved.';
      window.dispatchEvent(new Event('churches-changed'));
    } catch (err) { nameStatus.textContent = err.message; }
  } }, 'Save');

  // The church's own logo, shown in the header instead of the app's.
  const logoBox = h('div', { class: 'logo-row' });
  const logoStatus = h('span', { class: 'muted' });
  const logoInput = h('input', { type: 'file', name: 'logo', accept: 'image/png,image/jpeg,image/webp,image/svg+xml', hidden: true });
  const showLogo = (logo) => {
    logoBox.replaceChildren(
      logo ? h('img', { src: forTenant('api/logo') + '&v=' + logo, alt: 'Logo' }) : h('span', { class: 'muted' }, 'The Sermon Studio logo is used.'),
      h('button', { type: 'button', name: 'upload-logo', onclick: () => logoInput.click() }, logo ? 'Change logo…' : 'Upload logo…'),
      logo ? h('button', { type: 'button', name: 'remove-logo', onclick: async () => {
        try {
          await api('DELETE', 'api/logo');
          showLogo('');
          window.dispatchEvent(new Event('churches-changed'));
        } catch (err) { logoStatus.textContent = err.message; }
      } }, 'Remove') : '',
      logoInput, logoStatus);
  };
  logoInput.addEventListener('change', async () => {
    const file = logoInput.files[0];
    logoInput.value = '';
    if (!file) return;
    logoStatus.className = 'muted';
    logoStatus.textContent = '';
    const resp = await fetch(forTenant('api/logo'), { method: 'PUT', body: file }).catch((e) => ({ ok: false, json: async () => ({ error: e.message }) }));
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      logoStatus.className = 'error';
      logoStatus.textContent = data.error || `${resp.status} ${resp.statusText}`;
      return;
    }
    showLogo(data.logo);
    logoStatus.textContent = 'Saved.';
    window.dispatchEvent(new Event('churches-changed'));
  });
  showLogo(tenants.tenants.find((t) => t.id === currentTenant())?.logo || '');

  const shares = h('section', { id: 'shares' });
  if (tenants.shares) await sharesView(shares);

  const view_ = h('div', {},
    h('h2', {}, 'Church'),
    h('div', { class: 'panel tags' }, h('label', {}, h('span', { class: 'name' }, 'Name'), nameInput), h('div', {}, saveName, ' ', nameStatus),
      h('p', { class: 'muted' }, 'Logo, shown in the header. PNG, JPEG, WebP or SVG, up to 1 MB.'), logoBox),
    h('h2', {}, 'Default tags'),
    h('p', { class: 'muted' }, 'Written into all generated recordings. You can change them for one recording in the editor. Placeholders: ',
      data.placeholders.flatMap((p, i) => [h('code', {}, '{' + p.name + '}'), ' ' + p.help + (i < data.placeholders.length - 1 ? ' · ' : '.')])),
    h('div', { class: 'panel tags' }, rows, h('div', {}, save, ' ', status)),
    h('h2', {}, 'Sermon names'),
    h('p', { class: 'muted' }, 'A second sermon on the same day needs a name. Used names are suggested next time. Remove one-off names here.'),
    namesBox,
    h('h2', {}, 'Waveform'),
    h('p', { class: 'muted' }, 'Saved in this browser only.'),
    h('div', { class: 'panel' }, h('label', { class: 'check' }, h('input', {
      type: 'checkbox', name: 'wheel-zoom-key', checked: wheelZoomNeedsKey(),
      onchange: (e) => setWheelZoomNeedsKey(e.target.checked),
    }), 'Zoom with the scroll wheel only while holding Ctrl (Cmd on a Mac)')),
    tenants.shares ? shares : '');
  box.replaceChildren(view_);
  if (part === 'shares') shares.scrollIntoView();
  preview();
}

// The church in the header: its name (one church, no adding), else a quiet dropdown.
const select = document.getElementById('church');
const brand = document.querySelector('header .brand');
const nameText = document.getElementById('church-name');
const gear = document.querySelector('[data-nav=settings]');

function fillPicker() {
  const cur = currentTenant();
  const mine = churches.tenants.find((t) => t.id === cur);
  brand.querySelector('img').src = mine?.logo ? forTenant('api/logo') + '&v=' + mine.logo : 'icons/logo.svg';
  brand.href = href();
  gear.href = href('settings');
  const single = churches.tenants.length === 1 && !churches.canAdd;
  select.hidden = single;
  nameText.hidden = !single;
  nameText.textContent = mine?.name || '';
  select.replaceChildren(...churches.tenants.map((t) => h('option', { value: t.id, selected: t.id === cur }, t.name)),
    churches.canAdd ? h('option', { value: '+' }, '+ Add church…') : '');
  select.value = cur;
}

async function loadChurches() {
  churches = await api('GET', 'api/tenants');
  if (!churches.tenants.some((t) => t.id === currentTenant())) setTenant(churches.default);
  fillPicker();
}

// Switching church is going to its address; adding one goes there too.
select.addEventListener('change', async () => {
  if (select.value !== '+') {
    location.hash = '#/' + select.value;
    return;
  }
  const name = await promptDialog({ title: 'Add a church', label: 'Name of the new church', confirm: 'Add church' });
  if (name && name.trim()) {
    try {
      const t = await api('POST', 'api/tenants', { name });
      churches.tenants.push(t);
      location.hash = '#/' + t.id;
      if (t.group) alertDialog({ title: 'Church added', body: `Give people access by adding them to the group ${t.group} in Pocket ID.` });
    } catch (err) { alertDialog({ body: err.message }); }
  }
  fillPicker();
});

// The gear opens the settings over the page you are on (without leaving it).
gear.addEventListener('click', (e) => {
  if (e.button || e.metaKey || e.ctrlKey || e.shiftKey) return; // a new tab goes to the address
  e.preventDefault();
  openSettings();
});

// logoutButton shows Log out when the server knows the login proxy's logout.
// The icon asks first: a click opens "Log out?", which logs out when clicked;
// a click elsewhere, or a few seconds, closes it again.
async function logoutButton() {
  const { logoutUrl } = await api('GET', 'api/session');
  if (!logoutUrl) return;
  const form = document.getElementById('logout');
  const button = form.querySelector('button');
  form.action = logoutUrl;
  form.hidden = false;
  let timer = null;
  const close = () => {
    clearTimeout(timer);
    button.classList.remove('asking');
    button.setAttribute('aria-label', 'Log out');
  };
  button.addEventListener('click', (e) => {
    e.stopPropagation();
    if (button.classList.contains('asking')) {
      form.submit();
      return;
    }
    button.classList.add('asking');
    button.setAttribute('aria-label', 'Log out? Click again to log out');
    timer = setTimeout(close, 5000);
  });
  document.addEventListener('click', close);
}

async function start() {
  await logoutButton();
  await loadChurches();
  config = await api('GET', 'api/config');
  window.addEventListener('churches-changed', loadChurches);
  window.addEventListener('hashchange', route);
  route();
}
start().catch((err) => view.replaceChildren(h('p', { class: 'error' }, err.message)));
