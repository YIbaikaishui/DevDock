/* DevDock 前端逻辑 v0.3.0（界面重构）
   零依赖、零构建。状态变更走 REST，实时推送走一个全局 SSE 连接。
   红线：禁止拼接 HTML 字符串，一律 el() + textContent。 */
'use strict';

(function () {
  // ---------- 令牌处理 ----------
  // 服务启动时把 token 放在 URL 的 ?t= 里。立刻存进 sessionStorage 并从地址栏抹掉，
  // 避免令牌留在浏览器历史或被截图泄露。
  const params = new URLSearchParams(location.search);
  const fromUrl = params.get('t');
  if (fromUrl) {
    sessionStorage.setItem('devdock_token', fromUrl);
    params.delete('t');
    const qs = params.toString();
    history.replaceState(null, '', location.pathname + (qs ? '?' + qs : ''));
  }
  const TOKEN = sessionStorage.getItem('devdock_token') || '';

  // ---------- 状态 ----------
  const S = {
    projects: [],
    states: {},            // id -> state
    selected: null,        // 当前打开的日志/详情抽屉对应的项目
    detailID: null,        // 详情抽屉对应的项目
    search: '',
    logsById: Object.create(null),   // id -> 日志行数组（按项目缓存，非选中项目也收）
    lastSeqById: Object.create(null), // id -> 已收最大 seq
    follow: true,
    gbk: false,
    filter: '',
    onlyErr: false,
    showTs: true,
    es: null,
    editID: null,
    live: false,
    scan: { controller: null, rows: [], ignored: new Set() },
    pending: new Set(),
    dialogResolve: null,
    lastFocus: null,
    openMenu: null,
  };

  const MAX_CLIENT_LINES = 3000;

  // ---------- 工具 ----------
  const $ = (sel) => document.querySelector(sel);

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text != null) n.textContent = text;
    return n;
  }

  // 表单控件取值：form.elements.namedItem 不受 form.id / form.name 等内建属性影响。
  function field(form, name) {
    return form.elements.namedItem(name);
  }

  // ---------- 图标（内联 SVG，不新增静态文件） ----------
  const SVG_NS = 'http://www.w3.org/2000/svg';
  const ICONS = {
    play: ['M8 5.5v13l11-6.5-11-6.5z'],
    stop: ['M7.5 7.5h9v9h-9z'],
    restart: ['M4.5 12a7.5 7.5 0 1 0 2.2-5.3', 'M4.5 4.5v4h4'],
    logs: ['M6 3.5h8l4 4v13H6z', 'M9.5 12h5', 'M9.5 16h5'],
    close: ['M6 6l12 12', 'M18 6L6 18'],
    external: ['M14 5h5v5', 'M19 5l-8 8', 'M9 5H7a2 2 0 0 0-2 2v10a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2'],
    warn: ['M12 4l9 16H3z', 'M12 10v4', 'M12 17h.01'],
    check: ['M5 13l4 4L19 7'],
    info: ['M12 4a8 8 0 1 0 0 16 8 8 0 0 0 0-16z', 'M12 11v5', 'M12 8h.01'],
    plus: ['M12 5v14', 'M5 12h14'],
    refresh: ['M4 12a8 8 0 0 1 14-5.3', 'M20 12a8 8 0 0 1-14 5.3', 'M18 3v4h-4', 'M6 21v-4h4'],
    more: ['M12 6h.01', 'M12 12h.01', 'M12 18h.01'],
    copy: ['M9 9h11v11H9z', 'M5 15V4h11'],
    folder: ['M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v9a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z'],
    edit: ['M4 20h4L19 9l-4-4L4 16z'],
    trash: ['M4 7h16', 'M9 7V4h6v3', 'M6 7l1 13h10l1-13'],
    jump: ['M12 4v16', 'M6 14l6 6 6-6'],
  };

  function icon(name) {
    const n = document.createElementNS(SVG_NS, 'svg');
    n.setAttribute('viewBox', '0 0 24 24');
    n.setAttribute('fill', 'none');
    n.setAttribute('stroke', 'currentColor');
    n.setAttribute('stroke-width', '1.8');
    n.setAttribute('stroke-linecap', 'round');
    n.setAttribute('stroke-linejoin', 'round');
    n.setAttribute('aria-hidden', 'true');
    for (const d of ICONS[name] || []) {
      const p = document.createElementNS(SVG_NS, 'path');
      p.setAttribute('d', d);
      n.appendChild(p);
    }
    return n;
  }

  // ---------- Toast（最多同时 3 条，3.5s 自动消失） ----------
  function toast(msg, kind) {
    const wrap = $('#toast-wrap');
    while (wrap.childElementCount >= 3) wrap.removeChild(wrap.firstChild);
    const t = el('div', 'toast' + (kind ? ' ' + kind : ''));
    t.appendChild(icon(kind === 'error' ? 'warn' : kind === 'ok' ? 'check' : 'info'));
    t.appendChild(el('span', null, msg));
    wrap.appendChild(t);
    setTimeout(() => {
      t.classList.add('out');
      setTimeout(() => t.remove(), 200);
    }, 3500);
  }

  async function api(path, opts) {
    opts = opts || {};
    const headers = Object.assign({ 'X-DevDock-Token': TOKEN }, opts.headers || {});
    if (opts.body && !headers['Content-Type']) {
      headers['Content-Type'] = 'application/json';
    }
    let res;
    try {
      res = await fetch(path, Object.assign({}, opts, { headers }));
    } catch (e) {
      throw new Error('无法连接 DevDock 服务，请确认它仍在运行');
    }
    const text = await res.text();
    let data = null;
    if (text) {
      try { data = JSON.parse(text); } catch (e) { /* 非 JSON 响应 */ }
    }
    if (!res.ok) {
      const msg = data && data.error && data.error.message ? data.error.message : ('请求失败（' + res.status + '）');
      throw new Error(msg);
    }
    return data;
  }

  // ---------- 状态文案（pill 只表示主状态，readiness 细节放小字与详情） ----------
  function statusInfo(st) {
    if (!st) return { text: '已停止', cls: 'stopped' };
    switch (st.lifecycle) {
      case 'starting':
        return { text: '启动中', cls: 'pending' };
      case 'stopping':
        return { text: '停止中', cls: 'pending' };
      case 'failed':
        return { text: '失败', cls: 'failed' };
      case 'running':
        if (st.readiness === 'ready') return { text: '就绪', cls: 'ready' };
        if (st.readiness === 'timeout') return { text: '就绪超时', cls: 'failed' };
        return { text: '运行中', cls: 'running' };
      default:
        return { text: '已停止', cls: 'stopped' };
    }
  }

  // 卡片上的 readiness 小字。
  function readinessNote(st) {
    if (!st || st.lifecycle !== 'running') return '';
    switch (st.readiness) {
      case 'ready': return st.readyLatencyMs ? '就绪 ' + st.readyLatencyMs + 'ms' : '已就绪';
      case 'pending': return '等待就绪…';
      case 'timeout': return '就绪超时';
      case 'error': return '就绪探测出错';
      default: return '未配置就绪检查';
    }
  }

  // 详情抽屉里的完整就绪描述。
  function detailReadiness(st) {
    if (!st) return '—';
    if (st.lifecycle === 'running') {
      switch (st.readiness) {
        case 'ready': return (st.readyLatencyMs ? st.readyLatencyMs + ' ms · ' : '') + '已就绪';
        case 'pending': return '等待中…';
        case 'timeout': return '超时（进程仍在运行）';
        case 'error': return '探测出错';
        default: return '未配置就绪检查';
      }
    }
    if (st.lifecycle === 'failed' && st.lastExitCode != null) return '退出码 ' + st.lastExitCode;
    return '—';
  }

  function fmtDuration(fromISO) {
    if (!fromISO) return '—';
    const ms = Date.now() - new Date(fromISO).getTime();
    if (ms < 0) return '—';
    const s = Math.floor(ms / 1000);
    if (s < 60) return s + ' 秒';
    const m = Math.floor(s / 60);
    if (m < 60) return m + ' 分 ' + (s % 60) + ' 秒';
    const h = Math.floor(m / 60);
    return h + ' 小时 ' + (m % 60) + ' 分';
  }

  function fmtTime(iso) {
    if (!iso) return '—';
    const d = new Date(iso);
    if (isNaN(d.getTime())) return '—';
    const p = (n) => String(n).padStart(2, '0');
    return d.getFullYear() + '-' + p(d.getMonth() + 1) + '-' + p(d.getDate()) +
      ' ' + p(d.getHours()) + ':' + p(d.getMinutes());
  }

  function lastSeg(p) {
    const parts = String(p || '').split(/[\\/]/).filter(Boolean);
    return parts.length ? parts[parts.length - 1] : String(p || '');
  }

  // ---------- 渲染：项目卡列表 ----------
  function matchesSearch(p) {
    if (!S.search) return true;
    const q = S.search;
    return (p.name || '').toLowerCase().indexOf(q) >= 0 ||
      (p.command || '').toLowerCase().indexOf(q) >= 0 ||
      (p.cwd || '').toLowerCase().indexOf(q) >= 0;
  }

  function renderList() {
    const ul = $('#project-list');
    ul.textContent = '';
    const visible = S.projects.filter(matchesSearch);
    for (const p of visible) ul.appendChild(projectCard(p));

    const hasProjects = S.projects.length > 0;
    $('#empty-hint').hidden = hasProjects;
    $('#search-empty').hidden = !(hasProjects && visible.length === 0);
    renderLive();
  }

  function renderLive() {
    let running = 0;
    for (const p of S.projects) {
      const st = S.states[p.id];
      if (st && st.lifecycle === 'running') running++;
    }
    $('#summary').textContent = running + '/' + S.projects.length + ' 运行中';
    $('#live').classList.toggle('off', !S.live);
  }

  function projectCard(p) {
    const st = S.states[p.id] || null;
    const info = statusInfo(st);

    const li = el('li', 'project-item');
    li.dataset.id = p.id;
    if (st && st.lifecycle === 'stopped') li.classList.add('is-stopped');

    li.appendChild(el('span', 'dot ' + info.cls));

    const main = el('div', 'p-main');
    const top = el('div', 'p-top');
    const ready = st && st.lifecycle === 'running' && st.readiness === 'ready';
    let name;
    if (ready && p.port > 0) {
      name = el('a', 'p-name', p.name);
      name.href = 'http://127.0.0.1:' + p.port;
      name.target = '_blank';
      name.rel = 'noreferrer';
      name.title = '打开 http://127.0.0.1:' + p.port;
    } else {
      name = el('span', 'p-name', p.name);
    }
    top.appendChild(name);
    top.appendChild(el('span', 'pill ' + info.cls, info.text));
    main.appendChild(top);

    const meta = el('div', 'p-meta');
    meta.appendChild(el('code', 'p-cmd', p.command || '（未填命令）'));
    if (p.port > 0) meta.appendChild(el('span', 'p-port', ':' + p.port));
    if (st && st.lifecycle === 'running' && st.startedAt) {
      meta.appendChild(el('span', 'p-up', '运行 ' + fmtDuration(st.startedAt)));
    }
    const note = readinessNote(st);
    if (note) meta.appendChild(el('span', 'p-note', note));
    main.appendChild(meta);
    li.appendChild(main);

    li.appendChild(cardActions(p, st));
    return li;
  }

  // 主操作随状态变化；次要操作常驻，保证可发现性与键盘可达。
  function cardActions(p, st) {
    const wrap = el('div', 'p-actions');
    const lc = st ? st.lifecycle : 'stopped';
    const ready = lc === 'running' && st.readiness === 'ready';
    const canOpen = ready && p.port > 0;
    const busy = lc === 'starting' || lc === 'stopping';

    const primary = el('button', 'btn primary sm');
    if (lc === 'stopped' || lc === 'failed') {
      primary.textContent = '启动';
      primary.dataset.act = 'start';
    } else if (lc === 'starting' || lc === 'stopping') {
      primary.textContent = lc === 'starting' ? '停止' : '停止中';
      primary.dataset.act = 'stop';
      if (lc === 'stopping') primary.disabled = true;
    } else if (canOpen) {
      primary.textContent = '打开';
      primary.dataset.act = 'open';
    } else {
      primary.textContent = '停止';
      primary.dataset.act = 'stop';
    }
    wrap.appendChild(primary);

    if (lc === 'running' && canOpen) {
      const stop = el('button', 'btn sm', '停止');
      stop.dataset.act = 'stop';
      wrap.appendChild(stop);
    }
    if (lc === 'running' || lc === 'starting' || lc === 'failed') {
      const rs = el('button', 'btn sm', '重启');
      rs.dataset.act = 'restart';
      if (busy) rs.disabled = true;
      wrap.appendChild(rs);
    }
    const logs = el('button', 'btn sm', '日志');
    logs.dataset.act = 'logs';
    wrap.appendChild(logs);
    const detail = el('button', 'btn sm', '详情');
    detail.dataset.act = 'detail';
    wrap.appendChild(detail);

    const more = el('button', 'btn icon sm');
    more.dataset.act = 'more';
    more.setAttribute('aria-label', '更多');
    more.appendChild(icon('more'));
    wrap.appendChild(more);

    const menu = el('div', 'menu');
    menu.hidden = true;
    const edit = el('button', 'btn sm', '编辑');
    edit.dataset.act = 'edit';
    const folder = el('button', 'btn sm', '打开目录');
    folder.dataset.act = 'folder';
    const del = el('button', 'btn sm danger', '删除');
    del.dataset.act = 'delete';
    menu.appendChild(edit);
    menu.appendChild(folder);
    menu.appendChild(del);
    wrap.appendChild(menu);

    return wrap;
  }

  function toggleMenu(btn) {
    const actions = btn.closest('.p-actions');
    const menu = actions && actions.querySelector('.menu');
    if (!menu) return;
    const willOpen = menu.hidden;
    closeMenu();
    if (willOpen) {
      menu.hidden = false;
      S.openMenu = menu;
    }
  }

  function closeMenu() {
    if (S.openMenu) {
      S.openMenu.hidden = true;
      S.openMenu = null;
    }
  }

  function currentByID(id) {
    return S.projects.find((p) => p.id === id) || null;
  }

  // ---------- 动作 ----------
  async function doAction(action, id, btn, okMsg) {
    if (!id) return;
    setPending(btn, true);
    try {
      await api('/api/projects/' + encodeURIComponent(id) + '/' + action, { method: 'POST' });
      if (okMsg) toast(okMsg, 'ok');
      await loadProjects();
    } catch (e) {
      toast(e.message, 'error');
      await loadProjects().catch(() => {});
    } finally {
      setPending(btn, false);
    }
  }

  function setPending(btn, on) {
    if (!btn) return;
    if (on) {
      btn.classList.add('is-loading');
      btn.disabled = true;
      btn.setAttribute('aria-busy', 'true');
      S.pending.add(btn);
    } else {
      btn.classList.remove('is-loading');
      btn.disabled = false;
      btn.removeAttribute('aria-busy');
      S.pending.delete(btn);
    }
  }

  // SSE 状态事件到达后恢复所有 pending 按钮（卡片会整体重建）。
  function clearPending() {
    S.pending.clear();
    renderList();
    if (S.detailID) renderDetail();
  }

  async function openFolder(id) {
    try {
      await api('/api/projects/' + encodeURIComponent(id) + '/open-folder', { method: 'POST' });
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  async function deleteProject(id) {
    try {
      await api('/api/projects/' + encodeURIComponent(id) + '?stop=true', { method: 'DELETE' });
      if (S.selected === id || S.detailID === id) closeAllDrawers();
      if (S.detailID === id) S.detailID = null;
      if (S.selected === id) S.selected = null;
      toast('项目已删除', 'ok');
      await loadProjects();
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  function handleCardAction(act, id, btn) {
    const p = currentByID(id);
    if (!p) return;
    switch (act) {
      case 'start':
        doAction('start', id, btn, '已发出启动指令');
        break;
      case 'stop':
        confirmDialog({
          title: '停止项目',
          desc: '确定要停止「' + p.name + '」吗？该项目的整棵进程树都会被终止。',
          okText: '停止',
        }).then((ok) => { if (ok) doAction('stop', id, btn, '已发出停止指令'); });
        break;
      case 'restart':
        confirmDialog({
          title: '重启项目',
          desc: '确定要重启「' + p.name + '」吗？进程树会被终止后重新启动。',
          okText: '重启',
        }).then((ok) => { if (ok) doAction('restart', id, btn, '已发出重启指令'); });
        break;
      case 'open':
        window.open('http://127.0.0.1:' + p.port, '_blank', 'noreferrer');
        break;
      case 'logs':
        openLogDrawer(id);
        break;
      case 'detail':
        openDetailDrawer(id);
        break;
      case 'edit':
        closeMenu();
        openFormDrawer(p);
        break;
      case 'folder':
        closeMenu();
        openFolder(id);
        break;
      case 'delete':
        // 菜单先别关：关掉后「删除」进了隐藏层，取消时焦点就无处可还。
        confirmDialog({
          title: '删除项目',
          desc: '确定要删除项目「' + p.name + '」吗？此操作不可撤销。',
          okText: '删除',
        }).then((ok) => {
          if (ok) { closeMenu(); deleteProject(id); }
        });
        break;
      case 'more':
        toggleMenu(btn);
        break;
    }
  }

  // ---------- 日志：按项目缓存 ----------
  function logsFor(id) {
    if (!S.logsById[id]) S.logsById[id] = [];
    return S.logsById[id];
  }

  function currentLogs() {
    return S.selected ? (S.logsById[S.selected] || []) : [];
  }

  function decodeMaybeGBK(text) {
    if (!S.gbk) return text;
    try {
      // 服务端按 UTF-8 尽力解码，GBK 字节会变成替换符。这里把字符串重新编码为
      // latin1 字节再按 GBK 解码，做「看起来是乱码」的补救。
      const bytes = new Uint8Array(text.length);
      for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        bytes[i] = c > 255 ? 63 : c;
      }
      return new TextDecoder('gbk', { fatal: false }).decode(bytes);
    } catch (e) {
      return text;
    }
  }

  function lineVisible(line) {
    if (S.onlyErr && line.stream !== 'err') return false;
    if (S.filter) {
      const text = decodeMaybeGBK(line.text) || '';
      if (text.toLowerCase().indexOf(S.filter) < 0) return false;
    }
    return true;
  }

  // 关键词高亮：不拼接 HTML 字符串，用 el('mark', null, kw) 拼节点。
  function appendHighlighted(parent, text, kw) {
    if (!kw) {
      parent.appendChild(document.createTextNode(text));
      return;
    }
    const lower = text.toLowerCase();
    const klower = kw.toLowerCase();
    let i = 0;
    let hit = lower.indexOf(klower);
    while (hit >= 0) {
      if (hit > i) parent.appendChild(document.createTextNode(text.slice(i, hit)));
      parent.appendChild(el('mark', null, text.slice(hit, hit + kw.length)));
      i = hit + kw.length;
      hit = lower.indexOf(klower, i);
    }
    if (i < text.length) parent.appendChild(document.createTextNode(text.slice(i)));
  }

  function buildLogLine(line) {
    const span = el('span', 'ln' + (line.stream === 'err' ? ' stderr' : '') + (line.hist ? ' hist' : ''));
    span.appendChild(el('span', 'ts', (line.ts || '').slice(11, 19)));
    const text = decodeMaybeGBK(line.text) || '';
    if (S.filter) appendHighlighted(span, text, S.filter);
    else span.appendChild(document.createTextNode(text));
    return span;
  }

  function renderLogs() {
    const view = $('#logview');
    const logs = currentLogs();
    view.textContent = '';
    view.classList.toggle('no-ts', !S.showTs);

    let shown = 0;
    for (const line of logs) {
      if (!lineVisible(line)) continue;
      view.appendChild(buildLogLine(line));
      shown++;
    }
    if (shown === 0) {
      const empty = el('div', 'log-empty');
      const st = S.selected ? S.states[S.selected] : null;
      if (S.filter) empty.textContent = '没有匹配「' + S.filter + '」的日志';
      else if (!st || st.lifecycle === 'stopped' || st.lifecycle === 'failed') empty.textContent = '未启动，暂无日志';
      else empty.textContent = '暂无输出';
      view.appendChild(empty);
    }
    $('#log-count').textContent = logs.length + ' 行';
    if (S.follow) view.scrollTop = view.scrollHeight;
  }

  function appendLogLineTo(id, line) {
    const arr = logsFor(id);
    const lastSeq = S.lastSeqById[id] || 0;
    // 服务端重连补发时可能重复，按 seq 去重。
    if (line.seq && line.seq <= lastSeq) return;
    if (line.seq) S.lastSeqById[id] = line.seq;

    arr.push(line);
    if (arr.length > MAX_CLIENT_LINES) arr.splice(0, arr.length - MAX_CLIENT_LINES);

    // 非选中项目只更新缓存，不动 DOM。
    if (id !== S.selected || $('#log-mask').hidden) return;
    if (!lineVisible(line)) {
      $('#log-count').textContent = arr.length + ' 行';
      return;
    }
    const view = $('#logview');
    const empty = view.querySelector('.log-empty');
    if (empty) empty.remove();
    view.appendChild(buildLogLine(line));
    while (view.childElementCount > MAX_CLIENT_LINES) view.removeChild(view.firstChild);
    $('#log-count').textContent = arr.length + ' 行';
    if (S.follow) view.scrollTop = view.scrollHeight;
  }

  async function loadLogs(id) {
    // 缓存非空：直接渲染，不重新请求（解决切走切回断档/重拉）。
    if (S.logsById[id] && S.logsById[id].length > 0) {
      renderLogs();
      return;
    }
    try {
      const data = await api('/api/projects/' + encodeURIComponent(id) + '/logs?tail=500');
      const lines = data.lines || [];
      for (const l of lines) l.hist = true;
      S.logsById[id] = lines.slice(-MAX_CLIENT_LINES);
      let max = 0;
      for (const l of lines) if (l.seq) max = Math.max(max, l.seq);
      S.lastSeqById[id] = max;
      if (data.gap) $('#gapbar').hidden = false;
      renderLogs();
    } catch (e) {
      toast(e.message, 'error');
    }
  }

  function syncFollowUI() {
    $('#chk-follow').checked = S.follow;
    $('#log-follow-hint').hidden = S.follow;
  }

  function openLogDrawer(id) {
    const p = currentByID(id);
    if (!p) return;
    S.selected = id;
    $('#log-title').textContent = p.name;
    $('#log-cmd').textContent = p.command || '—';
    closeAllDrawers();
    $('#log-mask').hidden = false;
    S.lastFocus = document.activeElement;
    syncFollowUI();
    renderLogs();
    $('#logview').scrollTop = $('#logview').scrollHeight;
    $('#btn-close-log').focus();
    renderList();
    loadLogs(id);
  }

  function closeLogDrawer() {
    $('#log-mask').hidden = true;
    restoreFocus();
  }

  // ---------- 详情抽屉 ----------
  function openDetailDrawer(id) {
    const p = currentByID(id);
    if (!p) return;
    S.selected = id;
    closeAllDrawers();
    S.detailID = id;
    $('#detail-mask').hidden = false;
    S.lastFocus = document.activeElement;
    renderDetail();
    $('#btn-close-detail').focus();
    renderList();
  }

  function closeDetailDrawer() {
    $('#detail-mask').hidden = true;
    S.detailID = null;
    restoreFocus();
  }

  function renderDetail() {
    const p = currentByID(S.detailID);
    if (!p) return;
    const st = S.states[p.id] || { lifecycle: 'stopped', readiness: 'none' };
    const info = statusInfo(st);

    $('#d-name').textContent = p.name;
    $('#d-cwd').textContent = p.cwd;
    $('#d-command').textContent = p.command || '—';
    $('#d-port').textContent = p.port > 0 ? String(p.port) : '未声明';

    const pill = $('#d-status');
    pill.textContent = info.text;
    pill.className = 'pill ' + info.cls;

    $('#d-pid').textContent = st.pid ? String(st.pid) : '—';
    $('#d-uptime').textContent = st.lifecycle === 'running' ? fmtDuration(st.startedAt) : '—';
    $('#d-readiness').textContent = detailReadiness(st);
    $('#d-autostart').textContent = p.autostart ? '是' : '否';
    $('#d-created').textContent = fmtTime(p.createdAt);
    $('#d-updated').textContent = fmtTime(p.updatedAt);

    const env = p.env || {};
    const keys = Object.keys(env);
    $('#d-env').textContent = keys.length ? keys.map((k) => k + '=' + env[k]).join('\n') : '（无）';

    const errBox = $('#d-error');
    if (st.lastError) {
      errBox.hidden = false;
      errBox.textContent = st.lastError;
    } else {
      errBox.hidden = true;
    }

    const isRunning = st.lifecycle === 'running' || st.lifecycle === 'starting';
    const isBusy = st.lifecycle === 'starting' || st.lifecycle === 'stopping';
    $('#btn-start').disabled = isRunning || isBusy;
    $('#btn-stop').disabled = !isRunning || isBusy;
    $('#btn-restart').disabled = isBusy;
  }

  // ---------- 抽屉通用 ----------
  function closeAllDrawers() {
    for (const id of ['#log-mask', '#detail-mask', '#scan-mask', '#drawer-mask']) {
      $(id).hidden = true;
    }
    S.detailID = null;
    closeMenu();
  }

  function restoreFocus() {
    let node = S.lastFocus;
    // renderList() 会整表重建，触发按钮可能已被移出文档（焦点因此掉回 body）。
    // 这时按「同一张卡片 + 同一个 data-act」把新按钮找回来。
    if (node && !document.contains(node)) {
      const act = node.dataset ? node.dataset.act : '';
      const card = node.closest ? node.closest('.project-item') : null;
      const id = card ? card.dataset.id : '';
      if (act && id) {
        for (const li of document.querySelectorAll('.project-item')) {
          if (li.dataset.id !== id) continue;
          for (const b of li.querySelectorAll('[data-act]')) {
            if (b.dataset.act === act) { node = b; break; }
          }
          break;
        }
      }
    }
    if (node && document.contains(node)) {
      try { node.focus(); } catch (e) { /* 元素可能已不可聚焦 */ }
    }
    S.lastFocus = null;
  }

  // ---------- 表单抽屉 ----------
  function openFormDrawer(project) {
    closeAllDrawers();
    S.editID = project ? project.id : null;
    $('#drawer-title').textContent = project ? '编辑项目' : '新建项目';
    const f = $('#project-form');
    f.reset();
    $('#form-error').hidden = true;

    if (project) {
      field(f, 'name').value = project.name || '';
      field(f, 'id').value = project.id || '';
      field(f, 'id').disabled = true;              // id 创建后不可改
      field(f, 'cwd').value = project.cwd || '';
      field(f, 'command').value = project.command || '';
      field(f, 'port').value = project.port || '';
      field(f, 'readyPath').value = project.readiness && project.readiness.path ? project.readiness.path : '';
      field(f, 'readyTimeout').value = project.readiness && project.readiness.timeoutSec ? project.readiness.timeoutSec : '';
      field(f, 'autostart').value = project.autostart ? 'true' : 'false';
      const envLines = Object.keys(project.env || {}).map((k) => k + '=' + project.env[k]);
      field(f, 'env').value = envLines.join('\n');
    } else {
      field(f, 'id').disabled = false;
    }

    $('#drawer-mask').hidden = false;
    S.lastFocus = document.activeElement;
    setTimeout(() => field(f, 'name').focus(), 30);
  }

  function closeDrawer() {
    $('#drawer-mask').hidden = true;
    S.editID = null;
    restoreFocus();
  }

  // 返回 { env, bad }：bad 是无效行的行号（1 起）。
  function parseEnv(text) {
    const env = {};
    const bad = [];
    const lines = (text || '').split('\n');
    for (let i = 0; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line || line.startsWith('#')) continue;
      const idx = line.indexOf('=');
      if (idx <= 0) {
        bad.push(i + 1);
        continue;
      }
      env[line.slice(0, idx).trim()] = line.slice(idx + 1);
    }
    return { env, bad };
  }

  // 非法行不再静默丢弃：实时在 #form-error 提示。
  function updateEnvHint() {
    const f = $('#project-form');
    const { bad } = parseEnv(field(f, 'env').value);
    const box = $('#form-error');
    if (bad.length) {
      box.textContent = '第 ' + bad.join('、') + ' 行无效，已忽略';
      box.hidden = false;
    } else {
      box.hidden = true;
    }
  }

  async function submitForm(ev) {
    ev.preventDefault();
    const f = $('#project-form');
    const errBox = $('#form-error');
    errBox.hidden = true;

    const parsed = parseEnv(field(f, 'env').value);
    const body = {
      name: field(f, 'name').value.trim(),
      cwd: field(f, 'cwd').value.trim(),
      command: field(f, 'command').value,
      port: field(f, 'port').value ? parseInt(field(f, 'port').value, 10) : 0,
      autostart: field(f, 'autostart').value === 'true',
      env: parsed.env,
    };

    const readyPath = field(f, 'readyPath').value.trim();
    if (readyPath) {
      body.readiness = {
        path: readyPath,
        timeoutSec: field(f, 'readyTimeout').value ? parseInt(field(f, 'readyTimeout').value, 10) : 30,
      };
    }

    try {
      if (S.editID) {
        await api('/api/projects/' + encodeURIComponent(S.editID), {
          method: 'PATCH',
          body: JSON.stringify(body),
        });
        toast('项目已更新', 'ok');
      } else {
        const id = field(f, 'id').value.trim();
        if (id) body.id = id;
        const created = await api('/api/projects', {
          method: 'POST',
          body: JSON.stringify(body),
        });
        S.selected = created.id;
        toast('项目已创建', 'ok');
      }
      closeDrawer();
      await loadProjects();
      if (parsed.bad.length) {
        toast('第 ' + parsed.bad.join('、') + ' 行环境变量无效，已忽略', 'error');
      }
    } catch (e) {
      errBox.textContent = e.message;
      errBox.hidden = false;
    }
  }

  // ---------- 扫描抽屉 ----------
  // 扫描只读：只产出建议，导入必须由用户显式触发（单条填入表单 / 批量导入）。
  function openScan() {
    closeAllDrawers();
    $('#scan-mask').hidden = false;
    $('#scan-list').textContent = '';
    $('#scan-error').hidden = true;
    $('#scan-summary').textContent = '尚未扫描';
    $('#scan-status').textContent = '';
    $('#scan-roots').value = '';
    const start = $('#scan-start');
    start.disabled = false;
    start.textContent = '开始扫描';
    $('#scan-cancel').hidden = true;
    S.scan.rows = [];
    S.scan.ignored = new Set();
    updateScanImport();
    S.lastFocus = document.activeElement;
    setTimeout(() => $('#scan-roots').focus(), 30);
  }

  function closeScan() {
    if (S.scan.controller) {
      S.scan.controller.abort();
      S.scan.controller = null;
    }
    $('#scan-mask').hidden = true;
    restoreFocus();
  }

  function parseRoots(text) {
    return (text || '').split(/[;\n]+/).map((s) => s.trim()).filter(Boolean);
  }

  async function runScan() {
    const roots = parseRoots($('#scan-roots').value);
    const body = { depth: 3, budgetMs: 2000, limit: 100 };
    if (roots.length) body.roots = roots;
    if ($('#scan-all').checked) body.includeLow = true;

    const controller = new AbortController();
    S.scan.controller = controller;
    const start = $('#scan-start');
    start.disabled = true;
    start.textContent = '扫描中…';
    $('#scan-cancel').hidden = false;
    $('#scan-status').textContent = '扫描中…';
    $('#scan-error').hidden = true;
    $('#scan-list').textContent = '';
    $('#scan-summary').textContent = '';

    let res;
    try {
      res = await api('/api/scan', {
        method: 'POST',
        body: JSON.stringify(body),
        signal: controller.signal,
      });
    } catch (e) {
      if (controller.signal.aborted) {
        $('#scan-status').textContent = '已取消';
      } else {
        $('#scan-status').textContent = '';
        const box = $('#scan-error');
        box.textContent = e.message;
        box.hidden = false;
      }
      start.disabled = false;
      start.textContent = '开始扫描';
      $('#scan-cancel').hidden = true;
      S.scan.controller = null;
      return;
    }
    S.scan.controller = null;
    start.disabled = false;
    start.textContent = '开始扫描';
    $('#scan-cancel').hidden = true;
    $('#scan-status').textContent = '';
    renderScanResults(res);
  }

  function renderScanResults(res) {
    const list = $('#scan-list');
    list.textContent = '';
    const rows = res.suggestions || [];
    S.scan.rows = rows;
    S.scan.ignored = new Set();

    if (rows.length === 0) {
      const empty = el('div', 'scan-empty');
      empty.appendChild(el('p', 'empty-title', '没有发现可识别的项目'));
      empty.appendChild(el('p', 'empty-desc', '可以指定一个根目录再试。'));
      const manual = el('button', 'btn sm', '手动新建');
      manual.addEventListener('click', () => openFormDrawer(null));
      empty.appendChild(manual);
      list.appendChild(empty);
    } else {
      // 按 group 分组展示，monorepo 里同一仓库的多条建议归在一起。
      const groups = new Map();
      for (const s of rows) {
        const g = s.group || '(未分组)';
        if (!groups.has(g)) groups.set(g, []);
        groups.get(g).push(s);
      }
      for (const [g, items] of groups) {
        // 分组标题取路径末段，完整路径放 title（修 U15/V14）。
        const head = el('div', 'scan-group', lastSeg(g));
        head.title = g;
        list.appendChild(head);
        // A8：组内默认只展开 5 条，其余折起来，避免 monorepo 刷屏。
        const collapsed = items.length > 5;
        for (const s of (collapsed ? items.slice(0, 5) : items)) list.appendChild(scanRow(s));
        if (collapsed) {
          const more = el('button', 'btn small ghost scan-more', '还有 ' + (items.length - 5) + ' 条');
          more.addEventListener('click', () => {
            for (const s of items.slice(5)) more.parentNode.insertBefore(scanRow(s), more);
            more.remove();
            updateScanImport();
          });
          list.appendChild(more);
        }
      }
    }

    const parts = [
      '扫描 ' + res.scannedDirs + ' 个目录',
      res.elapsedMs + 'ms',
      '共 ' + rows.length + ' 条',
      '已选 ' + countPicked(),
    ];
    if (res.timedOut) parts.push('已达时间预算');
    if (res.truncated) parts.push('结果已截断');
    const summary = $('#scan-summary');
    summary.textContent = parts.join(' · ');
    summary.classList.toggle('warn', !!(res.timedOut || res.truncated));
    updateScanImport();
  }

  function scanRow(s) {
    const idx = S.scan.rows.indexOf(s);
    const row = el('div', 'scan-row' + (s.duplicate ? ' duplicate' : ''));
    row.dataset.idx = String(idx);

    const pickLabel = el('label', 'scan-check');
    const pick = document.createElement('input');
    pick.type = 'checkbox';
    pick.className = 'scan-pick';
    // A3：默认只勾「非 low，且已知端口 / 已识别框架 / high 置信度」。
    // 既没端口又没框架、又不是 high 的裸脚本不替用户勾——列出来让他自己点，
    // 也不让「一键导入」变成一键导入一堆启动即退出的条目。
    pick.checked = !s.duplicate && !!s.command && s.confidence !== 'low' &&
      (s.port > 0 || !!s.framework || s.confidence === 'high');
    pick.dataset.idx = String(idx);
    pick.addEventListener('click', (e) => e.stopPropagation());
    pickLabel.appendChild(pick);
    row.appendChild(pickLabel);

    const info = el('div', 'scan-info');
    const line1 = el('div', 'scan-line1');
    line1.appendChild(el('span', 'scan-name', s.name));
    if (s.framework) line1.appendChild(el('span', 'scan-badge', s.framework));
    line1.appendChild(el('span', 'scan-badge kind', s.kind));
    if (s.confidence === 'low') line1.appendChild(el('span', 'scan-badge low', 'low'));
    if (s.duplicate) line1.appendChild(el('span', 'scan-badge dup', '已存在'));
    info.appendChild(line1);

    info.appendChild(el('div', 'scan-cmd', s.command || '（判不出启动命令，导入后需手填）'));

    const sub = [lastSeg(s.cwd), s.port > 0 ? ':' + s.port : '端口未推断'];
    if (s.portInUse) sub.push('端口 ' + s.port + ' 当前被占用');
    const subEl = el('div', 'scan-sub', sub.join(' · '));
    subEl.title = s.cwd || '';
    info.appendChild(subEl);

    if (s.warnings && s.warnings.length) {
      info.appendChild(el('div', 'scan-warn', s.warnings.join('；')));
    }
    row.appendChild(info);

    const fill = el('button', 'btn small ghost scan-fill', '填入表单');
    fill.addEventListener('click', () => fillFromSuggestion(s));
    row.appendChild(fill);

    const ignore = el('button', 'btn small ghost scan-ignore', '忽略');
    ignore.addEventListener('click', (e) => {
      e.stopPropagation();
      S.scan.ignored.add(idx);
      row.classList.add('ignored');
      // §6.2 要求忽略态有徽章；撤销时再移除。
      line1.appendChild(el('span', 'scan-badge ignored', '已忽略'));
      pick.checked = false;
      pick.disabled = true;
      updateScanImport();
    });
    row.appendChild(ignore);

    const undo = el('button', 'btn small ghost scan-undo', '撤销');
    undo.addEventListener('click', (e) => {
      e.stopPropagation();
      S.scan.ignored.delete(idx);
      row.classList.remove('ignored');
      const badge = row.querySelector('.scan-badge.ignored');
      if (badge) badge.remove();
      pick.disabled = false;
      updateScanImport();
    });
    row.appendChild(undo);

    return row;
  }

  function isIgnoredRow(cb) {
    const row = cb.closest('.scan-row');
    return !!(row && S.scan.ignored.has(Number(row.dataset.idx)));
  }

  function countPicked() {
    let n = 0;
    for (const cb of document.querySelectorAll('#scan-list .scan-pick:checked')) {
      if (isIgnoredRow(cb)) continue;
      n++;
    }
    return n;
  }

  function updateScanImport() {
    const n = countPicked();
    const btn = $('#scan-import');
    btn.textContent = '导入选中 (' + n + ')';
    btn.disabled = n === 0;
  }

  // 填入表单：不直接创建，用户能在抽屉里改完再走现有 submitForm。
  function fillFromSuggestion(s) {
    closeScan();
    openFormDrawer(null);
    const f = $('#project-form');
    field(f, 'name').value = s.name || '';
    field(f, 'id').value = '';
    field(f, 'cwd').value = s.cwd || '';
    field(f, 'command').value = s.command || '';
    field(f, 'port').value = s.port > 0 ? String(s.port) : '';
    field(f, 'readyPath').value = '';
    field(f, 'readyTimeout').value = '';
    field(f, 'autostart').value = 'false';
    field(f, 'env').value = '';
  }

  // 批量导入：串行 POST，失败的条目保留在列表里、标红并去掉勾选。
  async function importSelected() {
    const picks = Array.from(document.querySelectorAll('#scan-list .scan-pick:checked'))
      .filter((cb) => !isIgnoredRow(cb))
      .map((cb) => S.scan.rows[Number(cb.dataset.idx)])
      .filter(Boolean);
    if (picks.length === 0) return;
    // A3：单批 ≤ 20，且导入前二次确认（自绘弹窗）。
    if (picks.length > 20) {
      toast('单批最多导入 20 条，请减少勾选（当前 ' + picks.length + ' 条）', 'error');
      return;
    }
    const ok = await confirmDialog({
      title: '导入项目',
      desc: '将导入 ' + picks.length + ' 个项目，已存在的项目会被跳过。',
      okText: '导入',
    });
    if (!ok) return;

    const btn = $('#scan-import');
    btn.disabled = true;
    const failed = [];
    let done = 0;
    for (const s of picks) {
      try {
        await api('/api/projects', {
          method: 'POST',
          body: JSON.stringify({
            name: s.name,
            cwd: s.cwd,
            command: s.command,
            port: s.port,
            env: {},
          }),
        });
        done++;
      } catch (e) {
        failed.push({ s: s, msg: s.name + '：' + e.message });
      }
    }

    if (failed.length > 0) {
      const box = $('#scan-error');
      box.textContent = failed.map((f) => f.msg).join('\n');
      box.hidden = false;
      btn.disabled = false;
      // 失败条目保留并标红、去掉勾选，避免被重复导入。
      for (const f of failed) {
        const idx = S.scan.rows.indexOf(f.s);
        const row = document.querySelector('#scan-list .scan-row[data-idx="' + idx + '"]');
        if (row) {
          row.classList.add('failed');
          const cb = row.querySelector('.scan-pick');
          if (cb) cb.checked = false;
        }
      }
      updateScanImport();
      toast('有 ' + failed.length + ' 条导入失败，已保留在列表中', 'error');
      return;
    }

    toast('已导入 ' + done + ' 个项目', 'ok');
    closeScan();
    await loadProjects();
  }

  // ---------- 自绘确认弹窗 ----------
  function confirmDialog(opts) {
    return new Promise((resolve) => {
      S.dialogResolve = resolve;
      S.lastFocus = document.activeElement;
      $('#dialog-title').textContent = opts.title || '';
      $('#dialog-desc').textContent = opts.desc || '';
      $('#dialog-ok').textContent = opts.okText || '确认';
      const dlg = $('#dialog-mask');
      // 自绘弹窗承担 alertdialog 语义（标题 + 描述 + 取消/确认）。
      dlg.querySelector('.dialog').setAttribute('role', 'alertdialog');
      dlg.hidden = false;
      $('#dialog-cancel').focus();
    });
  }

  function closeDialog(result) {
    if ($('#dialog-mask').hidden) return;
    $('#dialog-mask').hidden = true;
    const r = S.dialogResolve;
    S.dialogResolve = null;
    if (r) r(result);
    restoreFocus();
  }

  // ---------- 数据加载 ----------
  async function loadProjects() {
    const data = await api('/api/projects');
    S.projects = data.projects || [];
    for (const v of S.projects) {
      S.states[v.id] = v.state;
    }
    if (S.detailID && !currentByID(S.detailID)) {
      $('#detail-mask').hidden = true;
      S.detailID = null;
    }
    renderList();
    if (S.detailID) renderDetail();
  }

  // ---------- SSE ----------
  function connectSSE() {
    if (S.es) S.es.close();
    const url = '/api/events?t=' + encodeURIComponent(TOKEN);
    const es = new EventSource(url);
    S.es = es;

    es.addEventListener('state', (ev) => {
      let st;
      try { st = JSON.parse(ev.data); } catch (e) { return; }
      if (!st || !st.id) return;
      S.states[st.id] = st;
      // 列表里的项目定义可能带旧的 state，同步一下。
      const p = S.projects.find((x) => x.id === st.id);
      if (p) p.state = st;
      clearPending();
      if (S.detailID === st.id) renderDetail();
    });

    es.addEventListener('log', (ev) => {
      let line;
      try { line = JSON.parse(ev.data); } catch (e) { return; }
      // 全局流会收到所有项目的日志；按项目分发到各自缓存，不再丢弃非选中项目。
      const pid = line && line.id;
      if (!pid) return;
      appendLogLineTo(pid, line);
    });

    es.addEventListener('ready', () => {
      // 状态事件会带 readiness，无需额外处理。
    });

    es.addEventListener('gap', (ev) => {
      let id = '';
      try {
        const d = JSON.parse(ev.data);
        id = d && d.id ? d.id : '';
      } catch (e) { /* 忽略 */ }
      if (!id || id === S.selected) $('#gapbar').hidden = false;
    });

    es.onopen = () => {
      // EventSource 自带重连，连接恢复后把 Live 点变回绿色。
      S.live = true;
      renderLive();
    };

    es.onerror = () => {
      // EventSource 自带重连，这里只更新汇总提示。
      S.live = false;
      $('#live').classList.add('off');
      $('#summary').textContent = '连接中断，正在重连…';
    };
  }

  // ---------- 计时器：刷新运行时长 ----------
  function startUptimeTicker() {
    setInterval(() => {
      for (const li of document.querySelectorAll('#project-list .project-item')) {
        const st = S.states[li.dataset.id];
        const up = li.querySelector('.p-up');
        if (up && st && st.lifecycle === 'running' && st.startedAt) {
          up.textContent = '运行 ' + fmtDuration(st.startedAt);
        }
      }
      if (S.detailID) {
        const st = S.states[S.detailID];
        const elUp = $('#d-uptime');
        if (elUp && st && st.lifecycle === 'running') {
          elUp.textContent = fmtDuration(st.startedAt);
        }
      }
    }, 1000);
  }

  // ---------- 事件绑定 ----------
  function bind() {
    $('#btn-refresh').addEventListener('click', () => {
      loadProjects().then(() => toast('已刷新')).catch((e) => toast(e.message, 'error'));
    });
    $('#btn-new').addEventListener('click', () => openFormDrawer(null));
    $('#btn-scan').addEventListener('click', openScan);
    $('#btn-empty-new').addEventListener('click', () => openFormDrawer(null));
    $('#btn-empty-scan').addEventListener('click', openScan);
    $('#btn-clear-search').addEventListener('click', () => {
      $('#search').value = '';
      S.search = '';
      renderList();
      $('#search').focus();
    });
    $('#search').addEventListener('input', (e) => {
      S.search = e.target.value.trim().toLowerCase();
      renderList();
    });

    // 表单抽屉
    $('#btn-close-drawer').addEventListener('click', closeDrawer);
    $('#btn-cancel').addEventListener('click', closeDrawer);
    $('#project-form').addEventListener('submit', submitForm);
    field($('#project-form'), 'env').addEventListener('input', updateEnvHint);

    // 日志抽屉
    $('#btn-close-log').addEventListener('click', closeLogDrawer);
    $('#btn-close-detail').addEventListener('click', closeDetailDrawer);
    $('#btn-close-scan').addEventListener('click', closeScan);

    // 卡片操作（事件委托）
    $('#project-list').addEventListener('click', (e) => {
      const b = e.target.closest('[data-act]');
      if (!b) return;
      const li = b.closest('.project-item');
      if (!li) return;
      handleCardAction(b.dataset.act, li.dataset.id, b);
    });
    document.addEventListener('click', (e) => {
      if (!S.openMenu) return;
      if (S.openMenu.contains(e.target)) return;
      // 「更多」按钮自身由卡片委托处理器负责开关，这里不提前关掉。
      if (e.target.closest && e.target.closest('[data-act="more"]')) return;
      closeMenu();
    });

    // 日志工具条
    $('#log-filter').addEventListener('input', (e) => {
      S.filter = e.target.value.trim().toLowerCase();
      renderLogs();
    });
    $('#chk-stderr').addEventListener('change', (e) => {
      S.onlyErr = e.target.checked;
      renderLogs();
    });
    $('#chk-ts').addEventListener('change', (e) => {
      S.showTs = e.target.checked;
      renderLogs();
    });
    $('#chk-gbk').addEventListener('change', (e) => {
      S.gbk = e.target.checked;
      renderLogs();
    });
    $('#chk-follow').addEventListener('change', (e) => {
      S.follow = e.target.checked;
      if (S.follow) {
        const v = $('#logview');
        v.scrollTop = v.scrollHeight;
      }
      syncFollowUI();
    });
    $('#btn-jump').addEventListener('click', () => {
      const v = $('#logview');
      v.scrollTop = v.scrollHeight;
      S.follow = true;
      syncFollowUI();
    });
    $('#btn-copy').addEventListener('click', copyLogs);
    $('#btn-clearlog').addEventListener('click', () => {
      if (S.selected) {
        S.logsById[S.selected] = [];
        S.lastSeqById[S.selected] = 0;
      }
      $('#gapbar').hidden = true;
      renderLogs();
      toast('仅清显示，历史仍在服务端');
    });

    // 手动滚动：离开底部取消跟随，回到底部恢复（修 U1）。
    $('#logview').addEventListener('scroll', (e) => {
      const v = e.target;
      const atBottom = v.scrollHeight - v.scrollTop - v.clientHeight < 24;
      if (atBottom && !S.follow) {
        S.follow = true;
        syncFollowUI();
      }
      if (!atBottom && S.follow) {
        S.follow = false;
        syncFollowUI();
      }
    });

    // 扫描抽屉
    $('#scan-start').addEventListener('click', runScan);
    $('#scan-cancel').addEventListener('click', () => {
      if (S.scan.controller) S.scan.controller.abort();
    });
    $('#scan-import').addEventListener('click', importSelected);
    $('#scan-list').addEventListener('change', updateScanImport);

    // 详情操作
    $('#btn-start').addEventListener('click', () => {
      if (S.detailID) doAction('start', S.detailID, $('#btn-start'), '已发出启动指令');
    });
    $('#btn-stop').addEventListener('click', () => {
      const p = currentByID(S.detailID);
      if (!p) return;
      confirmDialog({
        title: '停止项目',
        desc: '确定要停止「' + p.name + '」吗？该项目的整棵进程树都会被终止。',
        okText: '停止',
      }).then((ok) => { if (ok) doAction('stop', p.id, $('#btn-stop'), '已发出停止指令'); });
    });
    $('#btn-restart').addEventListener('click', () => {
      const p = currentByID(S.detailID);
      if (!p) return;
      confirmDialog({
        title: '重启项目',
        desc: '确定要重启「' + p.name + '」吗？进程树会被终止后重新启动。',
        okText: '重启',
      }).then((ok) => { if (ok) doAction('restart', p.id, $('#btn-restart'), '已发出重启指令'); });
    });
    $('#btn-edit').addEventListener('click', () => {
      const p = currentByID(S.detailID);
      if (p) openFormDrawer(p);
    });
    $('#btn-folder').addEventListener('click', () => {
      if (S.detailID) openFolder(S.detailID);
    });
    $('#btn-delete').addEventListener('click', () => {
      const p = currentByID(S.detailID);
      if (!p) return;
      confirmDialog({
        title: '删除项目',
        desc: '确定要删除项目「' + p.name + '」吗？此操作不可撤销。',
        okText: '删除',
      }).then((ok) => { if (ok) deleteProject(p.id); });
    });

    // 确认弹窗
    $('#dialog-cancel').addEventListener('click', () => closeDialog(false));
    $('#dialog-ok').addEventListener('click', () => closeDialog(true));
    $('#dialog-mask').addEventListener('click', (e) => {
      if (e.target === $('#dialog-mask')) closeDialog(false);
    });

    // 遮罩点击关闭
    const masks = [
      ['#log-mask', closeLogDrawer],
      ['#detail-mask', closeDetailDrawer],
      ['#scan-mask', closeScan],
      ['#drawer-mask', closeDrawer],
    ];
    for (const [sel, fn] of masks) {
      $(sel).addEventListener('click', (e) => {
        if (e.target === $(sel)) fn();
      });
    }

    // Esc：只关最上层。
    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Escape') return;
      if (!$('#dialog-mask').hidden) { closeDialog(false); return; }
      if (!$('#log-mask').hidden) { closeLogDrawer(); return; }
      if (!$('#detail-mask').hidden) { closeDetailDrawer(); return; }
      if (!$('#scan-mask').hidden) { closeScan(); return; }
      if (!$('#drawer-mask').hidden) { closeDrawer(); return; }
      if (S.openMenu) {
        const actions = S.openMenu.closest('.p-actions');
        const more = actions && actions.querySelector('[data-act="more"]');
        closeMenu();
        if (more) more.focus();
      }
    });

    // 抽屉/弹窗打开时把 Tab 圈在容器内：否则 Shift+Tab 会跑到背后的页面，
    // aria-modal 就只是口号。
    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Tab') return;
      let mask = null;
      for (const id of ['#dialog-mask', '#log-mask', '#detail-mask', '#scan-mask', '#drawer-mask']) {
        const m = $(id);
        if (m && !m.hidden) { mask = m; break; }
      }
      if (!mask) return;
      const items = Array.from(mask.querySelectorAll('button, input, select, textarea, a[href]'))
        .filter((n) => !n.disabled && n.offsetParent !== null);
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    });
  }

  // 复制全部日志，失败回退 textarea + execCommand。
  async function copyLogs() {
    const logs = currentLogs();
    const text = logs.map((l) => (l.ts ? l.ts.slice(11, 19) + ' ' : '') + (l.text || '')).join('\n');
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text);
      } else {
        throw new Error('no clipboard');
      }
      toast('已复制 ' + logs.length + ' 行日志', 'ok');
    } catch (e) {
      try {
        const ta = el('textarea');
        ta.value = text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.select();
        document.execCommand('copy');
        ta.remove();
        toast('已复制 ' + logs.length + ' 行日志', 'ok');
      } catch (err) {
        toast('复制失败，请手动选择日志', 'error');
      }
    }
  }

  // ---------- 启动 ----------
  async function init() {
    bind();
    try {
      const meta = await api('/api/meta');
      if (meta.warning) {
        const b = $('#banner');
        b.textContent = meta.warning;
        b.hidden = false;
      }
    } catch (e) {
      toast(e.message, 'error');
    }

    await loadProjects().catch((e) => toast(e.message, 'error'));

    connectSSE();
    startUptimeTicker();
  }

  init();
})();
