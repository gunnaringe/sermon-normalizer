// Shares: codes that let people without a login send recordings to this
// church's inbox from the public upload page. A code is shown once, when the
// share is made.
import { api, h, formatSize } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';

const GB = 1 << 30;

function stateText(s) {
  if (s.state) return h('span', { class: 'badge' }, s.state);
  if (s.uploading) return h('span', { class: 'badge busy' }, `${s.uploading} uploading`);
  return h('span', { class: 'badge on' }, 'open');
}

function limits(s) {
  const out = [`up to ${formatSize(s.maxFileSize)} per file`, s.types.join(' ')];
  if (s.maxUploads) out.push(`${s.maxUploads} files`);
  if (s.maxTotal) out.push(`${formatSize(s.maxTotal)} in all`);
  out.push(s.expires ? 'until ' + new Date(s.expires).toLocaleString() : 'no expiry');
  return out.join(' · ');
}

function linkFor(url, code) {
  return url ? url.replace(/\/?$/, '/') + '#code=' + code : '';
}

async function showCode(url, code) {
  const link = linkFor(url, code);
  const copy = (text) => h('button', { class: 'link', type: 'button', onclick: (e) => {
    navigator.clipboard.writeText(text).then(() => { e.target.textContent = 'copied'; });
  } }, 'copy');
  await alertDialog({ title: 'Share made', body: [
    'The code is shown only now: copy it, or the link, before closing.', h('br'), h('br'),
    h('span', { class: 'share-code' }, code), ' ', copy(code),
    ...(link ? [h('br'), h('br'), h('span', { class: 'share-link' }, link), ' ', copy(link)] : []),
  ] });
}

export async function sharesView(view) {
  const data = await api('GET', 'api/shares');
  const num = (name, attrs = {}) => h('input', { type: 'number', name, min: 0, step: 'any', ...attrs });
  const label = h('input', { type: 'text', name: 'label', maxlength: 80, placeholder: 'e.g. Recordings from the sound desk' });
  const types = data.types.map((t) => h('input', { type: 'checkbox', name: 'type', value: t, checked: true }));
  const fileSize = num('maxFileSize', { value: data.defaultFileSize / GB, min: 0.01 });
  const uploads = num('maxUploads', { step: 1, placeholder: 'no limit' });
  const total = num('maxTotal', { placeholder: 'no limit' });
  const expires = h('input', { type: 'datetime-local', name: 'expires' });
  const status = h('span', { class: 'error' });
  const create = h('button', { class: 'primary', name: 'create-share', onclick: async () => {
    status.textContent = '';
    try {
      const made = await api('POST', 'api/shares', {
        label: label.value,
        types: types.filter((c) => c.checked).map((c) => c.value),
        maxFileSize: Math.round(Number(fileSize.value) * GB),
        maxUploads: Number(uploads.value) || 0,
        maxTotal: Math.round(Number(total.value) * GB) || 0,
        expires: expires.value ? new Date(expires.value).toISOString() : null,
      });
      await showCode(data.url, made.code);
      await sharesView(view);
    } catch (err) { status.textContent = err.message; }
  } }, 'Make share');

  const act = (s, action) => async () => {
    const ok = await confirmDialog(action === 'revoke'
      ? { title: 'Revoke share', body: `Nobody can upload with the code of "${s.label}" any more. Files already sent stay.`, confirm: 'Revoke', danger: true }
      : { title: 'Delete share', body: `"${s.label}" and its list of uploads go. Files already sent stay; uploads in progress are dropped.`, confirm: 'Delete', danger: true });
    if (!ok) return;
    try {
      await api('POST', `api/shares/${encodeURIComponent(s.id)}/${action}`);
      await sharesView(view);
    } catch (err) { alertDialog({ body: err.message }); }
  };

  const rows = data.shares.map((s) => h('div', { class: 'panel share' },
    h('p', {}, h('strong', {}, s.label), ' ', stateText(s), ' ',
      h('span', { class: 'muted mono' }, '…' + s.hint)),
    h('p', { class: 'muted' }, limits(s)),
    h('p', {}, `${s.count} file${s.count === 1 ? '' : 's'} received (${formatSize(s.bytes)}) · made ${new Date(s.created).toLocaleDateString()}`),
    s.log.length ? h('details', {}, h('summary', {}, 'Uploads'),
      h('table', { class: 'list' }, s.log.map((u) => h('tr', {},
        h('td', {}, new Date(u.time).toLocaleString()), h('td', {}, u.name), h('td', {}, formatSize(u.size)), h('td', { class: 'muted' }, u.ip))))) : '',
    h('p', {}, s.state !== 'revoked' ? h('button', { name: 'revoke', onclick: act(s, 'revoke') }, 'Revoke') : '', ' ',
      h('button', { name: 'delete', onclick: act(s, 'delete') }, 'Delete'))));

  view.replaceChildren(
    h('h1', {}, 'Shares'),
    h('p', { class: 'muted' }, 'A share is a code that lets someone without a login send recordings to this church\'s inbox, ' +
      'on the upload page', data.url ? [' (', h('a', { href: data.url, target: '_blank', rel: 'noopener' }, data.url), ')'] : '',
      '. From the inbox they go into their date folder like any other recording. Hand out the code or the link; ' +
      'the code is shown once, when the share is made.'),
    data.inbox ? h('div', { class: 'panel share-form' },
      h('label', { for: 'share-label' }, 'Name'), Object.assign(label, { id: 'share-label' }),
      h('span', {}, 'File types'), h('span', {}, types.map((c) => h('label', { class: 'check' }, c, ' ' + c.value + ' '))),
      h('label', {}, 'Largest file (GB)'), fileSize,
      h('label', {}, 'Number of files'), uploads,
      h('label', {}, 'In all (GB)'), total,
      h('label', {}, 'Expires'), expires,
      h('div', { class: 'wide' }, create, ' ', status))
      : h('p', { class: 'error' }, 'This church has no inbox, so it cannot have shares.'),
    h('h2', {}, 'Made'),
    ...(rows.length ? rows : [h('p', { class: 'muted' }, 'No shares yet.')]));
}
