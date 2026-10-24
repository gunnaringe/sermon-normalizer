// Members, a section of the settings (with the login, --oidc-issuer): an admin sees who the
// church's members are, changes their role, removes them, and makes and revokes invite links;
// a member can leave the church. A church always keeps an admin (the server says so).
import { api, h, formatDate, formatDateTime } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';
import { t } from './i18n.js';

const roleName = (role) => (role === 'admin' ? t('Admin') : t('Member'));

// membersView fills box with the church's members. admin: the user is one of its admins.
export async function membersView(box, church, admin) {
  const leave = (label) => h('button', { type: 'button', name: 'leave', onclick: async () => {
    const ok = await confirmDialog({
      title: t('Leave {church}?', { church: church.name }),
      body: h('p', {}, t('You can no longer use the studio for this church, until someone invites you again.')),
      confirm: t('Leave'), danger: true,
    });
    if (!ok) return;
    try {
      await api('POST', 'api/members/leave');
      location.href = './';
    } catch (err) { alertDialog({ body: err.message }); }
  } }, label);

  if (!admin) {
    box.replaceChildren(h('h2', {}, t('Members')),
      h('div', { class: 'panel' }, h('p', {}, t('You are a member of {church}. Its admins invite others and change its settings.', { church: church.name })), leave(t('Leave this church…'))));
    return;
  }

  const list = h('div', { class: 'panel', id: 'member-list' });
  const invites = h('div', { class: 'panel', id: 'invite-list' });

  async function change(method, url, body) {
    try {
      await api(method, url, body);
    } catch (err) { alertDialog({ body: err.message }); }
    await draw();
  }

  async function draw() {
    const data = await api('GET', 'api/members');
    list.replaceChildren(...data.members.map((m) => {
      const role = h('select', { name: 'role', 'aria-label': t('Role of {name}', { name: m.name }),
        onchange: () => change('PUT', 'api/members/' + m.id, { role: role.value }) },
      ['admin', 'member'].map((r) => h('option', { value: r, selected: r === m.role }, roleName(r))));
      const remove = m.you ? leave(t('Leave…')) : h('button', { type: 'button', name: 'remove', onclick: async () => {
        const ok = await confirmDialog({
          title: t('Remove {name}?', { name: m.name }),
          body: h('p', {}, t('{name} can no longer use the studio for {church}.', { name: m.name, church: church.name })),
          confirm: t('Remove'), danger: true,
        });
        if (ok) change('POST', 'api/members/' + m.id + '/remove');
      } }, t('Remove'));
      return h('div', { class: 'member-item' },
        h('strong', {}, m.name), m.you ? h('span', { class: 'muted' }, ' ' + t('(you)')) : '',
        h('span', { class: 'muted' }, (m.email ? ' · ' + m.email : '') + ' · ' + t('joined {date}', { date: formatDate(m.joined) })),
        h('span', { class: 'trash-actions' }, role, remove));
    }));
    invites.replaceChildren(...(data.invites.length ? data.invites.map((iv) => h('div', { class: 'member-item' },
      h('strong', {}, roleName(iv.role)),
      h('span', { class: 'muted' }, ' · ' + t('made by {name}', { name: iv.createdBy }) + ' · ' + t('works until {date}', { date: formatDateTime(iv.expires) })),
      h('span', { class: 'trash-actions' }, h('button', { type: 'button', name: 'revoke', onclick: async () => {
        const ok = await confirmDialog({ title: t('Revoke this invite?'), body: h('p', {}, t('The link stops working.')), confirm: t('Revoke'), danger: true });
        if (ok) change('POST', 'api/invites/' + iv.id + '/revoke');
      } }, t('Revoke')))))
      : [h('p', { class: 'muted' }, t('No open invites.'))]));
  }

  const role = h('select', { name: 'invite-role' }, h('option', { value: 'member', selected: true }, t('Member')), h('option', { value: 'admin' }, t('Admin')));
  const days = h('select', { name: 'invite-days' }, [[1, t('1 day')], [7, t('7 days')], [30, t('30 days')]].map(([d, label]) =>
    h('option', { value: d, selected: d === 7 }, label)));
  const status = h('span', { class: 'muted', role: 'status' });
  const make = h('button', { class: 'primary', type: 'button', name: 'make-invite', onclick: async () => {
    status.textContent = '';
    try {
      const made = await api('POST', 'api/invites', { role: role.value, days: Number(days.value) });
      await draw();
      await showInvite(made.link || new URL('invite.html#' + made.token, location.href).href, made.invite, church);
    } catch (err) { status.textContent = err.message; }
  } }, t('Make invite link'));

  await draw();
  box.replaceChildren(
    h('h2', {}, t('Members')),
    h('p', { class: 'muted' }, t('Who can use the studio for this church. Admins also change its settings and who its members are.')),
    list,
    h('h3', {}, t('Invite someone')),
    h('div', { class: 'panel tags' },
      h('p', { class: 'muted' }, t('An invite link works once, for the first person who opens it and accepts. Send it to the one you invite only.')),
      h('label', {}, h('span', { class: 'name' }, t('Role')), role),
      h('label', {}, h('span', { class: 'name' }, t('Works for')), days),
      h('div', {}, make, ' ', status)),
    h('h3', {}, t('Open invites')),
    invites);
}

// showInvite shows a new invite's link, once, with a button that copies it.
async function showInvite(link, invite, church) {
  const note = h('span', { class: 'muted', role: 'status' });
  const copy = h('button', { type: 'button', name: 'copy-invite', onclick: async () => {
    try {
      await navigator.clipboard.writeText(link);
      note.textContent = t('Link copied.');
    } catch { note.textContent = t('Select the link and copy it.'); }
  } }, t('Copy link'));
  await alertDialog({ title: t('Invite link made'), extra: [copy, note], body: [
    h('p', {}, invite.role === 'admin'
      ? t('Send this link to the one you invite to {church} as an admin. It is shown only now, and works once, until {date}.', { church: church.name, date: formatDateTime(invite.expires) })
      : t('Send this link to the one you invite to {church} as a member. It is shown only now, and works once, until {date}.', { church: church.name, date: formatDateTime(invite.expires) })),
    h('p', { class: 'share-link' }, link),
  ] });
}
