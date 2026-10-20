// Shares: codes that let people without a login send recordings to this
// church's inbox from the public upload page. A code is shown once, when the
// share is made. A section of the settings; sharesView fills its box.
import { api, h, formatSize, formatDate, formatDateTime } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';

const GB = 1 << 30;

function stateText(s) {
  if (s.state) return h('span', { class: 'badge' }, s.state);
  if (s.uploading) return h('span', { class: 'badge busy' }, `${s.uploading} uploading`);
  return h('span', { class: 'badge on' }, 'open');
}

function limits(s) {
  const out = [`up to ${formatSize(s.maxFileSize)} per file`, s.allTypes ? 'all audio' : s.types.join(' ')];
  if (s.maxUploads) out.push(`${s.maxUploads} files`);
  if (s.maxTotal) out.push(`${formatSize(s.maxTotal)} in all`);
  out.push(s.expires ? 'until ' + formatDateTime(s.expires) : 'no expiry');
  return out.join(' · ');
}

// parseExpiry reads yyyy-mm-dd or yyyy-mm-dd hh:mm in local time; a day
// alone lasts to its end. Empty = never.
function parseExpiry(s) {
  s = s.trim();
  if (!s) return null;
  const m = s.match(/^(\d{4})-(\d{2})-(\d{2})(?:[ T](\d{1,2}):(\d{2}))?$/);
  const d = m && (m[4] === undefined
    ? new Date(+m[1], m[2] - 1, +m[3], 23, 59, 59)
    : new Date(+m[1], m[2] - 1, +m[3], +m[4], +m[5]));
  if (!d || d.getMonth() !== m[2] - 1 || d.getDate() !== +m[3] || (m[4] !== undefined && (+m[4] > 23 || +m[5] > 59))) {
    throw new Error('Write the expiry as yyyy-mm-dd or yyyy-mm-dd hh:mm.');
  }
  return d.toISOString();
}

function linkFor(url, code) {
  return url ? url.replace(/\/?$/, '/') + '#code=' + code : '';
}

// holdToCopy copies text when el is held down half a second (a long press on
// a phone). The copy happens as it is let go, inside the gesture, which is
// what browsers allow; the phone's own selection menu is kept away.
function holdToCopy(el, text, done) {
  let since = 0;
  el.addEventListener('pointerdown', () => { since = Date.now(); });
  el.addEventListener('pointerup', () => {
    if (since && Date.now() - since >= 500) navigator.clipboard.writeText(text).then(done);
    since = 0;
  });
  el.addEventListener('pointerleave', () => { since = 0; });
  el.addEventListener('contextmenu', (e) => e.preventDefault());
}

async function showCode(url, code) {
  const link = linkFor(url, code);
  const note = h('span', { class: 'muted', role: 'status' });
  const codeEl = h('span', { class: 'share-code', title: 'Hold to copy' }, code);
  holdToCopy(codeEl, code, () => { note.textContent = 'Code copied.'; });
  const share = h('button', { type: 'button', name: 'share', onclick: async () => {
    const what = link ? { url: link } : { text: code };
    if (navigator.share) {
      try { await navigator.share({ title: 'Send recordings', ...what }); } catch { /* closed */ }
      return;
    }
    await navigator.clipboard.writeText(link || code);
    note.textContent = link ? 'Link copied.' : 'Code copied.';
  } }, 'Share');
  await alertDialog({ title: 'Share made', body: [
    'The code is shown only once. Share it before you close this.', h('br'), h('br'),
    codeEl, h('br'), h('span', { class: 'muted' }, 'Hold the code to copy it.'),
    ...(link ? [h('br'), h('br'), h('span', { class: 'share-link' }, link)] : []),
    h('br'), h('br'), share, ' ', note,
  ] });
}

export async function sharesView(box) {
  const data = await api('GET', 'api/shares');
  const num = (name, attrs = {}) => h('input', { type: 'number', name, min: 0, step: 'any', ...attrs });
  const label = h('input', { type: 'text', name: 'label', maxlength: 80, placeholder: 'e.g. Recordings from the sound desk' });
  const types = data.types.map((t) => h('input', { type: 'checkbox', name: 'type', value: t, checked: true }));
  const typeList = h('span', { hidden: true }, types.map((c) => h('label', { class: 'check' }, c, ' ' + c.value + ' ')));
  const allTypes = h('input', { type: 'checkbox', name: 'allTypes', checked: true,
    onchange: () => { typeList.hidden = allTypes.checked; } });
  const fileSize = num('maxFileSize', { value: data.defaultFileSize / GB, min: 0.01 });
  const uploads = num('maxUploads', { step: 1, placeholder: 'no limit' });
  const total = num('maxTotal', { placeholder: 'no limit' });
  const expires = h('input', { type: 'text', name: 'expires', class: 'date', maxlength: 16, autocomplete: 'off',
    placeholder: 'yyyy-mm-dd [hh:mm]' });
  const status = h('span', { class: 'error' });
  const create = h('button', { class: 'primary', name: 'create-share', onclick: async () => {
    status.textContent = '';
    try {
      const made = await api('POST', 'api/shares', {
        label: label.value,
        allTypes: allTypes.checked,
        types: allTypes.checked ? [] : types.filter((c) => c.checked).map((c) => c.value),
        maxFileSize: Math.round(Number(fileSize.value) * GB),
        maxUploads: Number(uploads.value) || 0,
        maxTotal: Math.round(Number(total.value) * GB) || 0,
        expires: parseExpiry(expires.value),
      });
      await showCode(data.url, made.code);
      await sharesView(box);
    } catch (err) { status.textContent = err.message; }
  } }, 'Make share');

  const revoke = (s) => async () => {
    const ok = await confirmDialog({ title: 'Revoke share', confirm: 'Revoke', danger: true,
      body: `Nobody can upload with the code of "${s.label}" any more. Files already sent stay; uploads in progress stop.` });
    if (!ok) return;
    try {
      await api('POST', `api/shares/${encodeURIComponent(s.id)}/revoke`);
      await sharesView(box);
    } catch (err) { alertDialog({ body: err.message }); }
  };

  const remove = (s) => async () => {
    const ok = await confirmDialog({ title: 'Remove share', confirm: 'Remove', danger: true,
      body: `"${s.label}" and its list of uploads are removed. Files already sent stay.` });
    if (!ok) return;
    try {
      await api('POST', `api/shares/${encodeURIComponent(s.id)}/remove`);
      await sharesView(box);
    } catch (err) { alertDialog({ body: err.message }); }
  };
  const ended = data.shares.filter((s) => s.state);
  const removeEnded = async () => {
    const ok = await confirmDialog({ title: 'Remove ended shares', confirm: 'Remove', danger: true,
      body: `${ended.length} share${ended.length === 1 ? '' : 's'} that no longer take${ended.length === 1 ? 's' : ''} uploads (revoked, expired, used up or full) are removed with their lists of uploads. Files already sent stay.` });
    if (!ok) return;
    try {
      await api('POST', 'api/shares/remove-ended');
      await sharesView(box);
    } catch (err) { alertDialog({ body: err.message }); }
  };

  const rows = data.shares.map((s) => h('div', { class: 'panel share' },
    h('p', {}, h('strong', {}, s.label), ' ', stateText(s), ' ',
      h('span', { class: 'muted mono' }, '…' + s.hint)),
    h('p', { class: 'muted' }, limits(s)),
    h('p', {}, `${s.count ? `${s.count} file${s.count === 1 ? '' : 's'} received (${formatSize(s.bytes)})` : 'Nothing received yet'} · made ${formatDate(s.created)}`),
    s.log.length ? h('details', {}, h('summary', {}, 'Uploads'),
      h('table', { class: 'list' }, s.log.map((u) => h('tr', {},
        h('td', {}, formatDateTime(u.time)), h('td', {}, u.name), h('td', {}, formatSize(u.size)), h('td', { class: 'muted' }, u.ip))))) : '',
    h('p', {}, s.state
      ? h('button', { name: 'remove', onclick: remove(s) }, 'Remove')
      : h('button', { name: 'revoke', onclick: revoke(s) }, 'Revoke'))));

  box.replaceChildren(
    h('h2', {}, 'Shares'),
    h('p', { class: 'muted' }, 'A share is a code that lets someone without a login send recordings on the upload page', data.url ? [' (', h('a', { href: data.url, target: '_blank', rel: 'noopener' }, data.url), ')'] : '',
      '. The recordings arrive in this church like any other. Give people the code or the link. ' +
      'The code is shown only once, when you make the share.'),
    data.inbox ? h('div', { class: 'panel share-form' },
      h('label', { for: 'share-label' }, 'Name'), Object.assign(label, { id: 'share-label' }),
      h('span', {}, 'File types'), h('span', {}, h('label', { class: 'check' }, allTypes, ' All supported audio'), h('br'), typeList),
      h('label', {}, 'Max file size (GB)'), fileSize,
      h('label', {}, 'Max number of files'), uploads,
      h('label', {}, 'Max total size (GB)'), total,
      h('label', {}, 'Expires'), expires,
      h('div', { class: 'wide' }, create, ' ', status))
      : h('p', { class: 'error' }, 'Shares need an inbox folder, and this church has none.'),
    h('h3', {}, 'Made'),
    ended.length ? h('p', {}, h('button', { name: 'remove-ended', onclick: removeEnded }, `Remove ${ended.length} ended`)) : '',
    ...(rows.length ? rows : [h('p', { class: 'muted' }, 'No shares yet.')]));
}
