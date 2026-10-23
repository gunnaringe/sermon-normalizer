// The church's intro and outro clips (clips.go), a section of the settings: the clips, the default intro
// and outro, and the pause at each join. A recording chooses its own in the editor.
import { api, forTenant, h, formatTime, q, INPUT_TYPES } from './util.js';
import { alertDialog, confirmDialog, promptDialog } from './dialog.js';
import { t, tn, tr } from './i18n.js';

// clipsView fills box with the church's clips; it stays empty for a church that cannot have any.
export async function clipsView(box) {
  let data = await api('GET', 'api/clips');
  if (!data.available) return;
  const status = h('span', { class: 'muted', role: 'status' });
  const defaults = h('div', { class: 'panel tags' });
  const list = h('div', { class: 'panel clips' });
  const fileInput = h('input', { type: 'file', name: 'clip', accept: INPUT_TYPES.join(','), hidden: true });
  let replacing = null; // the clip new audio is for, or null for a new clip
  let polling = null;

  const show = (next) => {
    data = next;
    drawDefaults();
    drawList();
    clearTimeout(polling);
    if (data.pending.some((p) => !p.error)) polling = setTimeout(async () => show(await api('GET', 'api/clips')), 1000);
  };
  const act = async (method, url, body) => {
    try {
      show(await api(method, url, body));
      return true;
    } catch (err) {
      alertDialog({ body: err.message });
      return false;
    }
  };

  function drawDefaults() {
    const active = data.clips.filter((c) => !c.archived);
    const pick = (name, label, value) => h('label', {}, h('span', { class: 'name' }, label),
      h('select', { name, oninput: () => { status.textContent = ''; } },
        h('option', { value: '' }, t('None')),
        active.map((c) => h('option', { value: c.id, selected: c.id === value }, c.name))));
    const intro = pick('default-intro', t('Default intro'), data.intro);
    const outro = pick('default-outro', t('Default outro'), data.outro);
    const pause = h('input', { type: 'number', name: 'clip-pause', min: 0, max: data.maxPause, step: 0.1, value: data.pause,
      'aria-label': t('Pause at each join (s)'), oninput: () => { status.textContent = ''; } });
    const save = h('button', { class: 'primary', name: 'save-clips', onclick: async () => {
      if (await act('PUT', 'api/clips/defaults', { intro: intro.querySelector('select').value, outro: outro.querySelector('select').value, pause: Number(pause.value) })) {
        status.textContent = t('Saved.');
      }
    } }, t('Save'));
    defaults.replaceChildren(intro, outro,
      h('label', {}, h('span', { class: 'name' }, t('Pause at each join (s)')), pause),
      h('div', {}, save, ' ', status));
  }

  const upload = (id) => { replacing = id; fileInput.click(); };
  fileInput.addEventListener('change', async () => {
    const file = fileInput.files[0];
    fileInput.value = '';
    if (!file) return;
    const url = (replacing ? 'api/clips/' + replacing + '/replace' : 'api/clips') + '?name=' + q(file.name);
    const resp = await fetch(forTenant(url), { method: 'POST', body: file }).catch((e) => ({ ok: false, json: async () => ({ error: e.message }) }));
    const body = await resp.json().catch(() => ({}));
    if (!resp.ok) {
      alertDialog({ body: tr(body.error || `${resp.status} ${resp.statusText}`) });
      return;
    }
    show(body);
  });

  async function rename(c) {
    const name = await promptDialog({ title: t('Rename clip'), label: t('Name'), value: c.name, confirm: t('Rename'), maxlength: 80 });
    if (name !== null && name.trim() !== c.name) act('PUT', 'api/clips/' + c.id, { name });
  }

  async function remove(c) {
    const ok = await confirmDialog({
      title: t('Delete {name}?', { name: c.name }),
      body: h('p', {}, c.used
        ? tn(c.used, "Used by {n} sermon. Files already made stay as they are, but this sermon can't be regenerated.",
          "Used by {n} sermons. Files already made stay as they are, but these sermons can't be regenerated.")
        : t('No sermon uses it. It is deleted for good.')),
      confirm: t('Delete'), danger: true,
    });
    if (ok) act('POST', 'api/clips/' + c.id + '/delete');
  }

  function clipRow(c) {
    const facts = [formatTime(c.seconds, 0)];
    if (c.id === data.intro) facts.push(t('default intro'));
    if (c.id === data.outro) facts.push(t('default outro'));
    facts.push(c.used ? tn(c.used, 'used by {n} sermon', 'used by {n} sermons') : t('not used'));
    const button = (name, label, onclick, attrs = {}) => h('button', { type: 'button', name, onclick, ...attrs }, label);
    return h('div', { class: 'clip', 'data-clip': c.id },
      h('strong', {}, c.name), h('span', { class: 'muted' }, facts.join(' · ')),
      h('audio', { controls: true, preload: 'none', src: forTenant('api/clips/' + c.id + '/audio') + '&v=' + c.version, 'aria-label': t('Listen to {name}', { name: c.name }) }),
      h('span', { class: 'trash-actions' }, c.archived
        ? [button('restore-clip', t('Restore'), () => act('PUT', 'api/clips/' + c.id, { archived: false })),
          button('delete-clip', t('Delete…'), () => remove(c), { class: 'delete' })]
        : [button('rename-clip', t('Rename…'), () => rename(c)),
          button('replace-clip', t('Replace…'), () => upload(c.id)),
          button('archive-clip', t('Archive'), () => act('PUT', 'api/clips/' + c.id, { archived: true })),
          button('delete-clip', t('Delete…'), () => remove(c), { class: 'delete' })]));
  }

  function pendingRow(p) {
    return h('div', { class: 'clip pending' },
      h('strong', {}, p.name),
      p.error
        ? [h('span', { class: 'error' }, tr(p.error)),
          h('span', { class: 'trash-actions' }, h('button', { type: 'button', name: 'dismiss-clip', onclick: () => act('POST', 'api/clips/' + p.id + '/delete') }, t('Remove')))]
        : h('span', { class: 'muted' }, p.replaces ? t('Replacing…') : t('Preparing…')));
  }

  function drawList() {
    const active = data.clips.filter((c) => !c.archived);
    const archived = data.clips.filter((c) => c.archived);
    list.replaceChildren(
      ...(active.length || data.pending.length ? [...active.map(clipRow), ...data.pending.map(pendingRow)] : [h('p', { class: 'muted' }, t('No clips yet.'))]),
      h('p', {}, h('button', { type: 'button', name: 'upload-clip', onclick: () => upload(null) }, t('Upload clip…')), ' ',
        h('span', { class: 'muted' }, t('Audio of up to {minutes} minutes and {mb} MB.', { minutes: data.maxMinutes, mb: data.maxMB })), fileInput),
      archived.length ? h('details', { class: 'archived' }, h('summary', {}, t('Archived ({n})', { n: archived.length })),
        h('p', { class: 'muted' }, t('Not offered for new choices. Sermons that use them can still be regenerated.')),
        archived.map(clipRow)) : '');
  }

  show(data);
  box.replaceChildren(
    h('h2', {}, t('Intro and outro')),
    h('p', { class: 'muted' }, t("Clips put before and after the sermon, such as a jingle. Each is brought to the sermon's loudness, with a short pause at each join. A recording can choose another clip, or none, in the editor.")),
    defaults, list);
}
