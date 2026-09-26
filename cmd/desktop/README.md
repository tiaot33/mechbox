# 桌面端

技能管理器的桌面端：Wails v3 应用，Go 进程直接持有技能库（Library），React 前端经 Wails 绑定调用它（ADR 0013）。macOS 一等支持，Windows / Linux 只保证能编译。

以下命令都在 `cmd/desktop/` 目录下执行。

## 功能

桌面端覆盖技能库(Library)的全部功能，界面按 UI 原型变体 A 组织：

| 界面位置 | 功能 |
| --- | --- |
| 左栏"安装技能" | 从本地目录、GitHub(`owner/repo`、`owner/repo@技能名`、github.com 链接)、URL 压缩包、商店安装;远程来源先获取候选(含 skills.sh 审计与诊断)再勾选安装 |
| 左栏"技能市场" | 搜索 skills.sh,选中后获取并安装 |
| 左栏筛选 | 所有技能、有可用更新(可一次全部更新)、检查更新、按来源的集合、组(新建 / 重命名 / 删除，悬停组行出现按钮) |
| 技能卡片 → 详情抽屉 | 元数据、更新检查状态、加入 / 移出组、各作用域的激活与投影状态(开关、单条重写、激活到…)、文件浏览(SKILL.md 渲染)、版本历史(与当前比较、查看旧版文件)、检查更新、重新读取来源、重写全部投影、移除技能 |
| 右栏作用域 | 全局与已登记项目下的各 Agent;登记 / 注销项目;点选即选中作用域 |
| 选中作用域后 | 卡片上的独立激活开关、列表顶部的投影目录与健康状态、全部重写、各组的整组激活开关、"仅已激活"筛选 |
| 拖放 | 技能卡片 → 右栏作用域 = 激活;技能卡片 → 左栏组 = 加入;组 → 右栏作用域 = 整组激活 |
| 左栏"同步" | 整库推送到 S3 / 从 S3 拉取(先预览远端再确认) |
| 左栏"设置" | 技能库位置、内置 Agent、外部配置(环境变量)是否就绪 |
| 顶栏 | 作用域胶囊、搜索(⌘K)、列表 / 卡片、刷新(重新读取并对照磁盘检查投影) |

冲突按技能库的约定处理：激活前预检，目标有未记录的外来文件夹时询问是否覆盖，被另一条投影占用或作用域根目录不存在时说明原因;整组激活、加入组、重写逐条返回结果，被跳过或失败的条目列出原因;移除技能、注销项目、移出组时让用户决定磁盘上投影的去留。

## 准备

| 依赖 | 说明 |
| --- | --- |
| Go | 版本见根目录 `go.mod` |
| Xcode Command Line Tools | macOS 上编译 cgo 需要（`xcode-select --install`） |
| Node.js + pnpm | 前端工具链；依赖在构建时自动 `pnpm install` |
| wails3 CLI | 必须和 `go.mod` 里钉住的 Wails 版本一致：`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.25` |

`go install` 把 `wails3` 装到 `$(go env GOBIN)`，未设置时装到 `$(go env GOPATH)/bin`；这个目录要在 `PATH` 里。装好后可用 `wails3 doctor` 检查环境。

## 开发

```sh
wails3 dev
```

它会生成绑定、启动 Vite 开发服务器（端口 9245），再编译并打开开发版应用 `bin/skills-manager.dev.app`：

- 改前端（`frontend/src/`）：窗口热更新，不重启。
- 改 `cmd/desktop/` 下的 Go 代码：自动重新生成绑定、重新编译并重启应用。
- 改 `internal/library/`：不在监听范围内（监听根目录是 `cmd/desktop/`），要 Ctrl-C 后重新 `wails3 dev`。
- 按 Ctrl-C 结束。如果异常退出后再启动报 `address already in use`，是上次的 Vite 还占着端口：`lsof -ti tcp:9245 | xargs kill`。

开发版打开的是**真实技能库**（见下文“运行时行为”），激活操作也会写进真实的 Agent 目录。想用一个隔离的库调试，就给它一个临时 `HOME`；技能库位置和 Agent 目录都从 `HOME` 推导，所以两者都会落在临时目录里。Go 缓存也默认跟着 `HOME` 走，一并指回原处，免得重新下载依赖：

```sh
HOME=/tmp/skills-dev GOMODCACHE=$(go env GOMODCACHE) GOCACHE=$(go env GOCACHE) wails3 dev
```

## 构建与打包

```sh
wails3 build     # 只编译二进制：bin/skills-manager
wails3 package   # 打成 macOS 应用：bin/skills-manager.app（ad-hoc 签名）
open bin/skills-manager.app
```

`bin/` 不入库。

**毛玻璃需要 `private_mac_apis` 构建标签。** 窗口背后是 macOS 原生毛玻璃，但要透出来，WebView 本身必须透明；Wails v3 只有带这个标签时才会把 WebView 设成透明，否则 WebView 画白底，把毛玻璃整个挡住。`Taskfile.yml` 已默认加上（`EXTRA_TAGS`），`wails3 dev` / `build` / `package` 都会带。代价是用了私有 API，不能上架 Mac App Store。直接 `go build ./cmd/desktop` 不带这个标签，也能运行，只是没有毛玻璃。

## 运行时行为

- **技能库位置**：`<用户配置目录>/skills-manager/library.db`，macOS 上是 `~/Library/Application Support/skills-manager/library.db`。首次启动时创建目录，由 Library 初始化空库。它只依赖 `HOME`，所以从 Finder / Dock 启动也能找到。
- **单实例**：同一时间只运行一个实例。再次启动时，新进程直接退出，把已有窗口带到前台（最小化了也会恢复）。单实例检查早于打开技能库，第二个进程不会碰库文件。

## 检查

```sh
go test ./...                              # 仓库根目录；含 cmd/desktop 的测试
go vet ./...
cd frontend && pnpm exec tsc               # 前端类型检查
cd e2e && pnpm install && pnpm test        # 端到端验证(见下)
```

`go test` 里的 `TestLibraryServiceExposesEveryLibraryMethod` 保证 `library.Library` 的每个方法都开放给了前端：Library 新增方法而绑定服务没跟上时，这个测试会失败。

### 端到端验证

`e2e/run.sh` 把桌面端编译成 Wails 的 server 模式(同一套前端与绑定，以 HTTP 服务运行、不开窗口),在临时 `HOME` 里放好测试技能、项目目录和一个 zip,然后用本机的 Google Chrome 按界面操作走一遍：安装(本地 / URL / 商店未配置)、分组、登记项目、激活与整组激活、冲突询问、重写、检查更新与更新、版本比较、拖放、同步未配置、设置、移除与注销，并核对磁盘上的投影。

```sh
cd e2e
pnpm install
pnpm test                    # 离线步骤
E2E_NETWORK=1 pnpm test      # 连同技能市场与 GitHub 安装(需要联网)
pnpm test 重写               # 只跑名字包含"重写"的步骤(步骤之间有依赖，单跑可能缺前置状态)
```

失败的步骤会截图到 `/tmp/skills-e2e-shots/`。server 模式没有原生窗口，所以系统目录选择框、窗口毛玻璃与单实例不在这里验证，需要在 `wails3 dev` 里手动确认。

`frontend/dist/` 里提交了一个占位文件 `.gitkeep`（源文件是 `frontend/public/.gitkeep`，每次 Vite 构建都会复制过去），所以没构建前端时 `go build/vet/test ./...` 也能通过，纯 Go 的开发不需要 Node 工具链。

## 代码结构

```
cmd/desktop/
├── main.go                 应用入口：窗口、单实例、错误编码
├── library_service.go      绑定服务：生命周期、环境信息、配置状态
├── library_methods.go      绑定服务：逐个转发 Library 的全部方法
├── e2e/                    端到端验证(server 模式 + Chrome)
├── Taskfile.yml、build/    Wails 构建配置（图标、Info.plist、各平台任务）
└── frontend/
    ├── bindings/           wails3 生成的 TS 绑定与类型，入库，不手改
    └── src/
        ├── App.tsx         主界面：状态与三栏布局
        ├── app.css         主界面样式（移植自 UI 原型）
        ├── components/app/ 主界面组件、详情抽屉与各弹窗
        ├── components/ui/  shadcn/ui 组件（pnpm dlx shadcn@latest add …）
        └── lib/            library.ts 读取快照，model.ts 领域辅助，actions.tsx 写操作流程，
                            ui.tsx 弹窗 / toast / run,errors.ts 错误文案，dnd.ts 拖放
```

### 给前端加一个技能库调用

1. 在 `library_methods.go` 里加一个方法，签名沿用技能库的：`ctx` 在前，返回 `(T, error)`。转发时经 `settle` 包一层：`return settle(s.lib.X(ctx))(ctx)`。前端取消调用后 Go 端若仍返回错误，Wails 会把它报成未处理的 rejection，`settle` 负责把这种错误吞掉。
2. 重新生成绑定。`wails3 dev` / `build` 会自动生成；也可以手动运行 `wails3 generate bindings -clean=true -ts -i`。生成的文件要提交。
3. 前端从 `@bindings/cmd/desktop` 导入 `LibraryService`，类型从 `@bindings/internal/library` 导入。只读调用返回可取消的 promise,组件卸载时记得 `cancel()`;写操作放进 `lib/actions.tsx`,经 `ui.run` 执行——它负责忙碌状态、按错误码提示，并在完成后重读快照。

错误：Go 端所有错误都经 `library.ErrorOf` 规范成技能库错误，再编码给前端。前端从 `err.cause` 拿到 `{code, message, details}`，用 `lib/library.ts` 的 `toLibraryError` 读取。

### 界面样式

主界面的外观以 UI 原型（分支 `worktree-prototype-wails-ui` 的 `cmd/prototype-wails`，变体 A）为准，`app.css` 就是从原型移植来的：

- 原型的 CSS 变量统一加 `--ui-` 前缀。shadcn 主题里有同名但含义不同的变量（`--border`、`--muted`、`--accent`），加前缀是为了不冲突。
- 元素级重置写成 `:where(.window) button` 这种形式，优先级保持在元素级。写成 `.window button` 会盖过 `.switch`、`.side-btn` 这些类自己的背景和边框。
- 禁用的白底控件只把文字调淡，不要整体降透明度，否则白色按钮会融进毛玻璃背景;蓝色主按钮禁用时降透明度。
- 弹窗渲染在 `.window` 之外，带 `window-portal` 类，元素级重置对它同样生效。
- shadcn/ui 组件保留自己的主题，留给对话框、菜单这类弹层使用。
