// The trash, a section of the settings: recordings that were deleted wait here for a time (the
// server's --trash-days) before they are removed for good. They can be restored until then.
import { api, h, formatSize, formatDate } from './util.js';
import { alertDialog, confirmDialog } from './dialog.js';
import { t, tn } from './i18n.js';

// trashView fills box with the trash of the church.
export async function trashView(box) {
  const note = h('p', { class: 'muted', role: 'status' });
  const list = h('div', { class: 'panel' });

  async function act(item, what) {
    try {
      await api('POST', `api/trash/${what}?id=` + encodeURIComponent(item.id));
      if (what === 'restore') note.textContent = t('{name} is back among the recordings.', { name: item.name });
      await draw();
    } catch (err) { alertDialog({ body: err.message }); }
  }

  async function remove(item) {
    const ok = await confirmDialog({
      title: t('Delete {name} for good?', { name: item.name }),
      body: h('p', {}, t('It is removed from the trash and cannot be restored.')),
      confirm: t('Delete now'), danger: true,
    });
    if (ok) act(item, 'remove');
  }

  async function draw() {
    const data = await api('GET', 'api/trash');
    list.replaceChildren(...(data.items.length ? data.items.map((item) => h('div', { class: 'trash-item' },
      h('strong', {}, item.name),
      h('span', { class: 'muted' }, ` · ${t('deleted {date}', { date: formatDate(item.deletedAt) })} · ${formatSize(item.size)} · ${tn(item.daysLeft, '{n} day left', '{n} days left')}`),
      h('span', { class: 'trash-actions' },
        h('button', { type: 'button', name: 'restore', onclick: () => act(item, 'restore') }, t('Restore')),
        h('button', { type: 'button', name: 'delete-now', onclick: () => remove(item) }, t('Delete now')))))
      : [h('p', { class: 'muted' }, t('The trash is empty.'))]));
    return data;
  }

  const data = await draw();
  box.replaceChildren(
    h('h2', {}, t('Trash')),
    h('p', { class: 'muted' }, t('Deleted recordings are kept for {days} days, then removed for good.', { days: data.days })),
    list, note);
}
