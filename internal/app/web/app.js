'use strict';

(() => {
  const $ = id => document.getElementById(id);
  const state = { user: null, csrf: '', limits: {}, usage: 0, sites: [], invites: [], tab: 'sites', selectedFiles: [], uploadMode: '', uploading: false, uploadSite: null, editSite: null, deleteSite: null };
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

  async function copyText(text, button) {
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
      notify('Đã sao chép link. Sẵn sàng để chia sẻ!');
    } catch {
      notify('Không sao chép được tự động. Bạn có thể chọn và sao chép địa chỉ.', true);
    } finally { button?.focus(); }
  }

  function switchTab(tab) {
    if (tab === 'invites' && !state.user?.admin) return;
    state.tab = tab;
    for (const name of ['sites', 'invites']) {
      const active = name === tab;
      $(`${name}-tab`).classList.toggle('active', active);
      $(`${name}-tab`).setAttribute('aria-selected', String(active));
      $(`${name}-tab`).tabIndex = active ? 0 : -1;
      $(`${name}-panel`).hidden = !active;
    }
    if (tab === 'invites') refreshInvites();
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
    dialog.querySelectorAll('input,button').forEach(control => { control.disabled = busy; });
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
    $('upload-submit').disabled = files.length === 0 || state.uploading;
    $('upload-limit-hint').textContent = `Tối đa ${bytes(state.limits.upload_bytes || 50 * 1024 ** 2)}/lượt. ${mode === 'html' ? 'File HTML sẽ là trang chính. Các tài nguyên đi kèm cần tải cùng bằng ZIP hoặc thư mục.' : `ZIP, thư mục hoặc nhóm file cần có index.html. Website tối đa ${bytes(state.limits.site_bytes || 100 * 1024 ** 2)}.`}`;
    $('file-selection-type').textContent = '';
    if (!files.length) return;
    $('file-selection-type').textContent = `Đã nhận dạng: ${mode === 'zip' ? 'File ZIP' : mode === 'html' ? 'File HTML' : files.some(file => file.webkitRelativePath) ? 'Thư mục website' : 'Nhóm file website'}`;
    $('file-selection-name').textContent = mode === 'folder' ? files[0].webkitRelativePath?.split('/')[0] || 'Các file website đã chọn' : files[0].name;
    $('file-selection-size').textContent = `${files.length} file · ${bytes(files.reduce((sum, file) => sum + file.size, 0))}`;
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
    if (!state.selectedFiles.length) { showError('upload-error', 'Bạn chưa chọn nội dung website.'); $('choose-files-button').focus(); return; }
    if (!state.uploadSite && !$('site-name').value.trim()) { showError('upload-error', 'Hãy nhập tên website.'); $('site-name').focus(); return; }
    const mode = state.uploadMode;
    const data = new FormData();
    data.append('mode', mode);
    if (!state.uploadSite) { data.append('name', $('site-name').value.trim()); data.append('slug', $('site-slug').value.trim()); data.append('spa', String($('site-spa').checked)); }
    for (const file of state.selectedFiles) data.append('files', file, mode === 'folder' ? file.webkitRelativePath || file.name : file.name);
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
  $('invites-tab').addEventListener('click', () => switchTab('invites'));
  document.querySelector('.tabs').addEventListener('keydown', event => {
    if (!state.user?.admin || !['ArrowLeft', 'ArrowRight', 'Home', 'End'].includes(event.key)) return;
    event.preventDefault();
    const tab = event.key === 'Home' ? 'sites' : event.key === 'End' ? 'invites' : state.tab === 'sites' ? 'invites' : 'sites';
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
