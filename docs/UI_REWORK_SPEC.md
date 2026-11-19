# DevDock 界面重构施工规格（UI Rework Spec）

> 依据：`docs/UX_RESEARCH.md`（调研 agent 只读产出）。本文是主 agent 的**裁决与施工依据**，开发 agent 照此实现。
> 目标：解决老板「不好用也不好看」。只做界面层，不动后端 API、不动 CLI、不动检测逻辑。
> 版本：v0.3.0 界面重构。

---

## 0. 一句话方案

把「左侧栏 + 右侧详情卡 + 详情卡里的日志区」改成 **devwebui 同款的「顶栏 + 居中单列项目卡 + 右侧全高抽屉」**：
项目卡即单位（状态/命令/端口/时长/操作全在卡上），日志/详情/扫描/表单都是右侧抽屉，互斥开关。
同时用一整套可命名的视觉 token 重绘，并修掉日志跟随、信息密度、确认弹窗、空状态、扫描分组这些具体毛病。

---

## 1. 裁决清单（与开发相关的每一条都要落实）

| # | 裁决 | 说明 |
|---|---|---|
| R1 | **布局**：单列 `max-width:800px` 居中 + 右侧全高 push 抽屉 | 采用调研 §3.1。删除左侧 sidebar、详情卡、详情内日志卡 |
| R2 | **不做**表格/卡片双视图 | 项目数量少，收益低（调研 §4.2 可选，本期不做） |
| R3 | **项目卡即单位** | 一张卡 = 一个启动配方；项目多时靠顶栏搜索过滤 |
| R4 | **抽屉互斥**：日志/详情/扫描/表单同时只开一个 | 保留各自的 mask id（见 §3），开新前先 `closeAllDrawers()` |
| R5 | **详情独立抽屉** | 完整命令、cwd、端口、PID、就绪延迟、错误输出、环境变量、创建/更新时间 |
| R6 | **日志独立抽屉**，全高 + 工具条 | 见 §5 |
| R7 | **项目卡操作**：主操作随状态变化 + 次要操作行 | 见 §3.2 |
| R8 | **确认弹窗自绘**，替换全部 `confirm()` | 停止/重启/删除/导入四处；见 §7 |
| R9 | **顶栏搜索**：按 `name`/`command`/`cwd` 过滤 | 无结果显示「没有匹配的项目」+「清除搜索」 |
| R10 | **日志按项目缓存**，非选中项目也收 | 见 §5.4；解决「切走再切回日志断档/重拉」 |
| R11 | **历史分页不做** | 后端 `handleLogs` 只支持 `tail`/`since`，没有 `before`/最早 seq；改 API 超出本轮范围 |
| R12 | **清屏**保持「只清当前项目客户端缓冲」 | 文案改成「仅清显示，历史仍在服务端」 |
| R13 | **toast**：最多同时 3 条、带图标、3.5s 自动消失 | 超出 3 条移除最旧 |
| R14 | **数字全部 `tabular-nums`** | 端口、运行时长、计数、PID |
| R15 | **不做**（明确排除） | 主题手动切换、oklch 迁移、监控图表、国际化、通知中心、多项目日志聚合、托盘/自动更新 |
| R16 | **不新增静态文件** | `web/embed.go` 是显式白名单（`index.html style.css app.js favicon.svg`），只改这三个文件 + e2e 脚本 |
| R17 | **禁用 `innerHTML`** | 统一 `el()` + `textContent`；日志关键词高亮用 `el('mark', null, kw)` |
| R18 | **不动后端** | `/api/*` 路由与语义、SSE 事件与重连语义一律不改 |
| R19 | **状态表达**：pill 只表示主状态，readiness 细节放卡片小字与详情抽屉 | 见 §3.3 |
| R20 | **图标**：内联 SVG（不新增文件） | logo、播放、停止、重启、日志、搜索、更多、关闭、外链、警告、成功/错误 |

---

## 2. 视觉 token（`style.css` `:root` 最终值，照抄）

保留现有 hex 蓝色品牌色系，只补层次。**字阶收敛为 6 档**，禁止再出现 11.5/12.5/13.5 这类值。

```css
:root {
  --bg:#f6f7f9; --surface:#ffffff; --surface-2:#f4f6f8;
  --border:#e3e6ea; --border-strong:#d0d5db;
  --text:#1c2024; --text-dim:#5c656e; --text-mute:#8a939c;
  --accent:#2f6feb; --accent-soft:#e8f0fe;
  --ok:#1a9d5a; --ok-soft:#e6f6ec;
  --warn:#c77700; --warn-soft:#fdf3e0;
  --danger:#d13438; --danger-soft:#fdecec;

  --text-2xs:11px; --text-xs:12px; --text-sm:13px;
  --text-ui:14px; --text-md:16px; --text-lg:18px;

  --radius-sm:6px; --radius-md:10px; --radius-lg:14px; --radius-pill:999px;
  --sp-1:4px; --sp-2:8px; --sp-3:12px; --sp-4:16px; --sp-5:20px; --sp-6:24px;

  --shadow-sm:0 1px 2px rgba(16,24,40,.06), 0 2px 8px rgba(16,24,40,.05);
  --shadow-md:0 2px 4px rgba(16,24,40,.08), 0 12px 32px rgba(16,24,40,.12);
  --shadow-lg:-12px 0 40px rgba(16,24,40,.18);

  --dur-fast:160ms; --dur-base:240ms;
  --ease-out:cubic-bezier(.22,1,.36,1);
  --mono:ui-monospace,"Cascadia Mono",Consolas,"Courier New",monospace;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg:#14161a; --surface:#1d2026; --surface-2:#262a31;
    --border:#2e333b; --border-strong:#3c424c;
    --text:#e6e9ed; --text-dim:#a4adb8; --text-mute:#767f8a;
    --shadow-sm:0 1px 2px rgba(0,0,0,.4), 0 2px 8px rgba(0,0,0,.3);
    --shadow-md:0 2px 4px rgba(0,0,0,.5), 0 12px 32px rgba(0,0,0,.4);
    --shadow-lg:-12px 0 40px rgba(0,0,0,.5);
  }
}
```

组件数值：

| 组件 | 数值 |
|---|---|
| 顶栏 | `height:52px; padding:0 16px; position:sticky; top:0; z-index:20; background:color-mix(in srgb, var(--surface) 82%, transparent); backdrop-filter:blur(10px); border-bottom:1px solid var(--border)` |
| 主列 | `max-width:800px; margin:0 auto; padding:16px; display:flex; flex-direction:column; gap:12px` |
| 项目卡 | `background:var(--surface); border:1px solid var(--border); border-radius:var(--radius-md); box-shadow:var(--shadow-sm); padding:12px 14px; display:flex; gap:12px; align-items:flex-start` |
| 状态点 | `width:8px; height:8px; border-radius:50%; margin-top:6px; flex:0 0 auto` |
| 卡片标题 | `font-size:var(--text-sm); font-weight:650` |
| 卡片 meta | `font-size:var(--text-xs); color:var(--text-mute); display:flex; gap:10px; flex-wrap:wrap` |
| 命令 code | `font-family:var(--mono); font-size:var(--text-xs); background:var(--surface-2); border:1px solid var(--border); border-radius:4px; padding:1px 6px; white-space:nowrap; overflow:hidden; text-overflow:ellipsis` |
| 按钮 | `height:30px; padding:0 12px; font-size:var(--text-sm); border-radius:var(--radius-sm); border:1px solid var(--border-strong); background:var(--surface); transition:background var(--dur-fast) var(--ease-out), border-color var(--dur-fast)` |
| 主按钮 | `background:var(--accent); border-color:var(--accent); color:#fff` |
| 危险按钮 | `color:var(--danger); border-color:color-mix(in srgb, var(--danger) 40%, transparent)` |
| 胶囊 | `font-size:var(--text-2xs); font-weight:600; padding:2px 8px; border-radius:var(--radius-pill); border:1px solid transparent` |
| 抽屉 | `width:min(560px,44vw); height:100%; background:var(--surface); box-shadow:var(--shadow-lg); animation:slide-in var(--dur-base) var(--ease-out)` |
| 抽屉动画 | `@keyframes slide-in { from { transform:translateX(32px); opacity:0 } to { transform:none; opacity:1 } }` |
| 焦点 | `:focus-visible { outline:2px solid var(--accent); outline-offset:2px; border-radius:var(--radius-sm) }` |
| 滚动条 | 宽 8px，thumb 用 `var(--border-strong)`，圆角 999px |
| 数字 | `font-variant-numeric:tabular-nums` |

---

## 3. HTML 结构（`index.html`）

### 3.1 骨架

```
<header class="topbar">
  brand(logo+DevDock) | live(点+「N/M 运行中」) | 搜索框 #search | 新建/扫描/刷新
</header>
<div class="banner" id="banner" hidden></div>
<main class="container">
  <ul class="project-list" id="project-list"></ul>
  <div class="empty" id="empty-hint" hidden>…</div>       <!-- 无项目 -->
  <div class="empty" id="search-empty" hidden>…</div>     <!-- 搜索无结果 -->
</main>

<!-- 四个右抽屉：日志 / 详情 / 扫描 / 表单 -->
<div class="drawer-mask" id="log-mask" hidden> … </div>
<div class="drawer-mask" id="detail-mask" hidden> … </div>
<div class="drawer-mask" id="scan-mask" hidden> … </div>
<div class="drawer-mask" id="form-mask" hidden> … </div>

<!-- 自绘确认弹窗 -->
<div class="dialog-mask" id="dialog-mask" hidden>
  <div class="dialog" role="alertdialog" aria-modal="true" aria-labelledby="dialog-title">
    <h3 id="dialog-title"></h3>
    <p id="dialog-desc"></p>
    <div class="dialog-actions">
      <button class="btn" id="dialog-cancel">取消</button>
      <button class="btn danger" id="dialog-ok">确认</button>
    </div>
  </div>
</div>

<div class="toast-wrap" id="toast-wrap"></div>
```

### 3.2 项目卡（JS 用 `el()` 生成，禁止 `innerHTML`）

```html
<li class="project-item" data-id="…">
  <span class="dot ready"></span>
  <div class="p-main">
    <div class="p-top">
      <a class="p-name" href="http://127.0.0.1:5173" target="_blank" rel="noreferrer">前端 dev</a>
      <span class="pill ready">就绪</span>
    </div>
    <div class="p-meta">
      <code class="p-cmd">npm run dev</code>
      <span class="p-port">:5173</span>
      <span class="p-up">运行 12分03秒</span>
      <span class="p-note">就绪 808ms</span>   <!-- readiness 细节 -->
    </div>
  </div>
  <div class="p-actions">
    <!-- 主操作：stopped→启动(primary)；running→打开(primary,有端口时)/停止；starting→停止 -->
    <button class="btn primary sm" data-act="start">启动</button>
    <button class="btn sm" data-act="stop">停止</button>
    <button class="btn sm" data-act="restart">重启</button>
    <button class="btn sm" data-act="logs">日志</button>
    <button class="btn sm" data-act="detail">详情</button>
    <button class="btn icon sm" data-act="more" aria-label="更多">⋮</button>
  </div>
</li>
```

- 已停止项目整卡 `opacity:.75`（调研 §3.1）。
- 「更多」展开一个内联菜单（`.menu`）：编辑 / 打开目录 / 删除（危险）。
- 名称在 `readiness==='ready'` 且 `port>0` 时渲染为 `<a>`，否则为 `<span>`。
- 操作行**常驻显示**（不做 hover 隐藏），保证可发现性与键盘可达。

### 3..3 状态映射（R19）

| lifecycle / readiness | 点颜色 | pill 文案 | pill 类 |
|---|---|---|---|
| running + ready | ok | 就绪 | `ready` |
| running + 未就绪 | accent | 运行中 | `running` |
| starting / stopping | warn | 启动中 / 停止中 | `pending` |
| stopped | mute | 已停止 | `stopped` |
| failed（退出码非 0） | danger | 失败 | `failed` |
| 就绪超时 | danger | 就绪超时 | `failed` |

### 3.4 必须保留的 id（e2e 与既有脚本断言）

`btn-scan`、`scan-mask`、`scan-roots`、`scan-start`、`scan-cancel`、`scan-list`、`scan-summary`、`scan-import`、`scan-all`、`scan-status`、`scan-error`、`scan-more`、`btn-close-scan`、`btn-new`、`btn-refresh`、`btn-close-drawer`、`btn-cancel`、`btn-save`、`project-form`、`form-error`、`drawer-mask`（表单抽屉可改名 `form-mask`，但**不要删** `drawer-mask` 这个类名，`.drawer-mask[hidden]` 规则要保留）。
**原则：现有 id 一个都不能删**，新增 id 用新名字。

---

## 4. 顶栏与搜索

- 顶栏左侧：logo + 「DevDock」；随后 Live 点（绿=连接正常，灰=断开）+「N/M 运行中」。
- 中部：搜索框 `#search`，`input` 事件实时过滤；空搜索显示全部。匹配 `name`/`command`/`cwd`（大小写不敏感）。
- 右侧：`+ 新建`（primary）、`扫描本机`、`刷新`。
- 搜索无结果：显示 `#search-empty`（「没有匹配的项目」+「清除搜索」按钮），同时隐藏 `#empty-hint`。

---

## 5. 日志抽屉（重点）

### 5.1 结构

```
#log-mask
  <aside class="drawer log-drawer">
    <header class="drawer-head">
      <div class="drawer-title"><h2 id="log-title">项目名</h2><code id="log-cmd">命令</code></div>
      <button class="btn icon ghost" id="btn-close-log" aria-label="关闭">✕</button>
    </header>
    <div class="log-tools">
      <input id="log-filter" placeholder="过滤日志…">
      <label class="check"><input type="checkbox" id="chk-stderr"> 仅看错误</label>
      <label class="check"><input type="checkbox" id="chk-ts" checked> 时间戳</label>
      <label class="check"><input type="checkbox" id="chk-gbk"> GBK</label>
      <button class="btn sm" id="btn-jump">跳到底部</button>
      <button class="btn sm ghost" id="btn-copy">复制全部</button>
      <button class="btn sm ghost" id="btn-clearlog">清屏</button>
    </div>
    <div class="gapbar" id="gapbar" hidden>部分日志已过期，已跳过中间内容</div>
    <pre class="logview" id="logview"></pre>
    <footer class="log-foot"><span id="log-count">0 行</span><span id="log-follow-hint" hidden>已取消跟随（上滚查看历史）</span></footer>
  </aside>
```

### 5.2 工具条行为

- `#log-filter`：`input` 实时过滤；命中关键词用 `el('mark', null, kw)` 高亮（**禁止 innerHTML**）。无命中显示「没有匹配「xxx」的日志」。
- `#chk-stderr`：只显示 `stream==='err'` 的行。
- `#chk-ts`：切换 `.ts` 的 `display`。
- `#chk-gbk`：沿用 `decodeMaybeGBK`。
- `#btn-jump`：`scrollTop = scrollHeight` 并恢复跟随。
- `#btn-copy`：`navigator.clipboard`，失败回退 `textarea + document.execCommand('copy')`，toast 提示。
- `#btn-clearlog`：清空**当前项目**客户端缓冲，文案「仅清显示，历史仍在服务端」。
- 切换项目时：若该项目客户端缓存非空 → 直接渲染缓存，不重新请求；否则 `GET /api/projects/{id}/logs?tail=500`。

### 5.3 跟随逻辑（必须修，U1）

```js
// onScroll
const atBottom = view.scrollHeight - view.scrollTop - view.clientHeight < 24;
if (atBottom && !S.follow) { S.follow = true; syncFollowUI(); }
if (!atBottom && S.follow) { S.follow = false; syncFollowUI(); }   // ← 现在缺这条
// appendLogLine
if (S.follow) view.scrollTop = view.scrollHeight;
```

`syncFollowUI()` 同步复选框与底部提示 `#log-follow-hint`。

### 5.4 按项目缓存（R10）

- `S.logsById = Object.create(null)`，每个项目最多保留 3000 行（超出丢弃最旧）。
- SSE `log` 事件按 `projectID` 分发到对应缓存，**不再丢弃非选中项目**（删除现有 `if (S.selected && pid && pid !== S.selected) return`）。
- `state`/`ready` 事件仍只更新项目状态，不触发日志渲染。
- 渲染日志时：过滤条件（关键词/仅错误）变化才全量重建；`appendLogLine` 保持增量。

### 5.5 视觉

- `.logview`：`background:var(--surface-2); border:1px solid var(--border); border-radius:var(--radius-sm); padding:8px; font-family:var(--mono); font-size:var(--text-xs); line-height:1.6; overflow:auto; flex:1; min-height:0`。
- 行 `.ln`：`padding:0 6px; border-radius:3px`，`:hover{background:var(--surface)}`。
- `.ts`：固定 `7ch` 宽，`user-select:none`，`color:var(--text-mute)`。
- `stderr` 行：`color:var(--danger)`。
- 历史行（本次会话之前拉到的）：`.hist { opacity:.7 }`。
- 空态三句：`未启动，暂无日志` / `暂无输出` / `没有匹配「xxx」的日志`。

---

## 6. 扫描抽屉（在现有基础上改，保留全部 id）

1. **分组标题取末段**：`group.split(/[\\/]/).filter(Boolean).pop()`，完整路径放 `title`；样式去掉全大写与 `letter-spacing`，改 `font-size:var(--text-2xs); font-weight:600; color:var(--text-mute)`（修 U15/V14）。
2. **新增「忽略 / 撤销」**：`S.scan.ignored = new Set()`，行加 `.ignored { opacity:.45 }` + `IGNORED` 徽章，hover 出「撤销」按钮。不新增后端字段。
3. **导入失败**：失败条目加 `.scan-row.failed`（红边 + 保留），**勾选去掉**并刷新「导入选中 (N)」计数；`#scan-error` 移到 `#scan-list` 之后（修 U17/U19）。
4. **汇总条**：`扫描 N 个目录 · Nms · 共 N 条 · 已选 M`；`timedOut` 时加 warn 文案（修 U18）。
5. **默认勾选条件与单批 ≤20 与组内折叠**：语义与正则**原样保留**（e2e 断言，见 §9）。
6. `#scan-all` 文案改为「显示 low 置信度」。

---

## 7. 表单抽屉与确认弹窗

### 7.1 表单

- 星号与 label 同行（`.form label` 改 `display:block`，星号用 `.req` 内联，`margin-left:2px`）。
- 分组（`.form-group` + `.form-group-title`）：`基本`（名称/ID/工作目录/启动命令）、`就绪`（端口/就绪路径/就绪超时）、`启动`（自动启动）、`环境变量`。
- hint 用 `var(--text-xs)` + `var(--text-mute)`，与 input 拉开层级。
- 环境变量非法行不再静默丢弃：在 `#form-error` 提示「第 N 行无效，已忽略」。

### 7.2 自绘确认弹窗（R8）

- 替换全部 `confirm()`：停止、重启、删除、导入。
- 结构见 §3.1；`role="alertdialog"`，标题 + 描述 + 取消 + 确认（危险色）。
- 行为：Esc 关闭、点遮罩关闭、打开时 focus 「取消」、关闭后焦点归还触发元素。
- 文案保留原意（导入：「将导入 N 个项目，已存在的项目会被跳过」）。
- **e2e 断言同步改**（见 §9）。

---

## 8. 空状态

| 场景 | 内容 |
|---|---|
| 无项目 | 居中卡片：logo 图标 + 「还没有项目」+ 说明（「DevDock 把启动命令放进独立进程 Job，停止时整棵进程树一起清理」）+ 主按钮「+ 新建项目」+ 次按钮「扫描本机」+ 示例行 `npm run dev · :5173` |
| 搜索无结果 | 「没有匹配的项目」+「清除搜索」 |
| 日志无输出 | 三态文案（见 §5.5） |
| 扫描无结果 | 保留现有 + 「手动新建」按钮 |

---

## 9. 需要同步修改的 e2e 断言

文件：`work/testsupport/e2e/run_scan_e2e.ps1`

| 行 | 现状 | 改为 |
|---|---|---|
| 209-210 | `if ($js -match 'confirm\(') { Pass 'app.js 导入前二次确认（A3）' }` | `if ($js -match 'dialog-mask' -and $js -match 'alertdialog') { Pass 'app.js 使用自绘确认弹窗做二次确认（A3）' } else { Fail 'app.js 缺少自绘确认弹窗（A3 二次确认）' }` |

其余断言**必须继续通过**：
- 静态资源 200（`/`、`/index.html`、`/style.css`、`/app.js`、`/favicon.svg`）
- index.html 含 8 个 id（`btn-scan` `scan-mask` `scan-roots` `scan-start` `scan-cancel` `scan-list` `scan-summary` `scan-import`）
- `scan-mask` 复用 `.drawer-mask` 类
- `style.css` 含 `.drawer-mask[hidden]`
- `app.js` 含 `function openScan(` `closeScan(` `runScan(` `importSelected(` `fillFromSuggestion(`
- `app.js` 无 `innerHTML`
- 默认勾选正则 `s\.confidence\s*!==\s*'low'` 与 `s\.port\s*>\s*0\s*\|\|\s*!!s\.framework\s*\|\|\s*s\.confidence\s*===\s*'high'`
- `picks\.length\s*>\s*20`
- `scan-more` + `items\.slice\(0,\s*5\)`

> **自检红线**：每新增一个带 `display:flex/grid` 的类名，必须同步补一条 `.类名[hidden]{display:none}`，否则会复现「抽屉关不掉」的历史 Bug。

---

## 10. 验收清单（主 agent 会逐条跑）

1. `go build ./...`、`go vet ./...`、`go test -count=1 ./...` 退出码全 0。
2. `testsupport/e2e/run_e2e.ps1` → `E2E_RESULT=PASS`，且性能不回退（启动 <1000ms、空闲 working set <50MiB、start API <100ms）。
3. `testsupport/e2e/run_scan_e2e.ps1` → `SCAN_E2E_RESULT=PASS`（断言已同步）。
4. 真实浏览器走查（paw-browser，暗色 + 亮色）：
   - 打开 → 项目卡显示命令/端口/时长/状态 → 启动 → 状态变「就绪」→ 名称可点；
   - 日志抽屉：上滚 → 底部出现「已取消跟随」→ 新日志不拽底 → 滚回底部恢复跟随；过滤命中高亮、无匹配文案正确；
   - 详情抽屉：完整命令/cwd/PID/就绪延迟/错误输出；
   - 扫描：分组标题是仓库名不是完整路径；勾选 → 导入 → 计数正确；忽略/撤销可用；
   - 停止/重启/删除 → 自绘确认弹窗 → Esc 关闭、焦点归还；
   - 搜索过滤与「清除搜索」；
   - Tab 焦点环可见；抽屉打开时焦点不跑出抽屉。
5. 截图归档到 `work/_e2e/`：覆盖 `ui-main-live.png`、`ui-drawer-open.png`、`ui-scan-open2.png`，新增 `ui-log-open.png`、`ui-detail-open.png`（暗色为主，亮色可选）。

---

## 11. 红线

- 只改 `D:\Desktop\DevDock`；`web/embed.go` 白名单不变（不新增静态文件）。
- 不动后端 `/api/*`、不动 CLI、不动 `internal/detect`、不动 SSE 重连语义。
- 不用 `innerHTML`；不引入任何第三方前端库；无构建链。
- 保留 §3.4 全部 id 与 §9 全部断言语义。
- 时间预算：本规格为单轮施工，不要顺手扩功能。

---

## 12. 实现偏差记录

> 开发 agent 如与本规格有偏差，必须在此逐条记录（偏差 → 原因 → 影响），不要静默改设计。

### 开发 agent 记录（2026-11-18）

1. **偏差**：表单抽屉保留 `id="drawer-mask"`，未按 §3.1 骨架改名 `form-mask`。
   **原因**：§3.4 把 `drawer-mask` 列入「现有 id 一个都不能删」，与 §3.1 的 `id="form-mask"` 冲突；id 与类名同为 `drawer-mask` 可同时满足 §3.4 与 §9 的 `.drawer-mask[hidden]` 字面断言。
   **影响**：无。仅命名与 §3.1 骨架不同，选择器与断言全部命中。

2. **偏差**：在逐条 `.类名[hidden]` 规则之外，额外加了一条全局 `[hidden]{display:none!important}`。
   **原因**：§9 自检红线只要求「新增 display:flex/grid 类各补一条」，但历史「抽屉关不掉」的根因正是作者样式压过浏览器默认 `[hidden]`；全局兜底后再逐类补齐更稳。
   **影响**：无负面。`hidden` 语义更稳，逐类规则仍全部保留。

3. **偏差**：`app.js` 的 `confirmDialog()` 里用 `setAttribute('role','alertdialog')` 动态设置角色。
   **原因**：§9 断言作用于 `/app.js` 文本，只写在本仓库 `index.html` 里的 `alertdialog` 命不中该正则；JS 内显式设置可同时满足断言与真实无障碍语义（`index.html` 仍保留静态 `role`）。
   **影响**：无。

4. **偏差**：日志工具条新增 `#chk-follow` 复选框（§5.1 工具条清单未列出）。
   **原因**：§5.3 要求 `syncFollowUI()` 同步「复选框与底部提示 `#log-follow-hint`」，该控件必须存在。
   **影响**：工具条多一个复选框，跟随状态可见；无其他影响。

5. **偏差**：扫描忽略态的「撤销」按钮默认用 `opacity:0` 隐藏，而非 `display:none`。
   **原因**：`display:none` 的元素不进 Tab 序列，键盘用户永远无法点到「撤销」；改为透明但保留在文档流，hover / `:focus-visible` 时显形。
   **影响**：忽略态行内预留一个透明按钮位；仍满足 §6.2「hover 出撤销」的视觉意图，并补上键盘可达性。

### 主 agent 验收后追加（2026-11-18）

验收（真实浏览器走查）暴露了规格没覆盖的 9 个问题，处理裁决如下。**裁决优先于本节之前的任何条文**。

| # | 问题 | 裁决 | 落地位置 |
|---|---|---|---|
| A1 | `.p-name` 整条不显示（flex 子项带 `overflow:hidden`，自动最小宽度为 0） | **按根因修**：`flex:1 1 auto; min-width:0` | `style.css` `.p-name` |
| A2 | `.p-cmd` 换行后占满整行，端口被顶走；`.p-port/.p-up/.p-note` 无规则被压成 min-content（逐字竖排） | **收窄弹性**：`.p-meta` 改 `nowrap`，`.p-cmd` 改 `flex:0 1 auto`，新增 `.p-port/.p-up/.p-note{flex:0 0 auto; white-space:nowrap}`——空间不足时只由命令省略 | `style.css` `.p-meta` 等 |
| A3 | 完全没有窄屏规则（317px 下顶栏按钮出视口、卡片名称消失） | **新增** `@media (max-width:640px)`：顶栏换行、搜索整行、卡片纵排、meta 允许换行 | `style.css` 尾部 |
| A4 | `.drawer` / `.scan-drawer` 是 `min(560px, 44vw)`，窄视口下只剩 139px | **裁决**：窄屏下抽屉**占满宽度**（不做 44vw 等比），扫描行才不会被挤成竖排 | `style.css` media |
| A5 | 关闭抽屉后焦点掉回 `<body>`（`renderList()` 的 `ul.textContent=''` 把触发按钮移出文档） | **改 `restoreFocus()`**：按「同一张卡片 + 同一个 `data-act`」把新按钮找回来再聚焦 | `web/app.js` `restoreFocus()` |
| A6 | 删除确认框取消后焦点掉回 `<body>` | **裁决**：删除流程**不提前 `closeMenu()`**，弹窗期间菜单保持打开；确认删除时才关菜单 | `web/app.js` `case 'delete'` |
| A7 | Esc 关不掉「更多」菜单 | **补** Esc 分支关菜单，并把焦点还给「更多」按钮 | `web/app.js` Esc 处理末尾 |
| A8 | 无 Tab 焦点圈，Tab 能跑到背后页面，`aria-modal` 形同虚设 | **新增** keydown 处理：抽屉/弹窗可见时把 Tab / Shift+Tab 圈在容器内 | `web/app.js`（Esc 处理之后） |
| A9 | §6.2 要求「已忽略」徽章，`.scan-badge.ignored` 样式存在但从未被使用 | **裁决**：规格要求的功能缺失，补上；忽略时加徽章，撤销时移除 | `web/app.js` 扫描行渲染 |

另有两条**不在规格内的同步改动**，一并记录：

- `testsupport/e2e/run_e2e.ps1`、`run_scan_e2e.ps1` 的 `-X main.version` 从 `0.2.0` 改为 `0.3.0`（否则 e2e 会继续用旧版本号构建，验收对象和产物不一致）。
- 上述所有改动只落在 `web/` 与两个 e2e 脚本，未触碰 `/api/*`、CLI、`internal/detect`、SSE 语义，符合 §11 红线。

### 验收期工具限制（如实记录）

- paw 的 WebContentsView 只有 **317 CSS px** 宽，全部截图为窄视口；≥ 1000px 桌面排版只做了代码审查，未截图。
- 会话后期 paw 无法把真实按键投递到页面（页面 `keydown` 计数为 0），Esc 相关路径改用 `document.dispatchEvent(new KeyboardEvent('keydown', {key:'Escape'}))` 验证处理逻辑；只有日志抽屉被真实 Esc 关闭过一次。**不得据此声称全部 Esc 路径都验证过真实按键。**
- 真实点击亦有同样问题，涉及点击的断言用 `evaluate` 内 `.click()` / 先 `focus()` 再 `.click()` 验证。
