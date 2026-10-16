'use strict';

(() => {
  const $ = id => document.getElementById(id);
  const state = { user: null, csrf: '', limits: {}, capabilities: {}, usage: 0, sites: [], invites: [], tab: 'sites', selectedFiles: [], uploadMode: '', uploading: false, uploadSite: null, editSite: null, deleteSite: null, keys: [], keysSiteId: '', keysRequest: 0, keyBusy: false, deleteKey: null, aiKeys: [], aiRequest: 0 };
  const icons = {
    moon: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 14.2A8.4 8.4 0 0 1 9.8 4a8.4 8.4 0 1 0 10.2 10.2Z"/></svg>',
    sun: '<svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/></svg>',
    external: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M14 4h6v6m0-6L10 14M11 4H5a1 1 0 0 0-1 1v14a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-6"/></svg>',
    copy: '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V4H4v12h4"/></svg>',
    upload: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M5 15v5h14v-5M12 16V4m-5 5 5-5 5 5"/></svg>',
    edit: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m14 5 5 5M4 20l5-1L20 8a2.8 2.8 0 0 0-4-4L5 15l-1 5Z"/></svg>',
    trash: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13M10 10v7m4-7v7"/></svg>',
  };
  const incomingURL = new URL(window.location.href);
  let inviteToken = incomingURL.searchParams.get('invite') || '';
  if (incomingURL.searchParams.has('invite')) {
    incomingURL.searchParams.delete('invite');
    history.replaceState(null, '', incomingURL.pathname + incomingURL.search + incomingURL.hash);
  }

  function icon(name) {
    const span = document.createElement('span');
    span.className = 'button-icon';
    span.innerHTML = icons[name]; // Only fixed, local SVG strings are inserted as HTML.
    return span;
  }

  function element(tag, className, content) {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (content !== undefined) node.textContent = content;
    return node;
  }

  function safeURL(value) {
    try {
      const url = new URL(value);
      return ['https:', 'http:'].includes(url.protocol) ? url : null;
    } catch { return null; }
  }

  function bytes(value) {
    const amount = Math.max(0, Number(value) || 0);
    if (amount < 1024) return `${amount} B`;
    const unit = amount < 1024 ** 2 ? 'KiB' : amount < 1024 ** 3 ? 'MiB' : 'GiB';
    const divisor = unit === 'KiB' ? 1024 : unit === 'MiB' ? 1024 ** 2 : 1024 ** 3;
    return `${new Intl.NumberFormat('vi-VN', { maximumFractionDigits: 1 }).format(amount / divisor)} ${unit}`;
  }

  function dateTime(value, short = false) {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return 'Chưa rõ thời gian';
    return new Intl.DateTimeFormat('vi-VN', { day: '2-digit', month: '2-digit', ...(!short && { year: 'numeric', hour: '2-digit', minute: '2-digit' }) }).format(date);
  }

  function showError(id, message = '') {
    $(id).textContent = message;
    $(id).hidden = !message;
  }

  function notify(message, error = false) {
    const toast = element('div', `toast${error ? ' error' : ''}`);
    toast.append(element('span', 'toast-symbol', error ? '!' : '✓'), element('span', '', message));
    const close = element('button', 'toast-close', '×');
    close.type = 'button';
    close.setAttribute('aria-label', 'Đóng thông báo');
    close.addEventListener('click', () => toast.remove());
    toast.append(close);
    while ($('toast-region').children.length >= 2) $('toast-region').firstElementChild.remove();
    $('toast-region').append(toast);
    setTimeout(() => toast.remove(), error ? 9000 : 5500);
  }

  function setTheme(theme) {
    document.documentElement.dataset.theme = theme;
    $('theme-toggle').replaceChildren(icon(theme === 'dark' ? 'sun' : 'moon'));
    $('theme-toggle').setAttribute('aria-label', `Chuyển sang giao diện ${theme === 'dark' ? 'sáng' : 'tối'}`);
    document.querySelector('meta[name="theme-color"]').content = theme === 'dark' ? '#121c18' : '#f7f8f5';
    try { localStorage.setItem('guiweb-theme', theme); } catch { /* Storage may be disabled. */ }
  }

  let storedTheme;
  try { storedTheme = localStorage.getItem('guiweb-theme'); } catch { /* Use system preference. */ }
  setTheme(['light', 'dark'].includes(storedTheme) ? storedTheme : matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light');
  $('theme-toggle').addEventListener('click', () => setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'));

  class APIError extends Error {
    constructor(message, status) { super(message); this.status = status; }
  }

  async function api(path, options = {}) {
    const method = options.method || 'GET';
    const headers = new Headers(options.headers || {});
    const request = { method, headers, credentials: 'same-origin', cache: 'no-store' };
    if (method !== 'GET' && state.csrf) headers.set('X-CSRF-Token', state.csrf);
    if (options.body !== undefined) {
      headers.set('Content-Type', 'application/json');
      request.body = JSON.stringify(options.body);
    }
    let response;
    try { response = await fetch(path, request); }
    catch { throw new APIError('Không kết nối được với GửiWeb. Kiểm tra mạng rồi thử lại nhé.', 0); }
    let data = {};
    try { data = await response.json(); } catch { /* Some upstream failures are not JSON. */ }
    if (!response.ok) {
      if (response.status === 401 && !options.allowUnauthorized) expireSession();
      throw new APIError(data.error || `Không hoàn tất được yêu cầu (${response.status}). Hãy thử lại.`, response.status);
    }
    return data;
  }

  function applySession(data) {
    state.user = data.user;
    state.csrf = data.csrf;
    state.limits = data.limits || {};
    state.capabilities = data.capabilities || {};
    state.usage = Number(data.usage_bytes) || 0;
  }

  function setView(view) {
    for (const name of ['loading', 'auth', 'workspace']) $(`${name}-view`).hidden = name !== view;
    $('account-actions').hidden = view !== 'workspace';
  }

  function showAuth() {
    setView('auth');
    const registering = Boolean(inviteToken);
    $('auth-eyebrow').textContent = registering ? 'BẠN ĐÃ ĐƯỢC MỜI' : 'CHÀO MỪNG TRỞ LẠI';
    $('auth-heading').textContent = registering ? 'Tạo tài khoản' : 'Đăng nhập';
    $('auth-description').textContent = registering ? 'Chọn tên tài khoản và bắt đầu chia sẻ website của bạn.' : 'Không gian để quản lý những website của bạn.';
    $('auth-submit').replaceChildren(document.createTextNode(registering ? 'Tạo tài khoản ' : 'Đăng nhập '), icon('external'));
    $('confirm-field').hidden = !registering;
    $('auth-switch-button').hidden = !registering;
    $('password-confirm').required = registering;
    $('username-hint').hidden = !registering;
    $('password-hint').hidden = !registering;
    $('password').autocomplete = registering ? 'new-password' : 'current-password';
    $('password').minLength = registering ? 12 : 1;
    $('password').maxLength = 72;
    $('auth-footnote').textContent = registering ? 'Link mời có giới hạn lượt tham gia và có thời hạn. Nếu link không dùng được, hãy nhờ người mời gửi link mới.' : 'Chưa có tài khoản? Nhờ chủ GửiWeb gửi bạn một link mời.';
    showError('auth-error');
  }

  function expireSession() {
    if (!state.user) return;
    state.user = null;
    state.csrf = '';
    state.sites = [];
    state.invites = [];
    resetPrivateUI();
    document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
    $('sites-grid').replaceChildren();
    $('invites-list').replaceChildren();
    $('invite-url').value = '';
    $('new-invite-result').hidden = true;
    showAuth();
    notify('Phiên đăng nhập đã hết hạn. Đăng nhập lại để tiếp tục.', true);
  }

  function showWorkspace() {
    setView('workspace');
    $('account-name').textContent = state.user.username;
    $('account-name').title = state.user.admin ? `${state.user.username} · Quản trị viên` : state.user.username;
    $('invites-tab').hidden = !state.user.admin;
    $('workspace-heading').replaceChildren(document.createTextNode(state.user.admin ? 'Những website đã gửi' : 'Website của bạn'), element('span', 'brand-dot', '.'));
    $('workspace-description').textContent = state.user.admin ? 'Một nơi cho những ý tưởng của cả nhóm.' : 'Những ý tưởng đã có một địa chỉ riêng.';
    $('empty-limit').textContent = `HTML · ZIP · Thư mục · Tối đa ${bytes(state.limits.upload_bytes || 50 * 1024 ** 2)}/lượt`;
    $('site-search').value = '';
    switchTab('sites');
  }

  function updateStorage() {
    const used = state.usage;
    const limit = state.user?.admin ? state.limits.total_bytes : state.limits.user_bytes || state.limits.total_bytes;
    $('storage-text').textContent = limit ? `${bytes(used)} / ${bytes(limit)}` : bytes(used);
    $('storage-summary').title = state.user?.admin ? 'Dung lượng website đang sử dụng của cả nhóm' : 'Dung lượng website của bạn';
  }

  async function refreshSites() {
    $('sites-loading').hidden = false;
    $('empty-sites').hidden = true;
    $('sites-grid').setAttribute('aria-busy', 'true');
    showError('sites-error');
    try {
      const [data, session] = await Promise.all([api('/api/sites'), api('/api/me')]);
      if (!state.user) return;
      state.sites = Array.isArray(data.sites) ? data.sites : [];
      applySession(session);
      updateStorage();
      renderSites();
      updateFeatureSites();
    } catch (error) {
      if (error.status !== 401) showError('sites-error', error.message);
    } finally {
      $('sites-loading').hidden = true;
      $('sites-grid').setAttribute('aria-busy', 'false');
    }
  }

  function actionButton(name, title, callback, className = '') {
    const button = element('button', `icon-button ${className}`.trim());
    button.type = 'button';
    button.title = title;
    button.setAttribute('aria-label', title);
    button.append(icon(name));
    button.addEventListener('click', callback);
    return button;
  }

  function siteCard(site) {
    const card = element('article', 'site-card');
    const hash = Array.from(String(site.slug)).reduce((sum, letter) => sum + letter.charCodeAt(0), 0);
    const preview = element('div', `site-preview preview-${hash % 4}`);
    const status = element('span', 'site-live');
    status.append(element('span', 'status-dot'), document.createTextNode('Đang chia sẻ'));
    preview.append(status, element('span', 'site-letter', Array.from(site.name || site.slug)[0]?.toLocaleUpperCase('vi-VN') || 'W'));
    const url = safeURL(site.url);
    if (url) {
      const open = element('a', 'icon-button preview-open');
      open.href = url.href;
      open.target = '_blank';
      open.rel = 'noopener noreferrer';
      open.title = `Mở ${site.name}`;
      open.setAttribute('aria-label', `Mở website ${site.name} trong tab mới`);
      open.append(icon('external'));
      preview.append(open);
    }
    const body = element('div', 'site-body');
    const heading = element('h2', '', site.name);
    heading.title = site.name;
    const link = element(url ? 'a' : 'span', 'site-url', url ? url.host : site.slug);
    if (url) { link.href = url.href; link.target = '_blank'; link.rel = 'noopener noreferrer'; }
    link.title = site.url;
    const meta = element('div', 'site-meta');
    const date = element('span', '', `Cập nhật ${dateTime(site.updated_at, true)}`);
    date.title = dateTime(site.updated_at);
    meta.append(element('span', '', bytes(site.size_bytes)), date);
    body.append(heading, link, meta);
    if (state.user.admin) body.append(element('span', 'site-owner', `Của ${site.owner || state.user.username}`));
    const actions = element('div', 'site-actions');
    const copy = element('button', 'button site-copy');
    copy.type = 'button';
    copy.append(icon('copy'), document.createTextNode('Sao chép link'));
    copy.setAttribute('aria-label', `Sao chép link ${site.name}`);
    copy.disabled = !url;
    copy.addEventListener('click', () => copyText(url.href, copy));
    actions.append(copy, actionButton('upload', `Cập nhật nội dung ${site.name}`, () => openUpload(site)), actionButton('edit', `Chỉnh sửa ${site.name}`, () => openEdit(site)), actionButton('trash', `Xóa ${site.name}`, () => openDelete(site), 'delete-action'));
    body.append(actions);
    card.append(preview, body);
    return card;
  }

  function renderSites() {
    const search = $('site-search').value.trim().toLocaleLowerCase('vi-VN');
    const sites = [...state.sites].sort((a, b) => new Date(b.updated_at) - new Date(a.updated_at)).filter(site => `${site.name} ${site.slug} ${site.owner || ''}`.toLocaleLowerCase('vi-VN').includes(search));
    $('site-count').textContent = state.sites.length;
    $('sites-toolbar').hidden = state.sites.length === 0;
    $('empty-sites').hidden = state.sites.length !== 0;
    $('no-search-results').hidden = !state.sites.length || sites.length !== 0;
    $('sites-grid').replaceChildren(...sites.map(siteCard));
  }

  async function copyText(text, button, message = 'Đã sao chép link. Sẵn sàng để chia sẻ!') {
    try {
      if (navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(text);
      else {
        // Native copy keeps the action useful on a local HTTP preview too.
        const input = element('textarea', 'sr-only');
        input.value = text;
        (document.querySelector('dialog[open]') || document.body).append(input);
        input.select();
        const copied = document.execCommand('copy');
        input.remove();
        if (!copied) throw new Error('copy');
      }
      notify(message);
    } catch {
      notify('Không sao chép được tự động. Bạn có thể chọn và sao chép nội dung.', true);
    } finally { button?.focus(); }
  }

  function switchTab(tab) {
    if (tab === 'invites' && !state.user?.admin) return;
    if (!['sites', 'keys', 'ai', 'invites'].includes(tab)) return;
    $('key-value').value = '';
    state.tab = tab;
    for (const name of ['sites', 'keys', 'ai', 'invites']) {
      const active = name === tab;
      $(`${name}-tab`).classList.toggle('active', active);
      $(`${name}-tab`).setAttribute('aria-selected', String(active));
      $(`${name}-tab`).tabIndex = active ? 0 : -1;
      $(`${name}-panel`).hidden = !active;
    }
    if (tab === 'invites') refreshInvites();
    if (tab === 'keys') { updateFeatureSites(); loadSiteKeys(); }
    if (tab === 'ai') { updateFeatureSites(); loadAIKeys(); }
  }

  async function refreshInvites() {
    showError('invites-error');
    try {
      const data = await api('/api/invites');
      if (!state.user?.admin) return;
      state.invites = Array.isArray(data.invites) ? data.invites : [];
      renderInvites();
    } catch (error) { if (error.status !== 401) showError('invites-error', error.message); }
  }

  function renderInvites() {
    $('empty-invites').hidden = state.invites.length !== 0;
    $('invites-list').replaceChildren(...state.invites.map((invite, index) => {
      const row = element('div', 'invite-row');
      const info = element('div', 'invite-info');
      const detail = element('div');
      const { count, max, active, label } = inviteStats(invite);
      detail.append(element('strong', '', `Lời mời ${String(index + 1).padStart(2, '0')}`), element('span', 'invite-usage', `${count}/${max} lượt đã dùng`), element('span', 'field-hint', `Hết hạn ${dateTime(invite.expires_at)}`));
      if ($('new-invite-result').dataset.id === String(invite.id)) renderNewInviteUsage(invite);
      info.append(element('span', 'invite-number', String(index + 1).padStart(2, '0')), detail);
      const actions = element('div', 'invite-row-actions');
      actions.append(element('span', `invite-status${active ? ' active' : ''}`, label));
      if (active) {
        const revoke = element('button', 'button button-small button-quiet', 'Thu hồi');
        revoke.type = 'button';
        revoke.setAttribute('aria-label', `Thu hồi lời mời ${invite.id}`);
        revoke.addEventListener('click', async () => {
          revoke.disabled = true;
          try {
            await api(`/api/invites/${encodeURIComponent(invite.id)}`, { method: 'DELETE' });
            if ($('new-invite-result').dataset.id === String(invite.id)) { $('new-invite-result').hidden = true; $('invite-url').value = ''; }
            notify('Đã thu hồi lời mời.');
            await refreshInvites();
          } catch (error) { if (error.status !== 401) showError('invites-error', error.message); }
          finally { revoke.disabled = false; }
        });
        actions.append(revoke);
      }
      row.append(info, actions);
      return row;
    }));
  }

  function inviteStats(invite) {
    const max = Number.isInteger(invite.max_uses) && invite.max_uses > 0 ? invite.max_uses : 1;
    const count = Number.isInteger(invite.use_count) && invite.use_count >= 0 ? invite.use_count : invite.used ? 1 : 0;
    const remaining = Math.max(0, max - count);
    const expired = new Date(invite.expires_at).getTime() <= Date.now();
    const exhausted = Boolean(invite.used) || remaining === 0;
    const active = !invite.revoked && !expired && !exhausted;
    const label = invite.revoked ? 'Đã thu hồi' : expired ? 'Đã hết hạn' : exhausted ? max === 1 ? 'Đã sử dụng' : 'Đã đủ lượt' : `Còn ${remaining} lượt`;
    return { max, count, remaining, expired, exhausted, active, label };
  }

  function renderNewInviteUsage(invite) {
    const { max, count, remaining, expired, exhausted, active } = inviteStats(invite);
    $('invite-result-usage').textContent = `Đã dùng ${count}/${max} lượt${active ? ` · Còn ${remaining} lượt tham gia` : ''}.`;
    $('invite-result-expiry').textContent = `Hết hạn ${dateTime(invite.expires_at)}.`;
    $('invite-result-hint').textContent = invite.revoked ? 'Link đã được thu hồi.' : expired ? 'Link đã hết hạn. Tạo link mới nếu muốn mời thêm người.' : exhausted ? 'Link đã dùng đủ lượt. Tạo link mới để mời thêm người.' : max > 1 ? `Link dùng chung cho tối đa ${max} tài khoản. Người nhận có thể chuyển tiếp cho bạn bè.` : 'Link chỉ dùng cho một tài khoản. Hãy gửi trực tiếp cho người bạn muốn mời.';
  }

  function setDialogBusy(dialog, busy) {
    dialog.dataset.busy = String(busy);
    dialog.querySelectorAll('input,button,textarea,select').forEach(control => { control.disabled = busy; });
    dialog.setAttribute('aria-busy', String(busy));
  }

  for (const dialog of document.querySelectorAll('dialog')) {
    dialog.querySelectorAll('.dialog-close,.dialog-cancel').forEach(button => button.addEventListener('click', () => { if (dialog.dataset.busy !== 'true') dialog.close(); }));
    dialog.addEventListener('cancel', event => { if (dialog.dataset.busy === 'true') event.preventDefault(); });
    dialog.addEventListener('click', event => {
      if (event.target !== dialog || dialog.dataset.busy === 'true') return;
      const bounds = dialog.getBoundingClientRect();
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) dialog.close();
    });
    dialog.addEventListener('close', () => {
      if (!document.querySelector('dialog[open]')) document.body.classList.remove('modal-open');
    });
  }

  function showDialog(dialog) {
    document.body.classList.add('modal-open');
    dialog.showModal();
  }

  function slugify(value) {
    return value.toLowerCase().replace(/đ/g, 'd').normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 40).replace(/-+$/g, '');
  }

  function openUpload(site = null) {
    state.uploadSite = site;
    state.selectedFiles = [];
    state.uploadMode = '';
    state.uploading = false;
    $('upload-form').reset();
    setDialogBusy($('upload-dialog'), false);
    $('site-slug').dataset.touched = 'false';
    $('new-site-fields').hidden = Boolean(site);
    $('site-name').required = !site;
    $('site-slug').required = !site;
    $('upload-spa-field').hidden = Boolean(site);
    $('upload-heading').textContent = site ? 'Cập nhật nội dung' : 'Tải website lên';
    $('upload-eyebrow').textContent = site ? 'LINK CŨ, Ý TƯỞNG MỚI' : 'THÊM MỘT ĐIỀU MỚI';
    $('upload-description').textContent = site ? `Thay nội dung của “${site.name}”. Link chia sẻ vẫn giữ nguyên; nếu bản mới lỗi, website hiện tại vẫn được giữ lại.` : 'Chọn nội dung, đặt một cái tên và sẵn sàng chia sẻ.';
    $('upload-submit').replaceChildren(document.createTextNode(site ? 'Cập nhật website ' : 'Tạo link chia sẻ '), icon('external'));
    $('upload-progress').hidden = true;
    showError('upload-error');
    renderFileSelection();
    renderHTMLSource();
    showDialog($('upload-dialog'));
  }

  function inferUploadMode(files, source) {
    if (source === 'folder' || files.some(file => file.webkitRelativePath)) return 'folder';
    if (files.length > 1) return 'folder';
    if (/\.zip$/i.test(files[0]?.name || '')) return 'zip';
    if (/\.html?$/i.test(files[0]?.name || '')) return 'html';
    return '';
  }

  function folderHasIndex(files) {
    const paths = files.map(file => file.webkitRelativePath || file.name);
    if (paths.includes('index.html')) return true;
    const wrapper = paths[0]?.split('/')[0];
    return Boolean(wrapper && paths.every(path => path.startsWith(wrapper + '/')) && paths.includes(wrapper + '/index.html'));
  }

  function clearFiles() {
    state.selectedFiles = [];
    state.uploadMode = '';
    $('upload-files').value = '';
    $('upload-folder-files').value = '';
  }

  function chooseFiles(selection, source = 'files') {
    const files = Array.from(selection);
    const mode = inferUploadMode(files, source);
    showError('upload-error');
    let error = '';
    const size = files.reduce((sum, file) => sum + file.size, 0);
    if (!files.length) error = 'Chưa có file nào được chọn. Hãy chọn HTML, ZIP hoặc thư mục website.';
    else if (!mode) error = 'File này chưa phải một website. Hãy chọn file HTML, file ZIP hoặc thư mục chứa index.html.';
    else if (state.limits.max_files && files.length > state.limits.max_files) error = `Website vượt giới hạn ${state.limits.max_files} file. Hãy bớt file rồi thử lại.`;
    else if (size > (state.limits.upload_bytes || 50 * 1024 ** 2)) error = `Nội dung vượt giới hạn ${bytes(state.limits.upload_bytes || 50 * 1024 ** 2)} mỗi lượt. Hãy giảm dung lượng rồi thử lại.`;
    else if (mode === 'folder' && size > (state.limits.site_bytes || 100 * 1024 ** 2)) error = `Website vượt giới hạn ${bytes(state.limits.site_bytes || 100 * 1024 ** 2)}. Hãy giảm dung lượng rồi thử lại.`;
    else if (mode === 'folder' && !folderHasIndex(files)) error = 'Không tìm thấy index.html ở thư mục gốc. Hãy chọn đầy đủ thư mục website đã build hoặc các file đi cùng index.html.';
    if (error) { clearFiles(); showError('upload-error', error); }
    else { state.selectedFiles = files; state.uploadMode = mode; }
    renderFileSelection();
  }

  function renderFileSelection() {
    const files = state.selectedFiles;
    const mode = state.uploadMode;
    $('file-selection').hidden = files.length === 0;
    $('file-dropzone').hidden = files.length !== 0;
    updateUploadReady();
    $('upload-limit-hint').textContent = `Tối đa ${bytes(state.limits.upload_bytes || 50 * 1024 ** 2)}/lượt. ${mode === 'html' ? 'File HTML sẽ là trang chính. Các tài nguyên đi kèm cần tải cùng bằng ZIP hoặc thư mục.' : `ZIP, thư mục hoặc nhóm file cần có index.html. Website tối đa ${bytes(state.limits.site_bytes || 100 * 1024 ** 2)}.`}`;
    $('file-selection-type').textContent = '';
    if (!files.length) return;
    $('file-selection-type').textContent = `Đã nhận dạng: ${mode === 'zip' ? 'File ZIP' : mode === 'html' ? 'File HTML' : files.some(file => file.webkitRelativePath) ? 'Thư mục website' : 'Nhóm file website'}`;
    $('file-selection-name').textContent = mode === 'folder' ? files[0].webkitRelativePath?.split('/')[0] || 'Các file website đã chọn' : files[0].name;
    $('file-selection-size').textContent = `${files.length} file · ${bytes(files.reduce((sum, file) => sum + file.size, 0))}`;
  }

  function pastedHTMLMode() { return document.querySelector('input[name="upload-source"]:checked').value === 'html'; }

  function htmlSize() { return new Blob([$('upload-html').value]).size; }

  function htmlLimit() { return Math.min(state.limits.upload_bytes || 50 * 1024 ** 2, state.limits.site_bytes || 100 * 1024 ** 2); }

  function updateUploadReady() {
    const ready = pastedHTMLMode() ? Boolean($('upload-html').value.trim()) && htmlSize() <= htmlLimit() : state.selectedFiles.length > 0;
    $('upload-submit').disabled = !ready || state.uploading;
  }

  function renderHTMLSource() {
    const html = pastedHTMLMode();
    $('upload-file-source').hidden = html;
    $('upload-html-source').hidden = !html;
    $('upload-html').required = html;
    $('upload-html-size').textContent = `${bytes(htmlSize())} / ${bytes(htmlLimit())}`;
    updateUploadReady();
  }

  function sendUpload(path, data) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('POST', path);
      xhr.setRequestHeader('X-CSRF-Token', state.csrf);
      xhr.timeout = 10 * 60 * 1000;
      xhr.upload.addEventListener('progress', event => {
        if (!event.lengthComputable) { $('upload-progress-bar').removeAttribute('value'); return; }
        const percent = Math.min(100, Math.round(event.loaded / event.total * 100));
        $('upload-progress-bar').value = percent;
        $('upload-progress-percent').textContent = `${percent}%`;
        $('upload-progress-label').textContent = percent === 100 ? 'Đang kiểm tra và tạo website…' : 'Đang tải nội dung lên…';
        if (percent === 100) { $('upload-progress-bar').removeAttribute('value'); $('upload-progress-percent').textContent = ''; }
      });
      xhr.addEventListener('load', () => {
        let result = {};
        try { result = JSON.parse(xhr.responseText); } catch { /* Upstream may return HTML. */ }
        if (xhr.status >= 200 && xhr.status < 300) resolve(result);
        else {
          if (xhr.status === 401) expireSession();
          reject(new APIError(result.error || `Không hoàn tất được upload (${xhr.status}). Hãy thử lại.`, xhr.status));
        }
      });
      xhr.addEventListener('error', () => reject(new APIError('Mất kết nối khi tải lên. Kiểm tra danh sách website trước khi thử lại.', 0)));
      xhr.addEventListener('timeout', () => reject(new APIError('Upload mất quá nhiều thời gian. Kiểm tra danh sách website trước khi thử lại.', 0)));
      xhr.send(data);
    });
  }

  $('upload-form').addEventListener('submit', async event => {
    event.preventDefault();
    if (state.uploading) return;
    showError('upload-error');
    const pasted = pastedHTMLMode();
    if (pasted && !$('upload-html').value.trim()) { showError('upload-error', 'Bạn chưa dán mã HTML.'); $('upload-html').focus(); return; }
    if (pasted && htmlSize() > htmlLimit()) { showError('upload-error', `Mã HTML vượt giới hạn ${bytes(htmlLimit())}. Hãy giảm dung lượng rồi thử lại.`); return; }
    if (!pasted && !state.selectedFiles.length) { showError('upload-error', 'Bạn chưa chọn nội dung website.'); $('choose-files-button').focus(); return; }
    if (!state.uploadSite && !$('site-name').value.trim()) { showError('upload-error', 'Hãy nhập tên website.'); $('site-name').focus(); return; }
    const mode = pasted ? 'html' : state.uploadMode;
    const data = new FormData();
    data.append('mode', mode);
    if (!state.uploadSite) { data.append('name', $('site-name').value.trim()); data.append('slug', $('site-slug').value.trim()); data.append('spa', String($('site-spa').checked)); }
    if (pasted) data.append('files', new File([$('upload-html').value], 'index.html', { type: 'text/html;charset=utf-8' }), 'index.html');
    else for (const file of state.selectedFiles) data.append('files', file, mode === 'folder' ? file.webkitRelativePath || file.name : file.name);
    const replacing = Boolean(state.uploadSite);
    const path = replacing ? `/api/sites/${encodeURIComponent(state.uploadSite.id)}/upload` : '/api/sites';
    state.uploading = true;
    setDialogBusy($('upload-dialog'), true);
    $('upload-progress').hidden = false;
    $('upload-progress-bar').value = 0;
    $('upload-progress-label').textContent = 'Đang tải nội dung lên…';
    $('upload-progress-percent').textContent = '0%';
    try {
      await sendUpload(path, data);
      $('upload-progress-label').textContent = 'Đang cập nhật danh sách website…';
      switchTab('sites');
      await refreshSites();
      $('upload-dialog').close();
      if (state.user) notify(replacing ? 'Đã cập nhật website. Link chia sẻ vẫn giữ nguyên.' : 'Website đã sẵn sàng! Sao chép link để chia sẻ nhé.');
    } catch (error) {
      if (error.status !== 401) { showError('upload-error', error.message); $('upload-progress').hidden = true; }
    } finally {
      state.uploading = false;
      setDialogBusy($('upload-dialog'), false);
      updateUploadReady();
    }
  });

  function openEdit(site) {
    state.editSite = site;
    $('edit-name').value = site.name;
    $('edit-spa').checked = Boolean(site.spa);
    setDialogBusy($('edit-dialog'), false);
    showError('edit-error');
    showDialog($('edit-dialog'));
  }

  $('edit-form').addEventListener('submit', async event => {
    event.preventDefault();
    const name = $('edit-name').value.trim();
    if (!name) { showError('edit-error', 'Hãy nhập tên website.'); return; }
    const body = { name, spa: $('edit-spa').checked };
    setDialogBusy($('edit-dialog'), true);
    showError('edit-error');
    try {
      await api(`/api/sites/${encodeURIComponent(state.editSite.id)}`, { method: 'PATCH', body });
      await refreshSites();
      $('edit-dialog').close();
      if (state.user) notify('Đã lưu thay đổi.');
    } catch (error) { if (error.status !== 401) showError('edit-error', error.message); }
    finally { setDialogBusy($('edit-dialog'), false); }
  });

  function openDelete(site) {
    state.deleteSite = site;
    $('delete-name').textContent = site.name;
    setDialogBusy($('delete-dialog'), false);
    showError('delete-error');
    showDialog($('delete-dialog'));
  }

  $('delete-submit').addEventListener('click', async () => {
    setDialogBusy($('delete-dialog'), true);
    showError('delete-error');
    try {
      await api(`/api/sites/${encodeURIComponent(state.deleteSite.id)}`, { method: 'DELETE' });
      await refreshSites();
      $('delete-dialog').close();
      if (state.user) notify('Đã xóa website.');
    } catch (error) { if (error.status !== 401) showError('delete-error', error.message); }
    finally { setDialogBusy($('delete-dialog'), false); }
  });

  $('auth-form').addEventListener('submit', async event => {
    event.preventDefault();
    showError('auth-error');
    const registering = Boolean(inviteToken);
    const password = $('password').value;
    if (registering && password !== $('password-confirm').value) { showError('auth-error', 'Hai mật khẩu chưa giống nhau. Bạn kiểm tra lại nhé.'); $('password-confirm').focus(); return; }
    if (new TextEncoder().encode(password).length > 72) { showError('auth-error', 'Mật khẩu quá dài. Hãy dùng mật khẩu ngắn hơn.'); return; }
    const body = { username: $('username').value.trim(), password, ...(registering && { token: inviteToken }) };
    $('auth-submit').disabled = true;
    try {
      const data = await api(registering ? '/api/register' : '/api/login', { method: 'POST', body, allowUnauthorized: true });
      applySession(data);
      if (registering) inviteToken = '';
      $('password').value = '';
      $('password-confirm').value = '';
      $('password').type = 'password';
      $('password-toggle').textContent = 'Hiện';
      $('password-toggle').setAttribute('aria-label', 'Hiện mật khẩu');
      $('password-toggle').setAttribute('aria-pressed', 'false');
      showWorkspace();
      await refreshSites();
      if (registering) notify('Chào mừng bạn đến với GửiWeb!');
    } catch (error) { showError('auth-error', error.message); }
    finally { $('auth-submit').disabled = false; }
  });

  $('password-toggle').addEventListener('click', () => {
    const visible = $('password').type === 'password';
    $('password').type = visible ? 'text' : 'password';
    $('password-toggle').textContent = visible ? 'Ẩn' : 'Hiện';
    $('password-toggle').setAttribute('aria-label', visible ? 'Ẩn mật khẩu' : 'Hiện mật khẩu');
    $('password-toggle').setAttribute('aria-pressed', String(visible));
  });

  $('auth-switch-button').addEventListener('click', () => {
    inviteToken = '';
    $('auth-form').reset();
    showAuth();
    $('username').focus();
  });

  $('logout-button').addEventListener('click', async () => {
    $('logout-button').disabled = true;
    try {
      await api('/api/logout', { method: 'POST', body: {} });
      state.user = null;
      state.csrf = '';
      state.sites = [];
      state.invites = [];
      resetPrivateUI();
      $('sites-grid').replaceChildren();
      $('invites-list').replaceChildren();
      $('invite-url').value = '';
      $('new-invite-result').hidden = true;
      $('auth-form').reset();
      showAuth();
      notify('Đã đăng xuất. Hẹn gặp lại bạn!');
    } catch (error) { if (error.status !== 401) notify(error.message, true); }
    finally { $('logout-button').disabled = false; }
  });

  let creatingInvite = false;

  function updateInviteKind() {
    const single = document.querySelector('input[name="invite-kind"]:checked').value === 'single';
    $('invite-capacity-field').hidden = single;
    $('invite-max-uses').disabled = single || creatingInvite;
    const duration = single || Number($('invite-max-uses').value) === 1 ? '48 giờ' : '7 ngày';
    $('invite-create-hint').textContent = single ? 'Một người có thể tạo tài khoản từ link này. Link hết hạn sau 48 giờ.' : `Người nhận có thể chuyển tiếp cho bạn bè. Chọn 1–1000 lượt tham gia; link hết hạn sau ${duration}.`;
  }

  document.querySelectorAll('input[name="invite-kind"]').forEach(input => input.addEventListener('change', updateInviteKind));
  $('invite-max-uses').addEventListener('input', updateInviteKind);

  $('invite-create-form').addEventListener('submit', async event => {
    event.preventDefault();
    if (creatingInvite) return;
    const single = document.querySelector('input[name="invite-kind"]:checked').value === 'single';
    const maxUses = single ? 1 : Number($('invite-max-uses').value);
    if (!Number.isInteger(maxUses) || maxUses < 1 || maxUses > 1000) { showError('invites-error', 'Hãy chọn từ 1 đến 1000 lượt tham gia.'); $('invite-max-uses').focus(); return; }
    creatingInvite = true;
    $('invite-create-form').querySelectorAll('input,button').forEach(control => { control.disabled = true; });
    $('invite-create-form').setAttribute('aria-busy', 'true');
    showError('invites-error');
    try {
      const data = await api('/api/invites', { method: 'POST', body: { max_uses: maxUses } });
      if (!state.user?.admin) return;
      const url = safeURL(data.url);
      if (!url) throw new Error('Link mời chưa hợp lệ. Hãy kiểm tra cấu hình địa chỉ của GửiWeb.');
      $('invite-url').value = url.href;
      renderNewInviteUsage(data);
      $('new-invite-result').hidden = false;
      $('new-invite-result').dataset.id = String(data.id);
      await refreshInvites();
      if (!state.user?.admin) return;
      $('invite-url').focus();
      $('invite-url').select();
      notify('Đã tạo link mời. Sao chép và gửi cho người quen nhé.');
    } catch (error) { if (error.status !== 401) showError('invites-error', error.message); }
    finally {
      creatingInvite = false;
      $('invite-create-form').querySelectorAll('input,button').forEach(control => { control.disabled = false; });
      $('invite-create-form').setAttribute('aria-busy', 'false');
      updateInviteKind();
    }
  });

  $('copy-invite-button').addEventListener('click', () => copyText($('invite-url').value, $('copy-invite-button')));
  $('new-site-button').addEventListener('click', () => openUpload());
  $('empty-upload-button').addEventListener('click', () => openUpload());
  $('site-search').addEventListener('input', renderSites);
  $('sites-tab').addEventListener('click', () => switchTab('sites'));
  $('keys-tab').addEventListener('click', () => switchTab('keys'));
  $('ai-tab').addEventListener('click', () => switchTab('ai'));
  $('invites-tab').addEventListener('click', () => switchTab('invites'));
  document.querySelector('.tabs').addEventListener('keydown', event => {
    if (!['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const tabs = ['sites', 'keys', 'ai', ...(state.user?.admin ? ['invites'] : [])];
    const index = tabs.indexOf(state.tab);
    const tab = event.key === 'Home' ? tabs[0] : event.key === 'End' ? tabs[tabs.length - 1] : tabs[(index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.length) % tabs.length];
    switchTab(tab);
    $(`${tab}-tab`).focus();
  });
  $('site-name').addEventListener('input', () => { if ($('site-slug').dataset.touched !== 'true') $('site-slug').value = slugify($('site-name').value); });
  $('site-slug').addEventListener('input', () => { $('site-slug').dataset.touched = 'true'; });
  $('choose-files-button').addEventListener('click', () => $('upload-files').click());
  $('choose-folder-button').addEventListener('click', () => $('upload-folder-files').click());
  $('choose-folder-button').hidden = !('webkitdirectory' in $('upload-folder-files'));
  $('upload-files').addEventListener('change', event => { if (event.target.files.length) chooseFiles(event.target.files, 'files'); });
  $('upload-folder-files').addEventListener('change', event => { if (event.target.files.length) chooseFiles(event.target.files, 'folder'); });
  $('clear-files-button').addEventListener('click', () => { clearFiles(); showError('upload-error'); renderFileSelection(); $('choose-files-button').focus(); });
  document.querySelectorAll('input[name="upload-source"]').forEach(input => input.addEventListener('change', () => {
    if (state.uploading) return;
    clearFiles();
    $('upload-html').value = '';
    showError('upload-error');
    renderFileSelection();
    renderHTMLSource();
    if (pastedHTMLMode()) $('upload-html').focus();
  }));
  $('upload-html').addEventListener('input', () => {
    renderHTMLSource();
    showError('upload-error', htmlSize() > htmlLimit() ? `Mã HTML vượt giới hạn ${bytes(htmlLimit())}. Hãy giảm dung lượng rồi thử lại.` : '');
  });
  $('file-dropzone').addEventListener('dragover', event => {
    event.preventDefault();
    if (!state.uploading) $('file-dropzone').classList.add('drag-over');
  });
  $('file-dropzone').addEventListener('dragleave', () => $('file-dropzone').classList.remove('drag-over'));
  $('file-dropzone').addEventListener('drop', event => {
    event.preventDefault();
    $('file-dropzone').classList.remove('drag-over');
    if (state.uploading) return;
    const containsDirectory = Array.from(event.dataTransfer.items || []).some(item => item.webkitGetAsEntry?.()?.isDirectory);
    if (containsDirectory) { showError('upload-error', 'Hãy dùng “Chọn thư mục” để giữ đầy đủ các file bên trong, hoặc tải lên bản ZIP của website.'); return; }
    chooseFiles(event.dataTransfer.files, 'files');
  });

  function ownSites() { return state.sites.filter(site => site.owner_id === state.user?.id); }

  function keyLimit() { return state.capabilities.key_limit || 20; }

  function keyBytesLimit() { return state.capabilities.key_value_bytes || 4096; }

  function telegramLimits() {
    return { text_max_chars: 4096, ip_per_minute: 5, site_per_minute: 20, global_per_minute: 60, ...state.capabilities.telegram };
  }

  function resetPrivateUI() {
    state.keys = [];
    state.aiKeys = [];
    state.keysSiteId = '';
    state.keysRequest += 1;
    state.aiRequest += 1;
    state.keyMutation = (state.keyMutation || 0) + 1;
    state.keyDeleteMutation = (state.keyDeleteMutation || 0) + 1;
    state.keyBusy = false;
    state.keysLoading = false;
    state.capabilities = {};
    $('key-form').reset();
    $('key-value').value = '';
    $('keys-list').replaceChildren();
    $('keys-site').replaceChildren();
    $('ai-site').replaceChildren(element('option', '', 'Tạo website mới'));
    $('ai-site').firstElementChild.value = '';
    $('ai-telegram-key').replaceChildren(element('option', '', 'Không dùng Telegram'));
    $('ai-telegram-key').firstElementChild.value = '';
    $('ai-brief').value = '';
    $('ai-prompt').value = '';
    $('upload-html').value = '';
    clearFiles();
  }

  function updateFeatureSites() {
    if (!state.user) return false;
    const sites = ownSites();
    const previous = state.keysSiteId;
    const previousAI = $('ai-site').value;
    const makeOption = site => { const option = element('option', '', site.name); option.value = site.id; return option; };
    $('keys-site').replaceChildren(...sites.map(makeOption));
    state.keysSiteId = sites.some(site => site.id === previous) ? previous : sites[0]?.id || '';
    $('keys-site').value = state.keysSiteId;
    const blank = element('option', '', 'Tạo website mới');
    blank.value = '';
    $('ai-site').replaceChildren(blank, ...sites.map(makeOption));
    $('ai-site').value = sites.some(site => site.id === previousAI) ? previousAI : '';
    if (previousAI !== $('ai-site').value) { state.aiKeys = []; state.aiRequest += 1; updateAIKeyOptions(); }
    $('keys-empty-sites').hidden = sites.length > 0;
    $('keys-workspace').hidden = sites.length === 0;
    const site = sites.find(item => item.id === state.keysSiteId);
    $('keys-site-url').textContent = site?.url || '';
    const rates = telegramLimits();
    $('telegram-rates').textContent = `Giới hạn gửi tin: ${rates.ip_per_minute}/phút mỗi IP trên cùng website · ${rates.site_per_minute}/phút mỗi website · ${rates.global_per_minute}/phút toàn hệ thống. Tối đa ${rates.text_max_chars} ký tự/tin.`;
    $('key-value-hint').textContent = `Tối đa ${bytes(keyBytesLimit())}. Không hiển thị lại sau khi lưu; luôn nhập giá trị mới khi thay khóa.`;
    if (previous !== state.keysSiteId) {
      state.keys = [];
      state.keysRequest += 1;
      resetKeyForm();
      renderKeys();
    }
    updateAIPrompt();
    return previous !== state.keysSiteId;
  }

  function metadataKey(key) {
    return { name: String(key.name || ''), kind: key.kind, chat_id: String(key.chat_id || ''), created_at: key.created_at, updated_at: key.updated_at, endpoint: String(key.endpoint || '') };
  }

  function telegramEndpoint(key) {
    return key?.kind === 'telegram' && /^[A-Z][A-Z0-9_]{0,63}$/.test(key.name) && key.endpoint === `/__guiweb/telegram/${key.name}` ? key.endpoint : '';
  }

  function keyFormKind() {
    const telegram = $('key-kind').value === 'telegram';
    $('key-chat-field').hidden = !telegram;
    $('key-chat-id').required = telegram;
    $('key-chat-id').disabled = !telegram || state.keyBusy || state.keysLoading || !state.keysSiteId;
    $('key-kind-hint').textContent = telegram ? 'Gửi tin nhắn tới một chat cố định, không lộ bot token.' : 'Chỉ lưu giữ khóa trên máy chủ. Hiện chưa có endpoint gọi các dịch vụ khác.';
  }

  function setKeyControls() {
    $('key-form').querySelectorAll('input,select,button').forEach(control => { control.disabled = Boolean(state.keyBusy || state.keysLoading || !state.keysSiteId); });
    $('keys-site').disabled = Boolean(state.keyBusy);
    $('key-form').setAttribute('aria-busy', String(Boolean(state.keyBusy || state.keysLoading)));
    keyFormKind();
  }

  function resetKeyForm() {
    $('key-form').reset();
    $('key-value').value = '';
    showError('key-form-error');
    keyFormKind();
  }

  async function loadSiteKeys() {
    const siteId = state.keysSiteId;
    const actor = state.user?.id;
    if (!siteId || !ownSites().some(site => site.id === siteId)) return;
    const request = ++state.keysRequest;
    $('key-value').value = '';
    state.keysLoading = true;
    $('keys-loading').hidden = false;
    showError('keys-error');
    setKeyControls();
    try {
      const data = await api(`/api/sites/${encodeURIComponent(siteId)}/keys`);
      if (request !== state.keysRequest || state.user?.id !== actor || state.keysSiteId !== siteId) return;
      state.keys = Array.isArray(data.keys) ? data.keys.map(metadataKey) : [];
      renderKeys();
    } catch (error) {
      if (request === state.keysRequest && error.status !== 401) showError('keys-error', error.message);
    } finally {
      if (request === state.keysRequest) { state.keysLoading = false; $('keys-loading').hidden = true; setKeyControls(); }
    }
  }

  function telegramSnippet(key) {
    const endpoint = telegramEndpoint(key);
    return `async function guiwebSendMessage(text) {\n  const response = await fetch(${JSON.stringify(endpoint)}, {\n    method: "POST",\n    headers: { "Content-Type": "application/json" },\n    body: JSON.stringify({ text: text })\n  });\n  if (!response.ok) {\n    const errors = {\n      400: "Nội dung chưa hợp lệ.",\n      404: "Website hoặc khóa không còn hoạt động.",\n      429: "Gửi quá nhanh. Hãy chờ rồi thử lại.",\n      502: "Telegram tạm thời không nhận được tin."\n    };\n    throw new Error(errors[response.status] || "Chưa gửi được tin.");\n  }\n}\n// Trong sự kiện submit (async), gọi:\n// await guiwebSendMessage("Có lời nhắn mới từ website");`;
  }

  function renderKeys() {
    $('key-count').textContent = `${state.keys.length}/${keyLimit()}`;
    $('keys-empty').hidden = state.keys.length > 0;
    $('keys-list').replaceChildren(...state.keys.map(key => {
      const row = element('article', 'key-item');
      const header = element('div', 'key-item-header');
      const detail = element('div');
      detail.append(element('h4', 'mono', key.name), element('span', 'field-hint', key.kind === 'telegram' ? `Telegram · Chat ${key.chat_id}` : 'Chỉ lưu giữ · Chưa có proxy dịch vụ'));
      const actions = element('div', 'key-actions');
      const edit = element('button', 'button button-small button-quiet', 'Thay khóa');
      edit.type = 'button'; edit.disabled = state.keyBusy;
      edit.setAttribute('aria-label', `Thay khóa ${key.name}`);
      edit.addEventListener('click', () => {
        resetKeyForm();
        $('key-name').value = key.name;
        $('key-kind').value = key.kind === 'telegram' ? 'telegram' : 'generic';
        $('key-chat-id').value = key.chat_id;
        keyFormKind();
        $('key-value').focus();
      });
      const revoke = element('button', 'button button-small button-quiet', 'Thu hồi');
      revoke.type = 'button'; revoke.disabled = state.keyBusy;
      revoke.setAttribute('aria-label', `Thu hồi khóa ${key.name}`);
      revoke.addEventListener('click', () => {
        $('key-value').value = '';
        state.deleteKey = { siteId: state.keysSiteId, name: key.name };
        $('key-delete-name').textContent = key.name;
        setDialogBusy($('key-delete-dialog'), false);
        showError('key-delete-error');
        showDialog($('key-delete-dialog'));
      });
      actions.append(edit, revoke); header.append(detail, actions); row.append(header);
      row.append(element('span', 'field-hint', `Cập nhật ${dateTime(key.updated_at)}`));
      if (telegramEndpoint(key)) {
        row.append(element('code', 'key-endpoint', key.endpoint));
        const snippet = element('details', 'key-snippet');
        snippet.append(element('summary', '', 'Mã gửi tin từ website này'));
        const pre = element('pre', 'code-snippet'); pre.append(element('code', '', telegramSnippet(key)));
        const copy = element('button', 'button button-small button-quiet', 'Sao chép mã gửi tin'); copy.type = 'button';
        copy.addEventListener('click', () => copyText(telegramSnippet(key), copy, 'Đã sao chép mã gửi tin. Chạy mã ở website đã chọn.'));
        snippet.append(pre, copy, element('p', 'field-hint', 'Đường dẫn tương đối gọi đúng website đang mở. Mã không chứa bot token hoặc chat ID.'));
        row.append(snippet);
      }
      return row;
    }));
  }

  $('keys-site').addEventListener('change', () => {
    state.keysSiteId = $('keys-site').value;
    state.keys = [];
    resetKeyForm();
    renderKeys();
    const site = ownSites().find(item => item.id === state.keysSiteId);
    $('keys-site-url').textContent = site?.url || '';
    loadSiteKeys();
  });
  $('key-kind').addEventListener('change', () => { $('key-value').value = ''; $('key-chat-id').value = ''; keyFormKind(); });
  $('key-name').addEventListener('input', () => { $('key-name').value = $('key-name').value.toUpperCase(); });
  $('key-form').addEventListener('submit', async event => {
    event.preventDefault();
    if (state.keyBusy || state.keysLoading || !ownSites().some(site => site.id === state.keysSiteId)) return;
    showError('key-form-error');
    const name = $('key-name').value.trim();
    const length = new TextEncoder().encode($('key-value').value).byteLength;
    if (!/^[A-Z][A-Z0-9_]{0,63}$/.test(name)) { showError('key-form-error', 'Tên khóa cần bắt đầu bằng chữ in hoa, chỉ gồm chữ in hoa, số và dấu _.'); return; }
    if (length < 1 || length > keyBytesLimit()) { showError('key-form-error', `Giá trị khóa cần từ 1 B đến ${bytes(keyBytesLimit())}.`); return; }
    if (state.keys.length >= keyLimit() && !state.keys.some(key => key.name === name)) { showError('key-form-error', `Website đã có ${keyLimit()} khóa. Hãy thay khóa cùng tên hoặc thu hồi một khóa cũ.`); return; }
    const actor = state.user.id;
    const siteId = state.keysSiteId;
    const mutation = state.keyMutation = (state.keyMutation || 0) + 1;
    const body = { name, kind: $('key-kind').value, value: $('key-value').value, chat_id: $('key-kind').value === 'telegram' ? $('key-chat-id').value.trim() : '' };
    $('key-value').value = '';
    state.keyBusy = true; setKeyControls(); renderKeys();
    try {
      await api(`/api/sites/${encodeURIComponent(siteId)}/keys`, { method: 'POST', body });
      if (state.user?.id !== actor || mutation !== state.keyMutation) return;
      resetKeyForm();
      await loadSiteKeys();
      notify('Đã lưu khóa. Giá trị khóa sẽ không hiển thị lại.');
    } catch (error) { if (state.user?.id === actor && mutation === state.keyMutation && error.status !== 401) showError('key-form-error', error.message); }
    finally {
      body.value = '';
      if (mutation === state.keyMutation) { $('key-value').value = ''; state.keyBusy = false; setKeyControls(); renderKeys(); }
    }
  });
  $('key-delete-submit').addEventListener('click', async () => {
    const item = state.deleteKey;
    if (!item || !ownSites().some(site => site.id === item.siteId)) return;
    const actor = state.user.id;
    const mutation = state.keyDeleteMutation = (state.keyDeleteMutation || 0) + 1;
    setDialogBusy($('key-delete-dialog'), true);
    showError('key-delete-error');
    try {
      await api(`/api/sites/${encodeURIComponent(item.siteId)}/keys/${encodeURIComponent(item.name)}`, { method: 'DELETE' });
      if (state.user?.id !== actor || mutation !== state.keyDeleteMutation) return;
      if (state.keysSiteId === item.siteId) await loadSiteKeys();
      $('key-delete-dialog').close();
      if (state.user) notify('Đã thu hồi khóa.');
    } catch (error) { if (mutation === state.keyDeleteMutation && error.status !== 401) showError('key-delete-error', error.message); }
    finally { if (mutation === state.keyDeleteMutation) setDialogBusy($('key-delete-dialog'), false); }
  });

  async function loadAIKeys() {
    const siteId = $('ai-site').value;
    const actor = state.user?.id;
    const request = ++state.aiRequest;
    state.aiKeys = [];
    updateAIKeyOptions();
    showError('ai-error');
    if (!siteId || !ownSites().some(site => site.id === siteId)) return;
    $('ai-telegram-hint').textContent = 'Đang đọc danh sách khóa Telegram…';
    try {
      const data = await api(`/api/sites/${encodeURIComponent(siteId)}/keys`);
      if (request !== state.aiRequest || state.user?.id !== actor || $('ai-site').value !== siteId) return;
      state.aiKeys = Array.isArray(data.keys) ? data.keys.map(metadataKey).filter(key => telegramEndpoint(key)) : [];
      updateAIKeyOptions();
    } catch (error) { if (request === state.aiRequest && error.status !== 401) { showError('ai-error', error.message); $('ai-telegram-hint').textContent = 'Chưa đọc được khóa Telegram. Bạn vẫn có thể tạo prompt website tĩnh.'; } }
  }

  function updateAIKeyOptions() {
    const previous = $('ai-telegram-key').value;
    const blank = element('option', '', 'Không dùng Telegram'); blank.value = '';
    const options = state.aiKeys.map(key => { const option = element('option', '', key.name); option.value = key.name; return option; });
    $('ai-telegram-key').replaceChildren(blank, ...options);
    $('ai-telegram-key').value = state.aiKeys.some(key => key.name === previous) ? previous : '';
    $('ai-telegram-key').disabled = !state.aiKeys.length;
    $('ai-telegram-hint').textContent = state.aiKeys.length ? 'Chỉ tên khóa và endpoint được đưa vào prompt; không có giá trị khóa hoặc chat ID.' : $('ai-site').value ? 'Website chưa có khóa Telegram. Thêm ở mục Khóa API nếu cần gửi tin.' : 'Muốn dùng Telegram, hãy chọn website có khóa Telegram đã lưu.';
    updateAIPrompt();
  }

  function updateAIPrompt() {
    if (!state.user) return;
    const site = ownSites().find(item => item.id === $('ai-site').value);
    const key = state.aiKeys.find(item => item.name === $('ai-telegram-key').value);
    const endpoint = site && key && telegramEndpoint(key);
    const brief = $('ai-brief').value.trim();
    const capabilities = state.capabilities;
    const limits = [];
    if (state.limits.upload_bytes) limits.push(`mỗi lượt tải lên tối đa ${bytes(state.limits.upload_bytes)}`);
    if (state.limits.site_bytes) limits.push(`website sau giải nén tối đa ${bytes(state.limits.site_bytes)}`);
    if (state.limits.max_files) limits.push(`tối đa ${state.limits.max_files} file/website`);
    if (state.limits.user_bytes && !state.user.admin) limits.push(`hạn mức lưu trữ của chủ website ${bytes(state.limits.user_bytes)}`);
    if (state.limits.total_bytes) limits.push(`dung lượng chung toàn hệ thống ${bytes(state.limits.total_bytes)}`);
    const rates = telegramLimits();
    const prompt = [
      'Hãy tạo một website hoàn chỉnh bằng tiếng Việt để triển khai trên GửiWeb.',
      '', 'Ý tưởng của tôi:', brief || '[Mô tả nội dung, giao diện và tương tác mong muốn ở đây.]', '',
      'Môi trường triển khai:',
      '- Website tĩnh HTML/CSS/JavaScript; JavaScript chạy trong trình duyệt. React/Vue phải được build trước khi tải lên.',
      '- Không có Node.js, PHP, backend tùy ý, cơ sở dữ liệu hoặc API lưu dữ liệu dùng chung cho website.',
      '- Có thể dùng localStorage cho dữ liệu riêng trên từng trình duyệt; xử lý trường hợp trình duyệt chặn lưu. Không coi đó là dữ liệu dùng chung.',
      capabilities.base_domain ? `- Mỗi website có hostname riêng dưới ${capabilities.base_domain}.` : '- Mỗi website có hostname riêng do GửiWeb cấp.',
      capabilities.admin_host ? `- ${capabilities.admin_host} là trang quản lý, không phải endpoint API công khai cho website.` : '- Không gọi API quản lý của GửiWeb từ website công khai.',
      site ? `- Website đã chọn: ${site.name}. Địa chỉ hiện tại: ${site.url}. Nội dung mới cần giữ nguyên địa chỉ này.` : '- Đây là website mới; GửiWeb sẽ cấp địa chỉ sau khi tải lên.',
      ...(limits.length ? [`- Giới hạn hiện tại: ${limits.join('; ')}.`] : []), '',
      'Yêu cầu bàn giao:',
      '- Ưu tiên một file index.html đầy đủ, có CSS trong <style> và JavaScript trong <script>, để tôi sao chép toàn bộ rồi chọn “Dán HTML” trong GửiWeb.',
      '- Nếu cần nhiều file, cung cấp cấu trúc thư mục đầy đủ với index.html ở gốc và đường dẫn tài nguyên tương đối; tôi sẽ tải ZIP hoặc chọn cả thư mục. Không tự chạy lệnh build trên GửiWeb.',
      '- Giao diện responsive, nhãn biểu mẫu rõ ràng, dùng được bàn phím, trạng thái tải/thành công/lỗi dễ hiểu.',
      '- Không đưa API key, bot token, mật khẩu hoặc bí mật vào HTML, JavaScript, localStorage hay nội dung bàn giao.',
      '- Kho Khóa API của GửiWeb hiện chỉ có proxy Telegram như mô tả bên dưới. Khóa loại khác chỉ được lưu giữ, không có proxy tự động cho mọi nhà cung cấp.', '',
      ...(endpoint ? [
        `Telegram đã cấu hình cho website này: tên khóa ${key.name}, endpoint ${endpoint}.`,
        '- Chỉ gọi endpoint này bằng fetch với đường dẫn tương đối trên chính hostname website đã chọn. Không gọi từ hostname quản lý; không tự tạo endpoint khác.',
        `- POST ${endpoint}; header Content-Type: application/json; body chỉ có {"text":"nội dung tin nhắn"}. Không gửi bot token, chat_id hay URL đích. Chat nhận đã cố định trên máy chủ.`,
        `- Endpoint công khai: mọi khách truy cập có thể gửi tới chat đã cấu hình. Không dùng để truyền dữ liệu riêng tư. Tối đa ${rates.text_max_chars} ký tự/tin; ${rates.ip_per_minute} tin/phút mỗi IP trên cùng website, ${rates.site_per_minute} tin/phút mỗi website và ${rates.global_per_minute} tin/phút toàn hệ thống.`,
        '- Kiểm tra response.ok và xử lý 400 (nội dung không hợp lệ), 404 (website/khóa không tồn tại hoặc đã thu hồi), 429 (gửi quá nhanh, chờ rồi thử lại), 502 (Telegram tạm lỗi). Không tự lặp gửi khi chưa biết kết quả.',
      ] : ['Chưa chọn khóa Telegram cho website này. Không bịa endpoint hoặc tích hợp dịch vụ cần khóa bí mật; nếu ý tưởng cần Telegram, giải thích bước lưu khóa và chọn đúng endpoint trước khi dùng.']),
      '', 'Trả về mã nguồn hoàn chỉnh, hướng dẫn ngắn cách sao chép HTML về GửiWeb để xuất bản, và chỉ rõ các giới hạn còn lại.',
    ];
    $('ai-prompt').value = prompt.join('\n');
    $('ai-copy-button').disabled = !brief;
  }

  $('ai-site').addEventListener('change', loadAIKeys);
  $('ai-telegram-key').addEventListener('change', updateAIPrompt);
  $('ai-brief').addEventListener('input', updateAIPrompt);
  $('ai-regenerate-button').addEventListener('click', () => { loadAIKeys(); updateAIPrompt(); notify('Đã tạo lại prompt theo mô tả và giới hạn hiện tại.'); });
  $('ai-copy-button').addEventListener('click', () => copyText($('ai-prompt').value, $('ai-copy-button'), 'Đã sao chép prompt. Dán vào công cụ AI bạn đang dùng.'));
  $('keys-create-site-button').addEventListener('click', () => openUpload());
  $('ai-paste-button').addEventListener('click', () => {
    const site = ownSites().find(item => item.id === $('ai-site').value);
    openUpload(site || null);
    document.querySelector('input[name="upload-source"][value="html"]').checked = true;
    renderHTMLSource();
    $('upload-html').focus();
  });

  async function initialize() {
    try {
      const data = await api('/api/me', { allowUnauthorized: true });
      applySession(data);
      showWorkspace();
      await refreshSites();
      if (inviteToken) notify('Bạn đang đăng nhập. Đăng xuất nếu muốn tạo tài khoản từ lời mời này.');
    } catch (error) {
      showAuth();
      if (error.status !== 401) showError('auth-error', error.message);
    }
  }
  initialize();
})();
