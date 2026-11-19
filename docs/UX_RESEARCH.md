# DevDock 界面交互与视觉设计调研

> 性质：**只读调研**。本文未修改任何代码，唯一产出是本文件。
> 老板原话：「我感觉现在不好用也不好看」。上一轮只覆盖了「怎么识别项目」，本轮只覆盖**界面交互与视觉**。
> 证据分三类：
> 1. devwebui 源码与截图（`D:\Desktop\aihaoji-workspace\tools\devwebui`，只读，未改）；
> 2. DevDock 现状源码与三张真机截图（`work/web/index.html`、`style.css`、`app.js`；`work/_e2e/ui-main-live.png`、`ui-drawer-open.png`、`ui-scan-open2.png`）；
> 3. 联网资料（见 §1.4，均附来源；查不到的明确写「未找到」）。
>
> 行号基于当前工作区文件（`index.html` 179 行 / `style.css` 643 行 / `app.js` 837 行）。

---

## 1. 参考项目做法清单

### 1.1 devwebui 布局骨架（与 DevDock 最接近，可整套借鉴）

来源：`web/src/AppShell.vue`、`shell/AppContainer.vue`、`shell/Sidebar.vue`、`components/TopBar.vue`、`components/RightDrawer.vue`。

```
┌────────────────────────────────────────────────────────────┐
│ TopBar（sticky top-0 z-20，bg-background/80 + backdrop-blur）│
│  logo 标题 │ Live ● 3 of 5 active │ 搜索 │ +Add │ 🔔 ⚙ ⋮    │
├────────────────────────────────────────────────────────────┤
│ 主区：单列、居中、max-width: 800px（--container-max）        │
│  ┌ 项目分组卡（Collapsible，rounded-xl border bg-card/40）─┐ │
│  │ ▾ 📦 项目名  [running/total badge]      ●开关 ▶ ■ ⋮    │ │
│  │   进程行/卡片（彩色点 + 名称 + 命令 + 状态 + 端口 +     │ │
│  │              时长 + CPU/内存 + Start/Stop/Restart/日志）│ │
│  └────────────────────────────────────────────────────────┘ │
│  ┌ 项目分组卡 2 …                                          ┐ │
├────────────────────────────────────────────────────────────┤
│ 右侧 push drawer（非模态、推移内容、互斥：settings/logs/     │
│ notifications 只能开一个）；300ms cubic-bezier(.4,0,.2,1)     │
└────────────────────────────────────────────────────────────┘
```

关键事实（带文件:行）：

| 事实 | 数值 / 做法 | 出处 |
|---|---|---|
| 内容列宽 | `--container-max: 800px`，`mx-auto w-full px-4 sm:px-6` | `styles/kit-base.css`；`shell/AppContainer.vue` |
| 顶栏 | `sticky top-0 z-20`，内容 `max-w-(--container-max) px-4 py-3`；标题 `text-lg font-bold tracking-tight` | `components/TopBar.vue` |
| 项目分组 | 卡片容器 `rounded-xl border border-border bg-card/40`；组头 `px-2.5 py-3 sm:px-4`；折叠箭头 `-rotate-90` | `components/ProjectPanel.vue` |
| 进程密度 | 卡片视图 `grid grid-cols-1 gap-4 sm:grid-cols-2`；表格视图 `rounded-lg border` | `components/ProjectPanel.vue` |
| 右抽屉 | push 模式**无 backdrop**、点击外部不关（`guardOutside` 仅 overlay 生效）；移动端的 bottom sheet `max-h-[92vh] rounded-t-2xl` | `shell/Sidebar.vue` |
| 抽屉头 | `h-12 px-4 pe-12 bg-background/70 backdrop-blur-md`；关闭钮 `absolute top-3 right-3` | `shell/Sidebar.vue` |
| 视图切换 | 表格 / 卡片分段控件 `rounded-md bg-muted p-1`，放在顶栏 ⋮ 菜单 | `components/TopBar.vue` |

### 1.2 devwebui 组件清单与关键交互

| 组件 | 值得抄的具体做法 | 出处 |
|---|---|---|
| `TopBar.vue` | Live/Offline 圆点（`animate-ping` + success 色）+「N of M active」；通知铃铛 badge `min-w-4 rounded-full px-1 text-3xs ring-2 ring-background`，>9 显示 `9+`；Add 按钮 hover 展开文字（`max-w-0 → group-hover:max-w-28`，200ms） | `components/TopBar.vue` |
| `ProjectPanel.vue` | 组头右侧：Switch + 启动/停止 IconButton + ⋮（编辑项目名/色、加进程、快捷方式、检查自动启动冲突、删除-确认）；内容区 disabled 时 `opacity-50`；过滤空态「noProcessesMatch」 | `components/ProjectPanel.vue` |
| `ProcessCard.vue` | 彩色圆点 `size-2.5`；running 时名称变 `<a>` + ExternalLink（hover 才显影）；错误计数 badge（destructive/10，点击 emit errors）；引擎 Badge `font-mono text-2xs`；指标 `grid grid-cols-3 text-xs tabular-nums`（uptime/cpu/mem）；操作行 `Start/Stop/Restart/Edit/Logs(ms-auto)/⋮` | `components/ProcessCard.vue` |
| `ProcessTable.vue` | 可排序列（Process/Status/Port/Uptime/CPU/Mem）；disabled 行 `variant dimmed`（降透明度）；端口冲突显 warning 色 | `components/ProcessTable.vue` |
| `LogDrawer.vue` | 头部名称 + `<code>` 完整命令；搜索框 `h-8`（大小写不敏感，实时过滤 live+history）；历史分页 `HISTORY_PAGE = 500`（加载时保持滚动位置）；历史行 `opacity-70`；stderr `text-destructive`；容器 `rounded-md border border-border bg-muted p-3 font-mono text-xs leading-relaxed`；空态区分 `noMatch` / `noOutput`；切换进程自动拉取并滚底；行 `key=seq` 去重 | `components/LogDrawer.vue` |
| `ScanResults.vue` | 容器 `rounded-xl border border-border bg-muted/30 p-2`；汇总行（count / ms / dirs + timedOut + stillScanning）；列表 `max-h-44`（compact）或 `max-h-[min(60vh,28rem)]`；configured 行 `bg-primary/10` + `text-3xs font-medium uppercase text-primary` 徽章；detected 行 `Sparkles` + framework 徽章；ignored 行 `opacity-45` + `IGNORED` 徽章 + hover 出撤销；空态带「Add manually」 | `components/ScanResults.vue` |
| `SettingsGroup.vue` / `SettingsRow.vue` | 小 caps 分组标题 `text-2xs font-semibold uppercase tracking-wider text-muted-foreground`；卡片 `rounded-xl border border-border/60 bg-card shadow-sm divide-y`；行 `px-3.5 py-2.5`，图标 `size-4.5 text-muted-foreground`，说明 `text-hint`，可点行 hover `bg-accent/60` | `shell/SettingsGroup.vue`、`shell/SettingsRow.vue` |
| `FocusView.vue` | 单进程「启动器」窗口：header Live 点 + 项目名 + Open dashboard；compact ProcessCard；footer Close（运行中先确认） | `components/FocusView.vue` |
| `RightDrawer.vue` | body `flex min-h-0 flex-1 flex-col overflow-hidden px-4 pb-4`，让内部日志自己滚 | `components/RightDrawer.vue` |

### 1.3 devwebui 视觉 token 具体数值（可直接照抄的「数字」）

来源：`web/src/styles/kit-base.css`、`kit-accent.css`、`kit-motion.css`、`web/src/style.css`。

**颜色（oklch）**

| token | light | dark |
|---|---|---|
| `--background` | `oklch(1 0 0)` | `oklch(0.145 0 0)` |
| `--foreground` | `oklch(0.145 0 0)` | `oklch(0.922 0 0)` |
| `--card` | `oklch(1 0 0)` | `oklch(0.205 0 0)` |
| `--muted` | `oklch(0.97 0 0)` | `oklch(0.269 0 0)` |
| `--muted-foreground` | `oklch(0.556 0 0)` | `oklch(0.708 0 0)` |
| `--border` | `oklch(0.922 0 0)` | `oklch(1 0 0 / 10%)` |
| `--primary`（indigo accent） | `oklch(0.511 0.23 277)` | `oklch(0.623 0.19 277)` |
| `--success` | `oklch(0.55 0.15 152)` | `oklch(0.7 0.17 152)` |
| `--warning` | `oklch(0.62 0.16 65)` | `oklch(0.8 0.15 75)` |
| `--destructive` | `oklch(0.577 0.245 27.325)` | `oklch(0.704 0.191 22.216)` |
| `--sidebar` | `oklch(0.985 0 0)` | `oklch(0.205 0 0)` |

**字号 / 行高（字阶）**

| token | 值 | 用途 |
|---|---|---|
| `--text-3xs` | `0.625rem`（10px） | 徽标、计数 |
| `--text-2xs` | `0.6875rem`（11px） | 分组小标题、引擎 |
| `--text-ui` | `0.8125rem`（13px） | 界面正文 |
| `--text-caption` | `0.75rem`（12px），line-height 0.75 | 时间戳、附注 |
| `--text-hint` | `0.75rem`（12px），line-height 1.375 | 表单说明 |
| `--text-label` | `0.875rem`（14px） | 表单标签 |

**字体**：`--font-sans: 'Inter Variable','Inter',ui-sans-serif,system-ui,...`（自托管 woff2，`font-display:swap`，离线零网络）；`--font-mono: ui-monospace, SFMono-Regular, "SF Mono", Menlo, Consolas, ...`。

**圆角 / 尺寸 / 间距**：`--radius: 0.625rem`；卡片 `rounded-xl`（12px）；小控件 `rounded-md`；胶囊 `rounded-full`；抽屉 `rounded-t-2xl`；卡片内边距 `px-3.5 py-2.5`；组头 `px-2.5 py-3`；顶栏 `py-3`；内容 `px-4 sm:px-6`。

**动效**：`--dur-fast: 200ms`；`--dur-theme: 280ms`；`--ease-soft: cubic-bezier(0.22, 1, 0.36, 1)`；抽屉 `300ms cubic-bezier(0.4,0,0.2,1)`；collapsible `0.22s/0.20s`；主题切换 crossfade；`prefers-reduced-motion` 关闭装饰动效。

**其它**：`.scroll-slim`（滚动条 8px、thumb 用 `var(--border)`、圆角 999px）；`.kit-expand-*` 用 `opacity + translateY(-4px)` 200ms；Toast（vue-sonner）bottom-center、`duration 3500`、`offset 16`、rich-colors、close-button。

**暗/亮主题**：默认暗色，跟随系统 + 手动切换；所有颜色走 token，无散落的 hardcode 色。

### 1.4 其它同类工具可借鉴点（联网，附来源）

> 说明：以下只提炼「界面/交互」层面的可借鉴点，不展开它们的后端实现。官方未提供界面设计规范的，明确写「未找到」。

| 工具 | 可借鉴点 | 来源 |
|---|---|---|
| **Laravel Herd（Windows）** | 站点即卡片；一键启停数据库/缓存等服务；每个站点可固定 PHP 版本；日志按「项目 + 文件」搜索并可跳转到 IDE 代码行；独立的 dump 调试窗口。**官方逐像素设计规范未找到**。 | https://herd.laravel.com/ ；功能介绍 https://blog.csdn.net/m0_71136328/article/details/137234819 |
| **DDEV** | 官方以 CLI 为主，**未找到官方独立 Web UI**；第三方 `DDEV Manager`（VS Code 扩展）用「左侧栏列出所有项目 + 详情/操作」的骨架，说明「列表 + 操作」是同类工具的主流解法。 | https://github.com/biati-digital/vscode-ddev-manager ；DDEV UI：未找到 |
| **Lando** | **未找到独立 Web 管理台**（官方路径是 CLI + `.lando.yml`）。可借鉴的只有「一个 recipe 对应一个站点、用 `lando start` 一键拉起」的信息架构。 | 未找到 |
| **pm2 / pm2 monitor / pm2.io / pm2-web-ui** | 终端仪表盘 `pm2 monit` 与 Web 面板都属于「一屏多进程」；第三方 Web UI 的卡片含 **CPU% / 内存 MB / 运行时长 / 在线状态**，操作固定为 **Reload（绿）/ Restart（蓝）/ Stop（红）** 三颗语义色按钮；日志区分 STDOUT / STDERR 并支持时间戳；多应用仪表板用卡片网格。 | https://pm2.io/ ；https://pm2.io/docs/enterprise/guides/dashboard ；第三方 UI 介绍 https://blog.csdn.net/gitblog_00019/article/details/139670387 、https://blog.csdn.net/gitblog_01166/article/details/155186141 |
| **lazydocker** | 单终端窗口多面板（项目 / 容器 / 镜像 / 卷 / 网络），底部常驻**快捷键提示栏**；日志页按容器分组、滚动缓冲 + 实时追加 + `/` 搜索 + `n` 跳下一处；可配置主题与边框；`gui.border: rounded/single/double/hidden`、`logs.timestamps`、`logs.since`。 | https://github.com/jesseduffield/lazydocker ；配置项详解 https://blog.csdn.net/gitblog_00874/article/details/151109668 ；日志/搜索行为 https://blog.csdn.net/weixin_33843947/article/details/94601962 |
| **ctop** | 单屏表格：CPU / MEM / NET IO / 容器名 / 状态用颜色区分；可按任意列排序；cgroup 树折叠；低内存时快速定位「谁在吃内存」。**官方设计规范未找到**，仅能拿到交互与界面结构描述。 | https://github.com/bcicen/ctop ；使用说明 https://www.jb51.net/linuxjishu/335707.html |
| **Portainer** | 首页 Dashboard = 端点统计（容器数 / 运行数 / 镜像 / 卷 / 健康）；左侧资源导航（Containers / Images / Networks / Volumes）；容器列表行内可看日志、统计、控制台；详情页集中操作（重启/停止/删除）；列表支持状态筛选。 | https://www.portainer.io/ ；功能与版本特性介绍 https://blog.csdn.net/J_liaty/article/details/156570366 |
| **Orb Stack** | 桌面应用以「应用 / 容器」为中心而非「资源」为中心；**无端口映射**、直接用 `localhost` 访问，UI 强调「点开即用」；**Windows 未发布**（仅 macOS）。 | https://orbstack.dev/ ；对比介绍 https://blog.csdn.net/zhiyuan411/article/details/163247117 |
| **Rancher Desktop** | **未找到官方 UI 设计规范**；公开描述只有「容器 / 镜像列表 + 详情」的桌面客户端形态，可借鉴价值有限。 | 未找到（Rancher 产品页 https://www.rancher.cn/products/rancher/ 不是 Desktop 界面规范） |
| **Homarr / Heimdall / Dashy** | 仪表盘/导航页：卡片网格 + 每张卡一个服务入口；**状态在线检测**（离线变灰/红）；即时搜索（按名称/域/标签）；可拖拽布局与主题编辑；Dashy 有「最小视图」做浏览器起始页、`yaml` 单文件配置。 | Dashy 官网 https://dashy.to/ 、文档 https://dashy.to/docs/ 、Demo https://demo.dashy.to/ ；Homarr/Heimdall 横评 https://post.m.smzdm.com/p/ag5z9o5m/ |
| **Warp 终端** | 「Blocks」概念：每条命令的输入与输出被分块，可跳转 / 复制 / 分享 / 书签；命令面板 + 快捷键优先；语法高亮与纠错；secret 自动打码；分屏。 | https://www.warp.dev/modern-terminal |
| **VS Code 终端** | 终端即面板：标签 + 分屏 + 输出通道（Tasks / Output）分离；终端内搜索框；可固定常用命令。 | https://code.visualstudio.com/docs/terminal/basics |
| **tnyzi** | **未找到**（无法定位该工具，疑为拼写误差）。 | 未找到 |

---

## 2. 现状问题清单

### 2.1 不好用

| # | 问题 | 证据 | 影响 |
|---|---|---|---|
| U1 | **上滚看历史时跟随不会取消**：`scroll` 监听只在「回到底部」时把 `follow` 打开，没有「离开底部就关」，而 `appendLogLine` 里 `if (S.follow) view.scrollTop = view.scrollHeight` 会持续把视图拽回底部 | `app.js:782-790`（只恢复）、`app.js:270`（无条件滚底） | 想看历史日志根本停不住，只能靠清屏，排障体验最差的一条 |
| U2 | **非选中项目的日志被直接丢弃** | `app.js:331-338`（`if (S.selected && pid && pid !== S.selected) return`） | 多项目同时跑时，切走再切回只能重新 `tail=500`，实时日志断档；也无法做多项目日志总览 |
| U3 | **切换项目即清空日志并重拉 500 行** | `app.js:287-298`、`app.js:300-310` | 每次切换都白屏一下；`gap` 提示被清掉；等待网络 |
| U4 | **清屏只清客户端**，切走切回日志又回来，且没有「加载更早历史」 | `app.js:776-779`（仅 `S.logs=[]`） | 用户以为清干净了，实际没有；历史只能靠 `tail=500`，更早的看不到 |
| U5 | **日志无搜索/过滤**（整站无 filter 输入） | `index.html` / `style.css` / `app.js` 全文无搜索框 | 日志一多只能肉眼滚，排障效率低；devwebui `LogDrawer` 有实时过滤 |
| U6 | **危险操作用原生 `confirm()`**（导入、停止、删除三处） | `app.js:660`、`app.js:727`、`app.js:750` | 样式不可控、阻塞主线程、与产品视觉脱节；停止确认文案很长，读起来累 |
| U7 | **详情卡底部 6 颗按钮权重相同**（启动/停止/重启/编辑/打开目录/删除） | `index.html:66-73`、`style.css:287`（`.actions` flex wrap gap 8） | 扫读困难、误触风险高；删除和启动并排 |
| U8 | **列表项信息太少**：只有名称 + 「状态 · :端口」 | `app.js:126-135` | 无法在列表判断命令、框架、运行时长；项目一多只能逐个点开 |
| U9 | **项目列表无搜索/排序/分组** | `index.html:36`（只有 `<ul>`）、`app.js:116-139` | 项目多了找不到；devwebui 顶栏有搜索框 |
| U10 | **元信息网格 `auto-fit minmax(180px,1fr)` 让「命令」折成多段** | `style.css:256-261`；截图 `ui-main-live.png`（命令 code 折行成 3 段） | 命令是核心信息，折行后最难读 |
| U11 | **无「打开项目 URL」入口**：ready 后仍要手输 `127.0.0.1:port` | `app.js:92`（只把状态写成「就绪」）、`index.html:60`（端口只是文本） | 启动成功后还要做一次手工操作 |
| U12 | **操作反馈弱**：点启动只弹「已发出启动指令」toast，按钮无 loading/pending 态 | `app.js:725-730`、`app.js:355-366` | 用户不确定是否点到了；快速连点无保护（虽然后端有状态机） |
| U13 | **状态只有文字胶囊，lifecycle × readiness 两个维度挤在一句话里** | `app.js:81-101`（`statusInfo` 返回长文案）、`app.js:175-177` | 「进程运行中 · 就绪超时」与「启动失败（退出码 3）」要读字，不能一眼扫 |
| U14 | **就绪信息分散**：pill 只写「就绪」，延迟在 meta 的「就绪」行 | `app.js:184`（`readyLatencyMs + ' ms · 已就绪'`） | 想判断「真就绪还是假就绪」要跳着看 |
| U15 | **扫描分组标题用完整路径**（如 `D:\DESKTOP\AITOEARN`），且全大写 | `app.js:558-559`、`style.css:490-498`、截图 `ui-scan-open2.png` | 路径又长又全大写，视觉上像报错；应显示仓库/父目录名 |
| U16 | **扫描行没有「忽略」能力**，重复条目只能不勾 | `app.js:582-624` | 已存在的项目每次扫描都出现，用户必须反复手动跳过 |
| U17 | **导入失败后勾选不清除、计数不更新** | `app.js:684-690`（只显示错误框 + toast，未去掉失败条目） | 用户再点「导入选中」会把失败的条目再导入一遍 |
| U18 | **扫描中无进度、无「已扫 N 个目录」**，只有「扫描中…」 | `app.js:504`、`index.html:162` | 用户不知道要等多久（实测 113ms，但 UI 没说） |
| U19 | **扫描导入的错误框在抽屉最顶部** | `index.html:164`（`#scan-error` 在 `#scan-list` 之上） | 列表一长，错误在视口外，用户以为没反应 |
| U20 | **表单必填星号与 label 视觉分离**（星号被挤到下一行） | `index.html:102`（`<label>项目名称 <span class="req">*</span>`）+ `style.css:393`（`.form label` 是 column flex）；截图 `ui-drawer-open.png` | 星号独占一行，表单显得碎 |
| U21 | **表单字段无分组**：基本设置 / 就绪 / 自启动 / 环境变量混排 | `index.html:101-138` | 字段越多越难定位；devwebui 有 `SettingsGroup` 分组 |
| U22 | **环境变量是裸 textarea，非法行静默丢弃** | `app.js:401-411`（`if (i <= 0) continue`） | 用户不知道哪行被吞了 |
| U23 | **抽屉关闭只有「关闭」按钮 + 遮罩点击 + Esc**，无焦点归还 | `app.js:717-723`、`app.js:792-796` | 键盘用户关抽屉后焦点丢失 |
| U24 | **日志时间戳只取 `HH:MM:SS`** | `app.js:235`（`.slice(11,19)`） | 跨天日志无法分辨日期 |
| U25 | **GBK 开关是全局且无每项目记忆** | `app.js:771-774`、`index.html:82` | 来回切项目要反复勾 |
| U26 | **toast 无上限、无动作按钮、无聚合** | `app.js:46-53` | 连续导入失败时一堆 toast 堆叠 |

### 2.2 不好看

| # | 问题 | 证据 | 影响 |
|---|---|---|---|
| V1 | **字号不成体系**：共 9 档（11.5 / 12 / 12.5 / 13 / 13.5 / 14 / 15 / 16 / 17），无字阶 token | `style.css:62, 82, 87, 123, 158, 164, 192, 205, 225, 226, 241, 297, 318, 340, 390, 393, 411, 525, 529, 549, 556, 592` | 界面像「表单拼贴」，没有统一节奏 |
| V2 | **圆角三套并存**：卡片 10px、按钮/输入 6px、胶囊 999px | `style.css:24-25`（`--radius:10px`、`--radius-sm:6px`）、`style.css:240`（999px）、`style.css:213`（var(--radius)） | 风格不统一，胶囊与卡片冲突 |
| V3 | **阴影几乎不可见**：light 下两层 `.05/.04` 透明度 | `style.css:26`、`style.css:210-216` | 卡片「浮」不起来，界面发平 |
| V4 | **暗色下卡片与背景对比过弱**：`--bg #16181c` vs `--surface #1e2126` | `style.css:32-33`；截图 `ui-main-live.png` | 暗色下边界几乎看不见，层次靠 1px border 硬撑 |
| V5 | **顶栏与侧栏同色**，只靠 1px 边框分隔 | `style.css:75`（topbar bg surface）、`style.css:112`（sidebar bg surface） | 顶栏不像顶栏，没有「层」的感觉 |
| V6 | **详情最大宽 1040px 但内容大量留白**，卡片 padding 16/18 | `style.css:208`、`style.css:215`；截图 `ui-main-live.png` | 宽屏浪费、信息密度低 |
| V7 | **日志区最难读**：灰底 + 12px + `text-dim`，无行 hover、无时间列固定、无行号 | `style.css:332-351` | 日志是核心功能，却被画成「代码块」 |
| V8 | **状态胶囊样式单薄**：soft 底 + 透明边框 + 12px/600 | `style.css:237-253` | 与 devwebui 的 Badge（带边框、字重、尺寸梯度）相比层级弱 |
| V9 | **彩色圆点 9px 且 `margin-top:6px`**，与文字基线不对齐 | `style.css:172-187` | 列表密集时参差，视觉噪点 |
| V10 | **无图标体系**：全站只有一个 SVG logo，其余全是文字按钮 | `index.html:14-18`；其余按钮见 `index.html:23-24, 34, 67-72, 83, 99, 141-142, 153, 159-160, 167` | 缺少扫读锚点，与 devwebui（全套 lucide 图标）差距明显 |
| V11 | **无统一 focus-visible**：只有输入框有 focus 样式，按钮/抽屉无焦点环 | `style.css:406-410` | 键盘 Tab 时看不到焦点，可访问性与精致度都扣分 |
| V12 | **抽屉动画偏快偏小**：`.16s`、位移 24px、无遮罩淡入 | `style.css:378-381` | 与 devwebui 300ms 的「重量感」相比显得廉价 |
| V13 | **空状态只有两行灰字**，无插图、无主行动按钮 | `index.html:37-40`（`#empty-hint`）、`index.html:44-47`（`#placeholder`）；`style.css:190-199, 204-206` | 新用户不知道下一步干什么；devwebui 空态带 Button |
| V14 | **扫描分组标题全大写 + `letter-spacing .5px` + 完整路径** | `style.css:490-498`；截图 `ui-scan-open2.png` | 视觉上像报错文本 |
| V15 | **toast 单薄**：左侧 3px 色条、无图标、右下堆叠 | `style.css:624-638` | 提示存在感弱，且与抽屉/日志区无关联 |
| V16 | **数字无 `tabular-nums`**（只有 `.counter` 有），运行时长每秒跳动 | `style.css:319`；`app.js:800-809`（1s ticker） | 数字宽度抖动，界面「不稳」 |
| V17 | **列表 active 与 hover 区分度低**：active 用 accent-soft，hover 用 surface-2 | `style.css:149-153` | 当前选中项不突出 |
| V18 | **表单三级灰阶接近**：label 12.5px / input 13px / hint 11.5px | `style.css:393-411` | 层级弱，眼睛要费力分辨 |
| V19 | **卡片标题字号落差大**：详情 h2 17px、日志 h3 14px | `style.css:225-226` | 同一屏内两个卡片的标题不成系统 |
| V20 | **响应式只有一档**：`max-width:900px` 把侧栏改成 220px | `style.css:640-642` | 小屏/笔记本侧栏仍占地方；无移动形态 |

---

## 3. 设计方案建议（1 个推荐方案）

### 3.0 方案一句话

**把「左列表 + 右详情（详情里塞日志）」改成 devwebui 同款的「顶栏 + 居中单列项目卡 + 右侧全高 push 抽屉（日志/扫描/表单/设置）」**，用一整套可命名的视觉 token 重绘，并顺手修掉日志跟随、信息密度、确认弹窗、空状态、扫描分组这些具体毛病。

选它的三个理由：
1. 与 devwebui 骨架一致 → §1 的组件与数值可以近乎照抄，设计风险最低；
2. 日志从「52vh 的详情卡附属区」升级为「全高抽屉」，直接解决 U1/U4/U5/V7 一组最差体验；
3. 项目卡本身承载状态/命令/端口/时长，列表信息密度问题（U8/U9/V6）一并解决。

> 备选方案（不推荐，仅作对照）：保留左右双栏，只把日志区撑满 + 加搜索。改动小，但列表信息密度、扫描分组、空状态、视觉 token 都要再改一遍，等于两次施工。

### 3.1 布局骨架（目标）

```
┌──────────────────────────────────────────────────────────────────┐
│ TopBar（sticky, backdrop-blur, 内容 max-width 800 居中）           │
│  ◆ DevDock   ● Live 3/5   [搜索项目…]   + 新建   扫描  刷新  ⋮    │
├──────────────────────────────────────────────────────────────────┤
│ 主列 max-width 800px，居中，gap 12px                               │
│  ┌ 项目卡（rounded-lg border bg-surface，padding 12/14）─────────┐ │
│  │ ● 项目名（就绪时可点 → http://127.0.0.1:5173）      [状态pill] │ │
│  │   `npm run dev`（mono，单行省略）  ·  :5173 · 运行 12分03秒     │ │
│  │                            [▶ 启动] [■ 停止] [↻] [日志] [⋮]    │ │
│  └──────────────────────────────────────────────────────────────┘ │
│  ┌ 项目卡 2（stopped，整卡 opacity .75）───────────────────────┐   │
│  …                                                               │
├──────────────────────────────────────────────────────────────────┤
│ 右侧 push drawer（宽 min(560px, 44vw)，全高，300ms）              │
│  · 日志抽屉：项目名 + 命令 + 状态 + 工具条 + 日志流                 │
│  · 扫描抽屉：根目录 + 工具 + 分组结果 + 底部导入条                  │
│  · 表单抽屉：新建/编辑（分组表单）                                 │
│  · 设置抽屉（可选，二期）                                          │
└──────────────────────────────────────────────────────────────────┘
```

要点：
- **单列**：内容列 `max-width: 800px`，`margin: 0 auto`，横向 padding `16px`（≥1280）/`12px`（<1280）/`8px`（<720）。
- **项目卡即单位**：DevDock 的一个 project = 一个启动配方（进程），所以不再有「分组 + 进程」两层；项目多时用顶栏搜索过滤。
- **右抽屉互斥**：同时只开一个（日志/扫描/表单/设置），开新的先关旧的；抽屉是**临时任务**，不长期占据主区。
- **列表与详情合一**：卡片上直接给启停/日志入口，点开日志进右抽屉。

### 3.2 信息层级

| 层级 | 内容 | 载体 |
|---|---|---|
| L0 全局 | 连接状态、运行中计数、搜索、新建、扫描 | 顶栏 |
| L1 项目 | 状态点 + 名称 + 状态 pill + 端口 + 运行时长 + 命令片段 | 项目卡头部 |
| L2 操作 | 启动/停止/重启/日志/更多 | 项目卡底部操作行 |
| L3 详情 | 完整命令、cwd、PID、就绪延迟、错误输出 | 抽屉（点「详情」或卡头） |
| L4 日志 | 时间戳流 + 过滤 + 跟随 | 右抽屉（默认主用抽屉） |
| L5 批量 | 扫描结果、导入 | 右抽屉（扫描） |
| L6 危险 | 删除、全部停止 | 自绘确认弹窗 |

### 3.3 组件清单（照此写 HTML 结构）

```html
<!-- 顶栏 -->
<header class="topbar">
  <div class="brand"><svg class="logo">…</svg><span class="title">DevDock</span></div>
  <div class="live" id="live"><span class="live-dot"></span><span id="live-text">Live · 3/5 运行中</span></div>
  <div class="topsearch"><input id="search" placeholder="搜索项目…"></div>
  <div class="topactions">
    <button class="btn primary" id="btn-new">+ 新建</button>
    <button class="btn" id="btn-scan">扫描本机</button>
    <button class="btn ghost" id="btn-refresh">刷新</button>
    <button class="btn icon" id="btn-menu" aria-label="更多">⋮</button>
  </div>
</header>

<!-- 主列 -->
<main class="container">
  <div class="project-list" id="project-list"></div>
  <div class="empty" id="empty-hint" hidden>…（见 3.7）</div>
</main>

<!-- 项目卡（JS 用 el() 生成，禁止 innerHTML） -->
<li class="project-item">
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
    </div>
  </div>
  <div class="p-actions">
    <button class="btn primary sm" data-act="start">启动</button>
    <button class="btn sm" data-act="stop">停止</button>
    <button class="btn sm" data-act="restart">重启</button>
    <button class="btn sm" data-act="logs">日志</button>
    <button class="btn icon sm" data-act="more" aria-label="更多">⋮</button>
  </div>
</li>

<!-- 右抽屉（日志/扫描/表单共用外壳） -->
<div class="drawer-mask" id="log-mask" hidden>
  <aside class="drawer" role="dialog" aria-modal="true">
    <header class="drawer-head">
      <div class="drawer-title"><h2 id="log-title">前端 dev</h2><code id="log-cmd">npm run dev</code></div>
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
    <footer class="log-foot"><span id="log-count">0 行</span><span id="log-follow-hint">已取消跟随（上滚查看历史）</span></footer>
  </aside>
</div>
```

> 保留现有 `id`（`btn-scan`、`scan-mask`、`scan-roots`、`scan-start`、`scan-cancel`、`scan-list`、`scan-summary`、`scan-import`、`scan-more`）以满足 `run_scan_e2e.ps1:180-184` 的静态断言。

### 3.4 视觉 token 表（照此写 CSS 变量）

在现有 `:root` 基础上重构，保留 `--accent` 蓝色系（品牌色），把「9 档字号 + 3 套圆角 + 看不见的阴影」收敛成下面的体系：

```css
:root {
  /* 颜色：保留现有 hex 体系，只补层次（也可整体换 oklch，见 §4 可选） */
  --bg:        #f6f7f9;
  --surface:   #ffffff;
  --surface-2: #f4f6f8;   /* 比原来的 #fbfbfc 深一点，暗色下才有层次 */
  --border:    #e3e6ea;
  --border-strong: #d0d5db;
  --text:      #1c2024;
  --text-dim:  #5c656e;
  --text-mute: #8a939c;

  --accent:      #2f6feb;
  --accent-soft: #e8f0fe;
  --ok:          #1a9d5a;  --ok-soft:  #e6f6ec;
  --warn:        #c77700;  --warn-soft:#fdf3e0;
  --danger:      #d13438;  --danger-soft:#fdecec;

  /* 字阶：6 档，禁止再出现 11.5/12.5/13.5 */
  --text-2xs: 11px;   /* 徽标、附注 */
  --text-xs:  12px;   /* 时间戳、说明 */
  --text-sm:  13px;   /* 卡片正文 */
  --text-ui:  14px;   /* 默认 */
  --text-md:  16px;   /* 页面标题 */
  --text-lg:  18px;   /* 大标题（少用） */

  /* 圆角：3 档 */
  --radius-sm: 6px;   /* 按钮、输入、小卡片 */
  --radius-md: 10px;  /* 卡片、抽屉 */
  --radius-lg: 14px;  /* 大面板、弹窗 */
  --radius-pill: 999px;

  /* 间距：4px 基数 */
  --sp-1: 4px; --sp-2: 8px; --sp-3: 12px;
  --sp-4: 16px; --sp-5: 20px; --sp-6: 24px;

  /* 阴影：能看见 */
  --shadow-sm: 0 1px 2px rgba(16,24,40,.06), 0 2px 8px rgba(16,24,40,.05);
  --shadow-md: 0 2px 4px rgba(16,24,40,.08), 0 12px 32px rgba(16,24,40,.12);
  --shadow-lg: -12px 0 40px rgba(16,24,40,.18);

  /* 动效 */
  --dur-fast: 160ms;
  --dur-base: 240ms;
  --ease-out: cubic-bezier(.22, 1, .36, 1);

  --mono: ui-monospace, "Cascadia Mono", Consolas, "Courier New", monospace;
}

@media (prefers-color-scheme: dark) {
  :root {
    --bg:        #14161a;   /* 比 surface 更深，拉开层次 */
    --surface:   #1d2026;
    --surface-2: #262a31;
    --border:    #2e333b;
    --border-strong: #3c424c;
    --text:      #e6e9ed;
    --text-dim:  #a4adb8;
    --text-mute: #767f8a;
    --shadow-sm: 0 1px 2px rgba(0,0,0,.4), 0 2px 8px rgba(0,0,0,.3);
    --shadow-md: 0 2px 4px rgba(0,0,0,.5), 0 12px 32px rgba(0,0,0,.4);
    --shadow-lg: -12px 0 40px rgba(0,0,0,.5);
  }
}
```

组件数值（写 CSS 时照抄）：

| 组件 | 数值 |
|---|---|
| 顶栏 | `height 52px; padding: 0 16px; position: sticky; top:0; background: color-mix(in srgb, var(--surface) 82%, transparent); backdrop-filter: blur(10px); border-bottom: 1px solid var(--border)` |
| 主列 | `max-width: 800px; margin: 0 auto; padding: 16px; display: flex; flex-direction: column; gap: 12px` |
| 项目卡 | `background: var(--surface); border: 1px solid var(--border); border-radius: var(--radius-md); box-shadow: var(--shadow-sm); padding: 12px 14px; display: flex; gap: 12px; align-items: flex-start` |
| 状态点 | `width: 8px; height: 8px; border-radius: 50%; margin-top: 6px`（与 13px 文字基线对齐） |
| 卡片标题 | `font-size: var(--text-sm); font-weight: 650` |
| 卡片 meta | `font-size: var(--text-xs); color: var(--text-mute); display:flex; gap: 10px; flex-wrap: wrap` |
| 命令 code | `font-family: var(--mono); font-size: var(--text-xs); background: var(--surface-2); border: 1px solid var(--border); border-radius: 4px; padding: 1px 6px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 100%` |
| 按钮 | `height: 30px; padding: 0 12px; font-size: var(--text-sm); border-radius: var(--radius-sm); border: 1px solid var(--border-strong); background: var(--surface); transition: background var(--dur-fast) var(--ease-out), border-color var(--dur-fast)` |
| 主按钮 | `background: var(--accent); border-color: var(--accent); color: #fff` |
| 危险按钮 | `color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent)` |
| 胶囊 | `font-size: var(--text-2xs); font-weight: 600; padding: 2px 8px; border-radius: var(--radius-pill); border: 1px solid transparent` |
| 抽屉 | `width: min(560px, 44vw); height: 100%; background: var(--surface); box-shadow: var(--shadow-lg); animation: slide-in var(--dur-base) var(--ease-out)` |
| 抽屉动画 | `@keyframes slide-in { from { transform: translateX(32px); opacity: 0 } to { transform: none; opacity: 1 } }` |
| 数字 | `font-variant-numeric: tabular-nums`（运行时长、端口、计数） |
| 焦点 | `:focus-visible { outline: 2px solid var(--accent); outline-offset: 2px; border-radius: var(--radius-sm) }`（按钮、卡片、抽屉都生效） |
| 滚动条 | 8px 宽，thumb 用 `var(--border-strong)`，圆角 999px |

### 3.5 日志区设计（重点）

**结构**：右抽屉，全高；从上到下 = 头部 / 工具条 / gap 条 / 日志流 / 底部状态条。

**工具条**（全部用 `el()` 生成）：
- 过滤输入：`#log-filter`，`input` 事件实时过滤 `S.logs`，命中关键词用 `<mark>` 高亮（注意 CSP 与 `el()` 约束，用 `textContent` 拼 `mark`）；
- 「仅看错误」复选：只显示 `stream === 'err'`；
- 「时间戳」复选：切换 `.ts` 的 `display`；
- 「GBK」复选：沿用现有 `decodeMaybeGBK`；
- 「跳到底部」「复制全部」（`navigator.clipboard`，失败回退 `textarea + execCommand`）、「清屏」。

**日志流**：
- 行结构不变（`span.ln` + `span.ts` + 文本），但 `.ts` 固定宽度 `7ch`（`HH:MM:SS`），`user-select: none`；
- `.ln` 加 `padding: 0 6px; border-radius: 3px`，`:hover { background: var(--surface-2) }`；
- `stderr` 用 `--danger`，并在行首加一个 `!` 标记（可选）；
- 历史行（`seq < 首屏 seq`）加 `.hist { opacity: .7 }`；
- 空态区分：`未启动，暂无日志` / `暂无输出` / `没有匹配「xxx」的日志`（三句不同）。

**跟随逻辑（必须修）**：
```
// 上滚离开底部 → 取消 follow；回到底部 → 恢复 follow
onScroll:
  atBottom = scrollHeight - scrollTop - clientHeight < 24
  if (atBottom && !follow) follow = true
  if (!atBottom && follow) follow = false     // ← 现在缺这一行
  同步复选框 UI + 底部提示「已取消跟随」
appendLogLine:
  if (follow) scrollTop = scrollHeight
```
对应改动：`app.js:782-790` 补 `if (!atBottom) { chk.checked = false; S.follow = false; }`。

**分页（可选，P1）**：`loadLogs` 支持 `before=<seq>&limit=500`，在顶部插行时记录 `prevHeight/prevTop` 保持滚动位置（照 devwebui `LogDrawer.vue`）。

### 3.6 扫描结果呈现

- **分组标题**：显示 `group` 的末段（仓库/父目录名），完整路径放 `title` 提示。取末段用 `group.split(/[\\/]/).filter(Boolean).pop()`；证据修 `app.js:559`。
- **行结构**：复选框 + 名称 + `framework` 徽章 + `confidence` 徽章（high/medium/low 三色）+ 端口徽章 + 命令（mono，单行省略）+ 路径（末段，`title` 全路径）+ 警告行 + 操作（`填入表单` / `忽略`）。
- **忽略**：新增 `S.scan.ignored = Set<idx>`，行变 `opacity .45` + `IGNORED` 徽章，hover 出「撤销」；不新增后端字段，纯 UI 状态。
- **汇总条**（抽屉底部固定）：`扫描 N 个目录 · Nms · 共 N 条 · 已选 M`；超时/截断时加 `--warn` 文案。
- **导入失败**：失败条目保留并标红（加 `.scan-row.failed`），勾选**去掉**，计数刷新，错误框移到列表底部（`#scan-error` 放 `#scan-list` 之后）；修 `app.js:684-690`。
- **默认勾选**：保留现有条件（`run_scan_e2e.ps1:205` 有正则断言，不能改语义），但把「包含 low」勾选框的文案改成「显示 low 置信度」以避免与「默认勾选」混淆。

### 3.7 空状态与引导

| 场景 | 现状 | 建议 |
|---|---|---|
| 无项目 | 两行灰字（`index.html:37-40`） | 居中卡片：图标 + 标题「还没有项目」+ 说明 + 主按钮「+ 新建项目」+ 次按钮「扫描本机」+ 一行示例 `npm run dev :5173` |
| 未选中项目 | 占位两行（`index.html:44-47`） | 单列方案下删除该占位（列表即全部） |
| 搜索无结果 | 不存在 | 「没有匹配的项目」+ 「清除搜索」 |
| 日志无输出 | 只有一行灰字 | 三态文案（未启动 / 暂无输出 / 无匹配） |
| 扫描无结果 | 有（`app.js:545-549`） | 保留并加「手动新建」按钮（devwebui 的 `#empty` slot 做法） |
| SSE 断开 | 顶栏 summary 变「连接中断，正在重连…」 | 顶栏 Live 点变灰 + 文字「连接中断」，恢复后自动刷新并变绿 |

### 3.8 关键交互与反馈

1. **按钮 pending 态**：点击启动/停止/重启后，按钮加 `.is-loading`（CSS 旋转环，纯 `border` 实现），`disabled` + `aria-busy="true"`；SSE 状态事件到达后恢复。禁止用 `innerHTML`，用 `textContent` + class。
2. **确认弹窗自绘**：`<div class="dialog-mask" hidden><div class="dialog" role="alertdialog">…`，含标题 / 描述 / 取消 / 确认（危险色）。Esc 关闭、点遮罩关闭、打开时 focus 确认钮、关闭后焦点归还触发钮。
   - ⚠️ `run_scan_e2e.ps1:209` 断言 `app.js` 含 `confirm(`，改自绘弹窗必须同步改脚本（见 §5）。
3. **行内快捷操作**：项目卡 hover 时操作行从 `opacity:.55` 变 `1`；键盘 Tab 仍可聚焦。
4. **打开 URL**：`readiness === 'ready'` 时项目名渲染成 `<a href="http://127.0.0.1:{port}" target="_blank" rel="noreferrer">`，带 ExternalLink 图标（hover 显影，照 devwebui `ProcessCard.vue`）。
5. **数字稳定**：运行时长、端口、计数统一 `font-variant-numeric: tabular-nums`。
6. **toast 增强**：加图标（成功/错误/信息），最多同时 3 条，超出移除最旧；错误 toast 带「详情」按钮（可选）。
7. **抽屉焦点**：打开时 focus 第一个可交互元素，关闭后 `focus()` 回触发元素；Esc 只关最上层。
8. **刷新**：SSE 已覆盖实时状态；「刷新」按钮保留为手动兜底，加旋转图标反馈。

---

## 4. 取舍清单

### 4.1 必改（P0，不做就还是「不好用/不好看」）

| # | 项 | 落点 | 理由 | 成本 |
|---|---|---|---|---|
| P0-1 | 日志跟随逻辑修复（上滚取消、回底恢复） | `app.js:782-790` | 当前根本没法看历史日志，是最硬的伤 | 0.5h |
| P0-2 | 日志区从详情卡附属升级为全高右抽屉 + 工具条 | `index.html` / `style.css` / `app.js` | 解决 U3/U4/U5/V7 一组问题 | 1–2 天 |
| P0-3 | 视觉 token 统一（字阶 6 档、圆角 3 档、阴影可见、暗色拉开层次） | `style.css:5-52` | 解决 V1–V5、V8、V11、V18、V19 | 0.5–1 天 |
| P0-4 | 项目卡信息密度（命令片段 + 端口 + 运行时长 + 状态点） | `index.html` / `app.js:116-139` | 解决 U8，减少无效点击 | 0.5 天 |
| P0-5 | 详情元信息改固定列（命令独占一行、不折行） | `style.css:256-272` | 解决 U10 | 0.5h |
| P0-6 | 表单排版修复（星号同行、分组、hint 层级） | `index.html:101-138` / `style.css:392-412` | 解决 U20/U21/V18 | 0.5 天 |
| P0-7 | 扫描分组标题取末段 + 徽章体系统一 | `app.js:558-559, 600-607` | 解决 U15/V14 | 0.5h |
| P0-8 | 空状态加主行动按钮 | `index.html:37-47` / `app.js` | 解决 V13，直接影响「第一印象」 | 0.5h |
| P0-9 | 数字 `tabular-nums` | `style.css` | 解决 V16 | 10 分钟 |
| P0-10 | 确认弹窗自绘（停止/删除/导入） | `app.js:660,727,750` + 新组件 | 解决 U6/V8；**需同步改 e2e 断言** | 0.5–1 天 |
| P0-11 | 非选中项目日志改后台缓存（切回不重拉、不断档） | `app.js:331-338` | 解决 U2/U3 | 0.5 天 |
| P0-12 | 焦点环 / focus-visible / 抽屉焦点归还 | `style.css` / `app.js` | 解决 V11/U23 | 0.5h |

### 4.2 可选（P1，做了更好）

| 项 | 理由 | 成本 |
|---|---|---|
| 日志搜索/过滤（关键词 + 仅看错误） | 排障效率；devwebui 有 | 0.5 天 |
| 日志历史分页（500 行以上） | 长跑项目 | 0.5 天 |
| 项目搜索/排序 | 项目多了必要 | 0.5 天 |
| 表格 / 卡片双视图切换 | devwebui 有；DevDock 项目少，收益一般 | 1 天 |
| 扫描忽略 + 撤销 | 减少重复条目噪音 | 0.5 天 |
| 复制日志 / 下载日志 | 排障 | 0.5 天 |
| 通知中心（Toast 聚合） | 多事件时有用 | 1 天 |
| 亮/暗主题手动切换 | 现在只跟随系统 | 0.5 天 |
| 颜色迁到 oklch | 与 devwebui 一致，但 hex 够用 | 1 天（收益低） |

### 4.3 不做（明确排除，避免范围膨胀）

| 项 | 理由 |
|---|---|
| 自定义主题 / 多主题色 | `outputs/PLAN.md` §10 Non-Goals 已明确不做 |
| 国际化 | 同上；界面全中文是产品定义 |
| 日志正则高亮 / 导出 | 同上 |
| 资源监控图表（CPU/内存曲线） | 同上；DevDock 是进程管理器不是监控面板 |
| 移动端原生体验 | 只做 ≥1280 / ≥900 / <900 三档响应式；不做手机专属布局 |
| 托盘图标 / 自动更新 / 云同步 | PLAN §11 首版不做 |
| 新增 web 静态文件 | `web/embed.go` 是显式文件名列表，红线（AUTO_DETECT_SPEC §B） |
| 多项目日志聚合视图 | 超出首版范围；先修好单项目日志 |
| 把 `confirm()` 全删掉而不同步 e2e | 会直接挂 `run_scan_e2e.ps1:209`；要么改脚本要么保留 |

---

## 5. 风险与验证建议

### 5.1 会被这次改动打破的现有断言

`work/testsupport/e2e/run_scan_e2e.ps1:166-213`（A11 段）是**静态正则断言**，逐条对照本方案：

| 断言（行号） | 断言内容 | 本方案影响 | 处理 |
|---|---|---|---|
| `:169-177` | `/`、`/index.html`、`/style.css`、`/app.js`、`/favicon.svg` 返回 200 | 只要不新增/改名静态文件就无影响 | 保持文件名 |
| `:180-184` | index.html 必须含 `id="btn-scan"` `scan-mask` `scan-roots` `scan-start` `scan-cancel` `scan-list` `scan-summary` `scan-import` | 扫描抽屉若重写，这些 id **必须保留** | 保留（§3.3 已注明） |
| `:186-191` | `scan-mask` 必须复用 `.drawer-mask` 类 | 若把扫描改成右 push 抽屉且换类名会挂 | 保留 `.drawer-mask`，或在脚本里同步加新类名 |
| `:193-195` | `style.css` 必须含 `.drawer-mask[hidden]` | 若抽屉改类名会挂 | 保留该规则；**任何新增带 `display:flex/grid` 的类名都要补 `[hidden]`**（历史 Bug 根因，见 `style.css:363-368`、`597-610`） |
| `:197-201` | app.js 必须含 `function openScan(` `closeScan(` `runScan(` `importSelected(` `fillFromSuggestion(` | 函数名不能改 | 保留函数名（扫描逻辑可重构内部，别改签名） |
| `:202-203` | app.js 不得出现 `innerHTML` | 高亮日志命中若用 `innerHTML` 会挂 | 用 `el('mark', null, kw)` + `textContent` |
| `:205-206` | 默认勾选条件正则 `s\.confidence\s*!==\s*'low'` 且 `s\.port\s*>\s*0\s*\|\|\s*!!s\.framework\s*\|\|\s*s\.confidence\s*===\s*'high'` | 不能改默认勾选语义 | 原样保留 `app.js:593-594` 的表达式 |
| `:207-208` | `picks\.length\s*>\s*20` | 单批上限不能改 | 保留 |
| `:209-210` | app.js 含 `confirm(` | **P0-10 改自绘弹窗会挂** | 二选一：① 脚本改成断言新弹窗（如 `el('div','dialog-mask'`）；② 保留导入路径的 `confirm(` |
| `:211-213` | `scan-more` + `items\.slice\(0,\s*5\)` | 组内折叠不能改 | 保留 |

`run_e2e.ps1`（335 行）**没有任何 web 文件静态断言**（只做 CLI/HTTP 生命周期），本方案不影响它，但它会重新编译并跑完整链路，仍需 `E2E_RESULT=PASS`。

### 5.2 其它风险

| 风险 | 说明 | 缓解 |
|---|---|---|
| 「抽屉关不掉」历史 Bug 复现 | 根因是新增类名带 `display:flex/grid` 而没补 `[hidden]{display:none}`（`style.css:363-368` 注释已写明） | 每加一个 flex/grid 类名，同步加 `[hidden]` 规则；把它写进开发自检清单 |
| 布局大改导致人工验收记录失效 | `docs/VERIFICATION.md` 里三张 `ui-*.png` 与「paw browser 点击 #btn-scan → #scan-mask.hidden=false」的记录基于旧布局 | 改完重新用 paw-browser 走查并更新 `VERIFICATION.md`（该文件由主 agent 统一更新，调研 agent 不写） |
| SSE 断线处理被误改 | `app.js:348-351` 现在只改 summary 文案；若顺手加重连逻辑要保留「不刷新整页」 | 只改 UI 层，不动 `connectSSE` 的重连语义 |
| 日志客户端上限 3000 行 + 全量重渲染 | `renderLogs()` 每次 `view.textContent=''` 全量重建；日志量大会卡 | 过滤/GBB 切换时才全量重建；`appendLogLine` 保持增量 |
| 后端 API 不变，前端改动面广 | 本方案不动 `/api/*`；但 `loadLogs` 若要加分页参数需后端支持 | 分页列为 P1，先确认后端有 `before` 参数再做 |
| 视觉改动的可回归性 | 没有视觉快照测试 | 用固定 fixture（`testsupport/fixture`）+ 固定暗色截图，人工对比；或后续引入 Playwright（超出首版） |

### 5.3 建议的验证清单

1. **静态**：`go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全部 0（不得退化）。
2. **e2e**：
   - `.\testsupport\e2e\run_e2e.ps1` → `E2E_RESULT=PASS`；
   - `.\testsupport\e2e\run_scan_e2e.ps1` → `SCAN_E2E_RESULT=PASS`（若动了 `confirm(` / `.drawer-mask` / id，先同步脚本断言）。
3. **真实浏览器走查**（主 agent 用 paw-browser，照 `docs/VERIFICATION.md` 的既有做法）：
   - 打开页面 → 默认选中第一个项目 → 启动 → 状态变「就绪」→ 名称可点；
   - 点「日志」→ 右抽屉打开 → 上滚 → 底部提示「已取消跟随」→ 新日志不再拽底 → 滚回底部恢复跟随；
   - 过滤框输入关键词 → 命中高亮、无匹配文案正确；
   - 打开扫描 → 分组标题是仓库名不是完整路径 → 勾选 → 导入 → 计数正确；
   - 停止/删除 → 自绘确认弹窗 → Esc 关闭、焦点归还；
   - 暗色主题下卡片/抽屉边界清晰；
   - 抽屉打开时 Tab 焦点不跑出抽屉。
4. **截图归档**：改完重截 `ui-main-live.png` / `ui-drawer-open.png` / `ui-scan-open2.png` 三张，覆盖旧图（文件名不变，便于对照）。

---

## 附：本文引用的 devwebui 文件（只读，未修改）

```
web/src/AppShell.vue
web/src/AppContainer.vue          （位于 shell/AppContainer.vue）
web/src/shell/AppContainer.vue
web/src/shell/Sidebar.vue
web/src/shell/SettingsGroup.vue
web/src/shell/SettingsRow.vue
web/src/components/ProcessCard.vue
web/src/components/ProcessTable.vue
web/src/components/LogDrawer.vue
web/src/components/ScanResults.vue
web/src/components/RightDrawer.vue
web/src/components/TopBar.vue
web/src/components/ProjectPanel.vue
web/src/components/FocusView.vue
web/src/components/IconButton.vue
web/src/components/Hint.vue
web/src/styles/kit-base.css
web/src/styles/kit-accent.css
web/src/styles/kit-motion.css
web/src/style.css
docs/screenshot.png / docs/logs.png / docs/light.png
```

> 注：任务描述里的 `shell/AppShell.vue` 实际不存在，该组件位于 `web/src/AppShell.vue`（已按此读取）。
