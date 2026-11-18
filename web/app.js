/* DevDock 前端逻辑
   零依赖、零构建。所有状态变更走 REST，实时推送走一个全局 SSE 连接。 */
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
    states: {},        // id -> state
    selected: null,
    logs: [],          // 当前项目的日志行
    lastSeq: 0,
    follow: true,
    gbk: false,
    es: null,
    editID: null,
    logVersion: 0,     // 用于丢弃过期异步渲染
    scan: { controller: null, rows: [] },
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

  function toast(msg, kind) {
    const t = el('div', 'toast' + (kind ? ' ' + kind : ''), msg);
    $('#toast-wrap').appendChild(t);
    setTimeout(() => {
      t.style.opacity = '0';
      setTimeout(() => t.remove(), 200);
    }, kind === 'error' ? 6000 : 3000);
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

  // ---------- 状态文案 ----------
  // 这是产品的核心表达：进程是否运行 与 业务是否就绪 是两回事。
  function statusInfo(st) {
    if (!st) return { text: '已停止', cls: 'stopped' };
    switch (st.lifecycle) {
      case 'starting':
        return { text: '启动中', cls: 'starting' };
      case 'stopping':
        return { text: '停止中', cls: 'stopping' };
      case 'failed':
        return { text: '启动失败' + (st.lastExitCode != null ? '（退出码 ' + st.lastExitCode + '）' : ''), cls: 'failed' };
      case 'running':
        switch (st.readiness) {
          case 'ready': return { text: '就绪', cls: 'ready' };
          case 'pending': return { text: '进程运行中 · 等待就绪', cls: 'pending' };
          case 'timeout': return { text: '进程运行中 · 就绪超时', cls: 'timeout' };
          case 'error': return { text: '进程运行中 · 就绪探测出错', cls: 'timeout' };
          default: return { text: '运行中（未配置就绪检查）', cls: 'running-none' };
        }
      default:
        return { text: '已停止', cls: 'stopped' };
    }
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

  // ---------- 渲染：列表 ----------
  function renderList() {
    const ul = $('#project-list');
    ul.textContent = '';

    $('#empty-hint').hidden = S.projects.length > 0;

    for (const p of S.projects) {
      const st = S.states[p.id] || null;
      const info = statusInfo(st);

      const li = el('li', 'project-item' + (p.id === S.selected ? ' active' : ''));
      li.appendChild(el('span', 'dot ' + info.cls));

      const box = el('div', 'info');
      box.appendChild(el('div', 'name', p.name));

      const bits = [info.text];
      if (p.port > 0) bits.push(':' + p.port);
      box.appendChild(el('div', 'sub', bits.join(' · ')));
      li.appendChild(box);

      li.addEventListener('click', () => selectProject(p.id));
      ul.appendChild(li);
    }

    // 汇总
    let running = 0, ready = 0, failed = 0;
    for (const p of S.projects) {
      const st = S.states[p.id];
      if (!st) continue;
      if (st.lifecycle === 'running') running++;
      if (st.lifecycle === 'running' && st.readiness === 'ready') ready++;
      if (st.lifecycle === 'failed') failed++;
    }
    let txt = S.projects.length + ' 个项目 · ' + running + ' 个运行中';
    if (ready) txt += ' · ' + ready + ' 个就绪';
    if (failed) txt += ' · ' + failed + ' 个失败';
    $('#summary').textContent = txt;
  }

  // ---------- 渲染：详情 ----------
  function currentProject() {
    return S.projects.find((p) => p.id === S.selected) || null;
  }

  function renderDetail() {
    const p = currentProject();
    $('#placeholder').hidden = !!p;
    $('#detail-body').hidden = !p;
    if (!p) return;

    const st = S.states[p.id] || { lifecycle: 'stopped', readiness: 'none' };
    const info = statusInfo(st);

    $('#d-name').textContent = p.name;
    $('#d-cwd').textContent = p.cwd;
    $('#d-command').textContent = p.command;
    $('#d-port').textContent = p.port > 0 ? String(p.port) : '未声明';

    const pill = $('#d-status');
    pill.textContent = info.text;
    pill.className = 'pill ' + info.cls;

    $('#d-pid').textContent = st.pid ? String(st.pid) : '—';
    $('#d-uptime').textContent = st.lifecycle === 'running' ? fmtDuration(st.startedAt) : '—';

    if (st.lifecycle === 'running') {
      if (st.readiness === 'ready') {
        $('#d-readiness').textContent = (st.readyLatencyMs ? st.readyLatencyMs + ' ms · ' : '') + '已就绪';
      } else if (st.readiness === 'pending') {
        $('#d-readiness').textContent = '等待中…';
      } else if (st.readiness === 'timeout') {
        $('#d-readiness').textContent = '超时（进程仍在运行）';
      } else {
        $('#d-readiness').textContent = '未配置就绪检查';
      }
    } else {
      $('#d-readiness').textContent = '—';
    }

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

  // ---------- 渲染：日志 ----------
  function decodeMaybeGBK(text) {
    if (!S.gbk) return text;
    try {
      // 服务端按 UTF-8 尽力解码，GBK 字节会变成替换符。
      // 这里用浏览器原生 GBK 解码器重新解释原始字节。
      // 由于服务端已经把字节转成字符串，我们只能对"看起来是乱码"的部分做补救：
      // 把字符串重新编码为 latin1 字节再按 GBK 解码。
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

  function renderLogs() {
    const view = $('#logview');
    const frag = document.createDocumentFragment();
    for (const line of S.logs) {
      const span = el('span', 'ln' + (line.stream === 'err' ? ' stderr' : ''));
      const ts = el('span', 'ts', (line.ts || '').slice(11, 19));
      span.appendChild(ts);
      span.appendChild(document.createTextNode(decodeMaybeGBK(line.text)));
      frag.appendChild(span);
    }
    view.textContent = '';
    view.appendChild(frag);
    $('#log-count').textContent = S.logs.length + ' 行';

    if (S.follow) {
      view.scrollTop = view.scrollHeight;
    }
  }

  function appendLogLine(line) {
    // 服务端重连补发时可能重复，按 seq 去重。
    if (line.seq && line.seq <= S.lastSeq) return;
    if (line.seq) S.lastSeq = line.seq;

    S.logs.push(line);
    if (S.logs.length > MAX_CLIENT_LINES) {
      S.logs.splice(0, S.logs.length - MAX_CLIENT_LINES);
    }

    const view = $('#logview');
    const span = el('span', 'ln' + (line.stream === 'err' ? ' stderr' : ''));
    span.appendChild(el('span', 'ts', (line.ts || '').slice(11, 19)));
    span.appendChild(document.createTextNode(decodeMaybeGBK(line.text)));
    view.appendChild(span);

    while (view.childElementCount > MAX_CLIENT_LINES) {
      view.removeChild(view.firstChild);
    }

    $('#log-count').textContent = S.logs.length + ' 行';
    if (S.follow) view.scrollTop = view.scrollHeight;
  }

  // ---------- 数据加载 ----------
  async function loadProjects() {
    const data = await api('/api/projects');
    S.projects = data.projects || [];
    for (const v of S.projects) {
      S.states[v.id] = v.state;
    }
    if (S.selected && !S.projects.some((p) => p.id === S.selected)) {
      S.selected = null;
    }
    renderList();
    renderDetail();
  }

  async function loadLogs(id) {
    S.logs = [];
    S.lastSeq = 0;
    $('#logview').textContent = '';
    const data = await api('/api/projects/' + encodeURIComponent(id) + '/logs?tail=500');
    for (const line of data.lines || []) {
      if (line.seq) S.lastSeq = Math.max(S.lastSeq, line.seq);
    }
    S.logs = data.lines || [];
    if (data.gap) $('#gapbar').hidden = false;
    renderLogs();
  }

  async function selectProject(id) {
    S.selected = id;
    $('#gapbar').hidden = true;
    renderList();
    renderDetail();
    try {
      await loadLogs(id);
    } catch (e) {
      toast(e.message, 'error');
    }
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
      renderList();
      if (st.id === S.selected) renderDetail();
    });

    es.addEventListener('log', (ev) => {
      let line;
      try { line = JSON.parse(ev.data); } catch (e) { return; }
      // 全局流会收到所有项目的日志，只显示当前选中项目的。
      const pid = ev.data && JSON.parse(ev.data).id;
      if (S.selected && pid && pid !== S.selected) return;
      appendLogLine(line);
    });

    es.addEventListener('ready', () => {
      // 状态事件会带 readiness，无需额外处理。
    });

    es.addEventListener('gap', () => {
      $('#gapbar').hidden = false;
    });

    es.onerror = () => {
      // EventSource 自带重连，这里只更新汇总提示。
      $('#summary').textContent = '连接中断，正在重连…';
    };
  }

  // ---------- 动作 ----------
  async function doAction(action, okMsg) {
    const p = currentProject();
    if (!p) return;
    try {
      await api('/api/projects/' + encodeURIComponent(p.id) + '/' + action, { method: 'POST' });
      if (okMsg) toast(okMsg, 'ok');
      await loadProjects();
    } catch (e) {
      toast(e.message, 'error');
      await loadProjects().catch(() => {});
    }
  }

  // ---------- 抽屉 ----------
  function openDrawer(project) {
    S.editID = project ? project.id : null;
    $('#drawer-title').textContent = project ? '编辑项目' : '新建项目';
    const f = $('#project-form');
    f.reset();
    $('#form-error').hidden = true;

    if (project) {
      f.name.value = project.name || '';
      f.id.value = project.id || '';
      f.id.disabled = true;              // id 创建后不可改
      f.cwd.value = project.cwd || '';
      f.command.value = project.command || '';
      f.port.value = project.port || '';
      f.readyPath.value = project.readiness && project.readiness.path ? project.readiness.path : '';
      f.readyTimeout.value = project.readiness && project.readiness.timeoutSec ? project.readiness.timeoutSec : '';
      f.autostart.value = project.autostart ? 'true' : 'false';
      const envLines = Object.keys(project.env || {}).map((k) => k + '=' + project.env[k]);
      f.env.value = envLines.join('\n');
    } else {
      f.id.disabled = false;
    }

    $('#drawer-mask').hidden = false;
    setTimeout(() => f.name.focus(), 30);
  }

  function closeDrawer() {
    $('#drawer-mask').hidden = true;
    S.editID = null;
  }

  function parseEnv(text) {
    const env = {};
    for (const raw of (text || '').split('\n')) {
      const line = raw.trim();
      if (!line || line.startsWith('#')) continue;
      const i = line.indexOf('=');
      if (i <= 0) continue;
      env[line.slice(0, i).trim()] = line.slice(i + 1);
    }
    return env;
  }

  async function submitForm(ev) {
    ev.preventDefault();
    const f = $('#project-form');
    const errBox = $('#form-error');
    errBox.hidden = true;

    const body = {
      name: f.name.value.trim(),
      cwd: f.cwd.value.trim(),
      command: f.command.value,
      port: f.port.value ? parseInt(f.port.value, 10) : 0,
      autostart: f.autostart.value === 'true',
      env: parseEnv(f.env.value),
    };

    const readyPath = f.readyPath.value.trim();
    if (readyPath) {
      body.readiness = {
        path: readyPath,
        timeoutSec: f.readyTimeout.value ? parseInt(f.readyTimeout.value, 10) : 30,
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
        const id = f.id.value.trim();
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
      if (S.selected) await selectProject(S.selected);
    } catch (e) {
      errBox.textContent = e.message;
      errBox.hidden = false;
    }
  }

  // ---------- 扫描抽屉 ----------
  // 扫描只读：只产出建议，导入必须由用户显式触发（单条填入表单 / 批量导入）。
  function openScan() {
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
    updateScanImport();
    setTimeout(() => $('#scan-roots').focus(), 30);
  }

  function closeScan() {
    if (S.scan.controller) {
      S.scan.controller.abort();
      S.scan.controller = null;
    }
    $('#scan-mask').hidden = true;
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

    if (rows.length === 0) {
      const empty = el('div', 'scan-empty');
      empty.appendChild(el('p', 'empty-title', '没有发现可识别的项目'));
      empty.appendChild(el('p', 'empty-desc', '可以指定一个根目录再试。'));
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
        list.appendChild(el('div', 'scan-group', g));
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

    const parts = ['扫描 ' + res.scannedDirs + ' 个目录', res.elapsedMs + 'ms', rows.length + ' 条建议'];
    if (res.timedOut) parts.push('已达时间预算');
    if (res.truncated) parts.push('结果已截断');
    $('#scan-summary').textContent = parts.join(' · ');
    updateScanImport();
  }

  function scanRow(s) {
    const row = el('div', 'scan-row' + (s.duplicate ? ' duplicate' : ''));

    const pickLabel = el('label', 'scan-check');
    const pick = document.createElement('input');
    pick.type = 'checkbox';
    pick.className = 'scan-pick';
    // A3：默认只勾「非 low，且已知端口 / 已识别框架 / high 置信度」。
    // 既没端口又没框架、又不是 high 的裸脚本（本机约 17 条，含 `node dist/cli.js --no-open`
    // 这类跑完即退出的 CLI 工具）不替用户勾——列出来让他自己点，也不让「一键导入」变成一键导入
    // 一堆启动即退出的条目。high 主要是 Go 服务（go run . 没有框架与端口线索，但证据充分）。
    pick.checked = !s.duplicate && !!s.command && s.confidence !== 'low' &&
      (s.port > 0 || !!s.framework || s.confidence === 'high');
    pick.dataset.idx = String(S.scan.rows.indexOf(s));
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

    const sub = [s.cwd, s.port > 0 ? ':' + s.port : '端口未推断'];
    if (s.portInUse) sub.push('端口 ' + s.port + ' 当前被占用');
    info.appendChild(el('div', 'scan-sub', sub.join(' · ')));

    if (s.warnings && s.warnings.length) {
      info.appendChild(el('div', 'scan-warn', s.warnings.join('；')));
    }
    row.appendChild(info);

    const fill = el('button', 'btn small ghost scan-fill', '填入表单');
    fill.addEventListener('click', () => fillFromSuggestion(s));
    row.appendChild(fill);
    return row;
  }

  function updateScanImport() {
    const n = document.querySelectorAll('#scan-list .scan-pick:checked').length;
    const btn = $('#scan-import');
    btn.textContent = '导入选中 (' + n + ')';
    btn.disabled = n === 0;
  }

  // 填入表单：不直接创建，用户能在抽屉里改完再走现有 submitForm。
  function fillFromSuggestion(s) {
    closeScan();
    openDrawer(null);
    const f = $('#project-form');
    f.name.value = s.name || '';
    f.id.value = '';
    f.cwd.value = s.cwd || '';
    f.command.value = s.command || '';
    f.port.value = s.port > 0 ? String(s.port) : '';
    f.readyPath.value = '';
    f.readyTimeout.value = '';
    f.autostart.value = 'false';
    f.env.value = '';
  }

  // 批量导入：串行 POST，失败的条目保留在列表里并显示服务端 message。
  async function importSelected() {
    const picks = Array.from(document.querySelectorAll('#scan-list .scan-pick:checked'))
      .map((cb) => S.scan.rows[Number(cb.dataset.idx)])
      .filter(Boolean);
    if (picks.length === 0) return;
    // A3：单批 ≤ 20，且导入前二次确认。
    if (picks.length > 20) {
      toast('单批最多导入 20 条，请减少勾选（当前 ' + picks.length + ' 条）', 'error');
      return;
    }
    if (!confirm('将导入 ' + picks.length + ' 个项目，继续？')) return;

    const btn = $('#scan-import');
    btn.disabled = true;
    const failed = [];
    let ok = 0;
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
        ok++;
      } catch (e) {
        failed.push(s.name + '：' + e.message);
      }
    }

    if (failed.length > 0) {
      const box = $('#scan-error');
      box.textContent = failed.join('\n');
      box.hidden = false;
      btn.disabled = false;
      toast('有 ' + failed.length + ' 条导入失败，已保留在列表中', 'error');
      return;
    }

    toast('已导入 ' + ok + ' 个项目', 'ok');
    closeScan();
    await loadProjects();
  }

  // ---------- 事件绑定 ----------
  function bind() {
    $('#btn-refresh').addEventListener('click', () => {
      loadProjects().then(() => toast('已刷新')).catch((e) => toast(e.message, 'error'));
    });
    $('#btn-new').addEventListener('click', () => openDrawer(null));
    $('#btn-close-drawer').addEventListener('click', closeDrawer);
    $('#btn-cancel').addEventListener('click', closeDrawer);
    $('#project-form').addEventListener('submit', submitForm);

    $('#btn-scan').addEventListener('click', openScan);
    $('#btn-close-scan').addEventListener('click', closeScan);
    $('#scan-start').addEventListener('click', runScan);
    $('#scan-cancel').addEventListener('click', () => {
      if (S.scan.controller) S.scan.controller.abort();
    });
    $('#scan-import').addEventListener('click', importSelected);
    $('#scan-list').addEventListener('change', updateScanImport);

    $('#drawer-mask').addEventListener('click', (e) => {
      if (e.target === $('#drawer-mask')) closeDrawer();
    });

    $('#scan-mask').addEventListener('click', (e) => {
      if (e.target === $('#scan-mask')) closeScan();
    });

    $('#btn-start').addEventListener('click', () => doAction('start', '已发出启动指令'));
    $('#btn-stop').addEventListener('click', () => {
      if (!confirm('确定要停止这个项目吗？该项目的整棵进程树都会被终止。')) return;
      doAction('stop', '已发出停止指令');
    });
    $('#btn-restart').addEventListener('click', () => doAction('restart', '已发出重启指令'));

    $('#btn-edit').addEventListener('click', () => {
      const p = currentProject();
      if (p) openDrawer(p);
    });

    $('#btn-folder').addEventListener('click', async () => {
      const p = currentProject();
      if (!p) return;
      try {
        await api('/api/projects/' + encodeURIComponent(p.id) + '/open-folder', { method: 'POST' });
      } catch (e) {
        toast(e.message, 'error');
      }
    });

    $('#btn-delete').addEventListener('click', async () => {
      const p = currentProject();
      if (!p) return;
      if (!confirm('确定要删除项目「' + p.name + '」吗？此操作不可撤销。')) return;
      try {
        await api('/api/projects/' + encodeURIComponent(p.id) + '?stop=true', { method: 'DELETE' });
        S.selected = null;
        toast('项目已删除', 'ok');
        await loadProjects();
        $('#detail-body').hidden = true;
        $('#placeholder').hidden = false;
      } catch (e) {
        toast(e.message, 'error');
      }
    });

    $('#chk-follow').addEventListener('change', (e) => {
      S.follow = e.target.checked;
      if (S.follow) {
        const v = $('#logview');
        v.scrollTop = v.scrollHeight;
      }
    });

    $('#chk-gbk').addEventListener('change', (e) => {
      S.gbk = e.target.checked;
      renderLogs();
    });

    $('#btn-clearlog').addEventListener('click', () => {
      S.logs = [];
      renderLogs();
    });

    // 手动滚动时自动取消跟随，滚回底部再打开。
    $('#logview').addEventListener('scroll', (e) => {
      const v = e.target;
      const atBottom = v.scrollHeight - v.scrollTop - v.clientHeight < 24;
      const chk = $('#chk-follow');
      if (atBottom && !chk.checked) {
        chk.checked = true;
        S.follow = true;
      }
    });

    document.addEventListener('keydown', (e) => {
      if (e.key !== 'Escape') return;
      if (!$('#drawer-mask').hidden) { closeDrawer(); return; }
      if (!$('#scan-mask').hidden) closeScan();
    });
  }

  // ---------- 计时器：刷新运行时长 ----------
  function startUptimeTicker() {
    setInterval(() => {
      if (S.selected) {
        const st = S.states[S.selected];
        if (st && st.lifecycle === 'running') {
          $('#d-uptime').textContent = fmtDuration(st.startedAt);
        }
      }
    }, 1000);
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

    // 默认选中第一个项目。
    if (!S.selected && S.projects.length > 0) {
      await selectProject(S.projects[0].id);
    }

    connectSSE();
    startUptimeTicker();
  }

  init();
})();
