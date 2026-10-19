# 桌面端

Mechbox 的桌面端：Wails v3 应用，Go 进程直接持有技能库（Library），React 前端经 Wails 绑定调用它（ADR 0008）。macOS 一等支持，Windows / Linux 只保证能编译。

以下命令都在 `cmd/desktop/` 目录下执行。

## 功能

桌面端覆盖技能库(Library)的全部功能，界面按 UI 原型变体 A 组织：

| 界面位置 | 功能 |
| --- | --- |
| 左栏"入库技能" | 一个输入框:本地路径(可点"选择…",`~` 展开为主目录)、GitHub(`owner/repo`、`owner/repo@技能名`、github.com 链接)、GitLab 等 Git 仓库地址(含 SSH)、SKILL.md / zip / tar 下载链接或站点链接，由技能库识别来源;输入是 Git 仓库时，停下输入约半秒后从技能库取分支和 tag,在扫描前从下拉里选(分组列出、可筛选，默认选中默认分支，输入里带了分支时选中它;改选别的分支时只在发给技能库的请求里换掉输入里的分支(去掉结尾的 `#<分支>`,或换掉 tree 链接里的分支、保留目录),输入框里的文字不变;取不到时显示原因，仍可入库预览),只看某个目录就粘贴 tree 链接;识别失败时在对话框里显示原因。入库预览结果页先写识别结果(来源类型、展示名称、分支),在候选里勾选入库;诊断列出未成为候选的路径，GitHub 仓库可按需查看 skills.sh 审计，没有 GitHub 令牌时提醒容易被限流 |
| 左栏"技能市场" | 搜索 skills.sh,选中后做入库预览并入库 |
| 左栏筛选 | 所有技能、更新(要处理的更新数，和 Dock 角标同一套规则)、检查更新(按来源，本地目录来源不检查)、按来源的集合(只列有两个以上技能的)、组(新建 / 重命名 / 删除，悬停组行出现按钮) |
| 更新列表 | 每个有要处理的更新的集合一行：来源版本从哪到哪、哪些技能有变化、可能改名或来源里已删除，以及风险提示(新增或改动的脚本、新增的可执行文件);"查看"打开更新计划，"更新"按默认勾选直接执行，多个时可"全部更新";要处理的改动都会换掉你的修改时，这一行(和卡片)写明，"更新…"只打开更新计划;来源版本未知的技能如实写出 |
| 更新计划 | 先标风险，再给 GitHub 的提交说明，点技能或风险看文件差异(SKILL.md 按文档、脚本按代码);技能有变化的默认全勾(从技能发起时只勾这一个、其余折叠，并提示集合会不在同一个来源版本上;用目录里的修改更新过的技能有变化时标明"更新会换掉你的修改"、默认不勾),可以跳过单个技能的这次改动或整个来源版本;改名推测回答"是 / 不是",来源里已删除的选"移除 / 继续保留";顺带提来源里的新技能("去入库" / "不感兴趣");下面勾选装出去的地方(默认全勾;改过的地方、文件夹不在了的地方默认不勾并写明原因，勾上才覆盖、才装回去;项目不在这台机器上的不能勾)。一次确认先换库、再逐处重新安装，计划生成后来源变了时提示重新生成;完成提示带"回滚",没换上的地方列出原因、可以重试 |
| 集合标题条 | 筛选某个集合时：集合里的技能在哪个来源版本上、有几个有变化、更新、历史(每次更新、跳过、回滚各一条;更新可以回滚，跳过的可以找回来再更新)、来源里的新技能，以及"看来源里的其他技能"(对已有集合再做一次入库预览) |
| 技能卡片 → 详情抽屉 | 元数据(检查更新的结论、和同一来源其他技能的关系)、来源里已删除时的"移除 / 继续保留"(继续保留的只留淡色标记)、可能改名的说明、加入 / 移出组、各作用域的安装与状态(直接安装开关、单条重新安装、安装到…;被手动改过的地方可以"用这里的修改更新":看差异，勾选其他地方一起换成这份修改，用户改过的地方默认不勾，完成提示带"回滚")、文件浏览(SKILL.md 渲染)、版本历史(来源版本、与当前比较、查看旧版文件)、更新(打开所在集合的更新计划，只勾这一个)或重新读取(本地目录来源：一个操作读出新版本并换到装过的地方，改过的、文件夹不在了的地方不动;目录没变时把还没换上的地方补上)、更新历史(哪几次更新改过它，可以回滚)、只有一个技能的来源上"看来源里的其他技能"(点过"不感兴趣"的新技能也能找回来)、全部重新安装、移除技能 |
| 右栏作用域 | 全局与已登记项目下的各 Agent(左右两栏与技能详情抽屉都可以拖动边缘调宽，双击恢复默认，宽度记在这台机器上);登记 / 改位置 / 注销项目，项目目录不在这台机器上时带标记;点选即选中作用域，并打开"仅已安装"筛选列表 |
| 选中作用域后 | 卡片上的直接安装开关、列表顶部的 Agent 目录与健康状态、全部重新安装、各组的组安装开关、"仅已安装"筛选;看所有技能时，库里的技能之后接着列出这个作用域里的未纳管技能(不是库装的技能文件夹，虚线卡片标"未纳管"，提交在项目仓库里的标"在项目仓库里"，符号链接写出指向哪里),可以"在 Finder 中显示"或"入库"(确认时写清：交给 app 管以后再用命令行工具更新，只能报为被手动改过;项目仓库里的之后在 app 里更新会改动仓库里的文件),不能删除。npx skills 的来源记录里记了来源的，卡片上写"来自 …"，入库确认里预填成来源链接，写明以后跟着它检查更新;记录认不出来源的，列出之后在后台按名字去 skills.sh 搜，内容和搜到的同名技能大部分相同时同样写"来自 …"、预填(这次打开 app 里找过的不再找，确认框开着时找到的、用户还没改链接就补上);用户可以改链接或清空，读不成来源(连不上、来源里没有、认不出、集合里同名但内容不一样)时留在框里说明，可以重试、改链接或清空。带来源入库的进这个来源的集合，和来源里的不一样、又说不清是不是改过的，版本历史里写"入库时和来源里的不一样，可能有你的修改"，更新计划里同样提示会换掉修改、默认不勾。入库时磁盘上什么都不改(符号链接照样是符号链接);不带来源时和库里某个版本一致只补记这一处安装，否则成为没有来源的技能：卡片与详情写"没有来源"，不检查更新、不计入角标，在装出去的地方改了以后用那里的修改更新;同一个 Agent 的全局和某个项目里有同名技能时(不论是不是库装的),两处的卡片都标"同名"，并写出在这个 Agent 里会怎样(Claude Code 里全局的生效，Codex 里两份都会出现，自定义 Agent 只说同名) |
| 第一次打开、登记项目之后 | 在这台机器上第一次打开 app 时(记在 `settings.json` 里，换到新机器也一样),直接打开各 Agent 全局目录里不是库装的技能的确认;每登记一个项目，打开这个项目里的。先写磁盘上什么都不改、Agent 用的和现在一样，再写唯一的代价(之后还用命令行工具更新，只能报为被手动改过);第一次打开时说明项目里的登记项目时再接。按认出的来源(含在 skills.sh 上找到的，找到后补上)分组，内容一样的几处(含符号链接)合成一条，每条确认前都可以"改来源"(改链接或清空);在项目仓库里的、项目 skills-lock.json 记的默认不勾，勾上才提醒之后在 app 里更新会改动项目仓库里的文件;同名但内容不一样的单列出来，每个只选一份，默认选最近修改的;读不出的列在"不能入库"里。按来源逐批入库，读来源时写出在等什么;一个失败不影响其余的，没入库的可以改来源链接重试或清空后不带来源入库。之后作用域摘要里淡淡写一句还有几个不是库装的(在项目仓库里的不算),"全部入库…"再打开这个作用域的确认，不计入角标 |
| 拖放 | 技能卡片 → 右栏作用域 = 直接安装;技能卡片 → 左栏组 = 加入;组 → 右栏作用域 = 组安装 |
| 左栏"同步" | 整库推送到 S3 / 从 S3 拉回(先看同步计划：S3 上是哪台机器什么时候推送的那一份;拉回还说明里面有什么、不能拉回的原因、哪些项目不在这台机器上) |
| 左栏"设置" | 设置页(替换三栏，左边是分区目录):通用(关闭窗口后留在菜单栏、Dock 角标、语言：跟随系统 / 简体中文 / English)、Agent(显示勾选;添加、编辑、删除自定义 Agent,改目录时选择是否迁移)、项目(登记 / 改位置 / 注销，与右栏同一套弹窗)、检查更新(自动检查开关与间隔)、GitHub(个人访问令牌)与同步(服务地址、存储桶、对象名、区域、Access Key ID、Secret Access Key、Session Token;留空时照旧读环境变量)、关于(应用版本、数据库位置)、卸载 Mechbox(见下文"运行时行为") |
| 顶栏 | 作用域胶囊、搜索(⌘K)、列表 / 卡片、刷新(重新读取并对照磁盘巡检已安装的技能) |

Agent 的显示勾选只影响选择安装位置的地方(右栏与作用域弹层、详情里的"安装到…"):用户没勾选过的 Agent,检测到的和自定义的显示，其余不显示;已经装了技能的 Agent 不管勾没勾，装出去的地方照常列出。右栏与作用域弹层在每个地方直接列出装有技能的与检测到的 Agent,勾选了显示但没检测到的折进"其他 N 个 Agent"。几个 Agent 共用一个目录时，装一次这几个都算已装;从其中一个卸载前提示还被哪些 Agent 共用。改自定义 Agent 的目录、删除自定义 Agent 时，确认框列出还有别的 Agent 在用、因而保留的文件夹。

冲突按技能库的约定处理：安装前先看安装计划，目标有不是从库里装的同名文件夹时询问是否覆盖，被另一个技能占用或作用域根目录不存在时说明原因;重新安装前先现查各处状态，被改过的几处逐处勾选是否覆盖，文件夹不在了的几处(可能是用户有意删掉的)逐处选不动、装回去还是卸载，默认不动，只点了这一处的单条重新安装直接装回去;组安装、加入组、重新安装逐条返回结果，被跳过或失败的条目列出原因;移除技能、注销项目、移出组时让用户决定磁盘上已安装文件夹的去留，并说明其中哪几处被改过。

## 准备

| 依赖 | 说明 |
| --- | --- |
| Go | 版本见根目录 `go.mod` |
| Xcode Command Line Tools | macOS 上编译 cgo 需要（`xcode-select --install`） |
| Node.js + pnpm | 前端工具链。`frontend/` 与 `e2e/` 是同一个 pnpm workspace(根在 `cmd/desktop/`,共用一份 `pnpm-lock.yaml`),在 `cmd/desktop/` 下 `pnpm install` 一次装好两边的依赖;构建时会自动执行 |
| wails3 CLI | 必须和 `go.mod` 里钉住的 Wails 版本一致：`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-beta.25` |

`go install` 把 `wails3` 装到 `$(go env GOBIN)`，未设置时装到 `$(go env GOPATH)/bin`；这个目录要在 `PATH` 里。装好后可用 `wails3 doctor` 检查环境。

## 开发

```sh
wails3 dev
```

它会生成绑定、启动 Vite 开发服务器（端口 9245），再编译并打开开发版应用 `bin/mechbox.dev.app`：

- 改前端（`frontend/src/`）：窗口热更新，不重启。
- 改 `cmd/desktop/` 下的 Go 代码：自动重新生成绑定、重新编译并重启应用。
- 改 `internal/library/`：不在监听范围内（监听根目录是 `cmd/desktop/`），要 Ctrl-C 后重新 `wails3 dev`。
- 按 Ctrl-C 结束。如果异常退出后再启动报 `address already in use`，是上次的 Vite 还占着端口：`lsof -ti tcp:9245 | xargs kill`。

开发版打开的是**真实技能库**（见下文“运行时行为”），安装操作也会写进真实的 Agent 目录。想用一个隔离的库调试，就给它一个临时 `HOME`；技能库位置和 Agent 目录都从 `HOME` 推导，所以两者都会落在临时目录里。Go 缓存也默认跟着 `HOME` 走，一并指回原处，免得重新下载依赖：

```sh
HOME=/tmp/skills-dev GOMODCACHE=$(go env GOMODCACHE) GOCACHE=$(go env GOCACHE) wails3 dev
```

## 构建与打包

```sh
wails3 build     # 只编译二进制：bin/mechbox
wails3 package   # 打成 macOS 应用：bin/mechbox.app（ad-hoc 签名）
open bin/mechbox.app
```

`bin/` 不入库。

**毛玻璃需要 `private_mac_apis` 构建标签。** 窗口背后是 macOS 原生毛玻璃，但要透出来，WebView 本身必须透明；Wails v3 只有带这个标签时才会把 WebView 设成透明，否则 WebView 画白底，把毛玻璃整个挡住。`Taskfile.yml` 已默认加上（`EXTRA_TAGS`），`wails3 dev` / `build` / `package` 都会带。代价是用了私有 API，不能上架 Mac App Store。直接 `go build ./cmd/desktop` 不带这个标签，也能运行，只是没有毛玻璃。

### 发布

推送 `v<X.Y.Z>` 形式的 tag(如 `git tag v0.1.0 && git push origin v0.1.0`)后，`.github/workflows/release.yml` 在 GitHub Actions 上构建各平台的安装包，再发布到同名的 GitHub Release:macOS 通用 `.dmg`(Apple 芯片 + Intel)、Windows amd64 的 NSIS 安装程序、Linux amd64 / arm64 的 `.AppImage`、`.deb`、`.rpm`、`.pkg.tar.zst`,另附 `SHA256SUMS.txt`。tag 带 `-` 后缀(如 `v0.2.0-beta.1`)时发布成预发布版本。

- 版本号以 tag 为准：构建前由 `.github/scripts/set-version.sh` 写进 `build/config.yml`(关于页显示的版本)、`Info.plist`、Windows 版本资源、NSIS 与 nfpm 配置，仓库里这几处的版本号不用手改。macOS 与 Windows 的版本字段只认数字，预发布后缀只留在 `config.yml` 与 Linux 包里。
- 在 Actions 页面手动运行这个 workflow 只构建、不发布，安装包留在那次运行的 Artifacts 里，文件名里的版本是 `dev-<提交>`。
- Windows 构建要开 cgo(`CGO_ENABLED=1`,技能库用的 SQLite 驱动需要),Windows 任务默认不开。
- macOS 只做 ad-hoc 签名、没有公证，Windows 安装程序也没有代码签名;用户第一次打开时会被系统拦一下，Release 说明里写了怎么放行。

## 运行时行为

- **技能库位置**：`<用户配置目录>/mechbox/library.db`，macOS 上是 `~/Library/Application Support/mechbox/library.db`。首次启动时创建目录，由 Library 初始化空库。它只依赖 `HOME`，所以从 Finder / Dock 启动也能找到。
- **应用设置**：检查更新、通用与 Agent 的显示勾选(以及这台机器是否打开过 app,第一次打开时带用户入库已有的技能)只属于这台机器，存在技能库旁边的 `settings.json`（macOS 上是 `~/Library/Application Support/mechbox/settings.json`），不进技能库、不随同步走；文件里没有的字段取默认值，加设置项就是给 `Settings` 加字段。
- **GitHub 令牌与同步设置**:设置页填的令牌、Secret Access Key、Session Token 合成一项存在系统里:macOS 是钥匙串(一个条目，服务名 `com.tiaot33.mechbox`);Windows 是凭据管理器(一条普通凭据 `com.tiaot33.mechbox:credentials`,只存在这台机器上、不随漫游配置文件走;单条凭据最多 2560 字节，超过时分成几条 `com.tiaot33.mechbox:credentials:<代>:<序号>`,最后才改写第一条，中途失败时旧内容仍可读;缺了一段或第一条坏了(例如手动删过)按没存过处理，重新填写后覆盖);Linux 是 D-Bus 会话总线上的 Secret Service(GNOME Keyring、KWallet 等;默认集合里一项，属性 `service=com.tiaot33.mechbox`、`account=credentials`)。其余几项存在 `settings.json` 的 `sync` 下;都不进技能库、不随同步走(ADR 0011)。启动时和点保存后交给技能库;令牌没填时技能库读 `GITHUB_TOKEN` / `GH_TOKEN`,同步设置全部留空时读 `SKILLS_S3_*`、`AWS_*`,填了任意一项就整组用设置页的。应用换了签名(每次重新编译、`wails3 dev` 重启)后第一次读取时 macOS 会询问是否允许;Linux 上集合锁着时 Secret Service 会弹出解锁框，5 分钟没处理就撤掉、按读不到处理。拒绝或关掉后打开设置页会再读一次，仍读不到时这两个分区只显示「重试」,不能修改。Linux 上没有 Secret Service(没有 D-Bus 会话——不会自己启动 `dbus-launch`——或没装、没启动 GNOME Keyring / KWallet)时同样按读不到处理，不退回只存在内存里;读不到的提示在 Linux 上还写明可以装好并启动 GNOME Keyring / KWallet 后重试，或从设置了环境变量的终端启动;卸载 Mechbox 时删不掉条目照旧拦下，可以跳过(Secret Service 可能只是没启动，条目还在)。
- **自动检查更新**：默认打开、每天。Go 端每分钟看一次是否到了间隔（以技能库里记下的检查时间为准，从未检查过的技能从入库时算起，手动检查也算）；“只在打开时”是启动应用、从菜单栏或 Dock 重新打开窗口时各检查一次。只检查非本地目录来源的集合，不发系统通知；检查完更新 Dock 角标，并发 `updates-checked` 事件让前端重读。
- **Dock 角标**：计入角标的技能数，按技能库给出的 `UpdateCheck.Pending`(有新版本且没跳过、来源里已删除且没选继续保留、推测出改名;本地目录来源、跳过的改动、新技能不计入),和左栏"更新"的计数一致；前端每次重读快照后经 `AppService.RefreshBadge` 重算。只在 macOS 上显示。
- **卸载 Mechbox**:设置页最后一个分区。弹窗先现查各处安装的状态，写明会删什么(技能库、设置页里的设置、存在系统里的令牌与密钥、应用本身);配置了同步时提醒可以先推送(「先去同步…」打开同步弹窗),没配置时写明无法恢复。已安装的技能文件夹按全局、各项目分开计数，三选一：保留(默认，Agent 照常使用)、移到废纸篓但保留被改过的几处、全部移到废纸篓;被改过的逐处列出。确认后先删令牌与密钥的条目(见上一条)——删不掉时什么都不动，可以重试或跳过;再移文件夹(已经不在的、在不在这台机器上的项目里的跳过),中途失败时数据库与设置都不动，重试时跳过已经移走的;从开始卸载起不再保存令牌与同步设置。然后退出，技能库关上以后再删数据库与 `settings.json`(目录空了连目录一起删)、`~/Library` 下 WebKit、Caches、HTTPStorages、Saved Application State、Preferences 里这个应用的数据，最后把 `.app` 移到废纸篓，移不动时在 Finder 中选中它。S3 上的库不动。不在 `.app` 里运行(如 `go build` 出来的)时不动可执行文件;Windows / Linux 上:
  - 退出后只删数据库与 `settings.json`,应用本身和 WebView 的数据不动，弹窗里写明应用要用系统的方式卸载。
  - Windows 上文件夹移到回收站，弹窗里说「回收站 / Recycle Bin」。
  - Linux 上文件夹按 freedesktop 规范移进 `$XDG_DATA_HOME/Trash`(默认 `~/.local/share/Trash`);和家目录不在同一个文件系统上时，放进那个卷顶层的 `.Trash/$uid` 或 `.Trash-$uid`。弹窗里中文说「回收站」,英文说 Trash。
  - 进不了回收站时报错，那一处留在原处、不直接删除;弹窗提示可以改选保留后重试。
  - server 模式下只删数据库与 `settings.json`,文件夹直接删除。
- **界面语言**：设置页「通用」里选跟随系统(默认)、简体中文或 English,存在 `settings.json` 的 `language`(跟随系统时不写)。跟随系统时由 Go 端判断系统语言(`AppService.Language`):macOS 读系统设置里排第一的首选语言(`[NSLocale preferredLanguages]`,不用 WebView 的 `navigator.language`,它跟随 app bundle 声明的语言),其他平台与 server 模式读 `LC_ALL` / `LC_MESSAGES` / `LANG`;首选中文(含繁体)时用中文，其余用英文。前端等 Go 端给出语言再渲染，改了设置整个界面立即切换，菜单栏图标的菜单与 macOS 顶部菜单栏同时换文字。系统控件(目录选择框的按钮、输入框的右键菜单等)跟随系统语言，不跟随这里的选择;`Info.plist` 声明了 en 与 zh-Hans 两种本地化，系统控件才会按系统语言显示中文。
- **关闭窗口**：“关闭窗口后留在菜单栏”打开（默认）时，关闭窗口只是隐藏，Dock 图标和角标仍在，菜单栏出现一个入口（打开 / 退出）；点它或 Dock 图标重新打开窗口，入口随之消失。关掉时关闭窗口就退出。
- **单实例**：同一时间只运行一个实例。再次启动时，新进程直接退出，把已有窗口带到前台（最小化了也会恢复）。单实例检查早于打开技能库，第二个进程不会碰库文件。

## 检查

```sh
go test ./...                              # 仓库根目录；含 cmd/desktop 的测试
go vet ./...
cd frontend && pnpm exec tsc               # 前端类型检查
pnpm install && cd e2e && pnpm test        # 端到端验证(见下)
```

`go test` 里的 `TestLibraryServiceExposesEveryLibraryMethod` 保证 `library.Library` 的每个方法都开放给了前端：Library 新增方法而绑定服务没跟上时，这个测试会失败。

### 端到端验证

`e2e/run.sh` 把桌面端编译成 Wails 的 server 模式(同一套前端与绑定，以 HTTP 服务运行、不开窗口),在临时 `HOME` 里放好测试技能、项目目录和几个 zip,然后用本机的 Google Chrome 按界面操作走一遍：入库(本地目录 / URL / 本地 Git 仓库与分支下拉)、分组、登记项目、直接安装与组安装、冲突询问、重新安装(文件夹不在了时不动、装回去或卸载)、检查更新与更新(风险提示、连同装出去的地方、从技能发起、重新读取、用目录里的修改更新)、更新计划(差异、确认改名、继续保留、新技能不感兴趣、跳过)、历史与回滚、版本比较、拖放、同步未配置、设置(含 GitHub 令牌与同步设置、Agent 的显示勾选、自定义 Agent 的添加 / 改目录迁移 / 删除与共用目录卸载)、第一次打开时一次确认全部入库(内容一样的合成一条、同名不一样的只选较新的、作用域里再全部入库)、登记项目后一次确认入库(项目仓库里与 skills-lock.json 记的默认不勾、读不成来源的清空后入库)、未纳管技能(符号链接、项目仓库里已提交的;入库、内容一致时只补记安装、从来源记录认出来源与可能有你的修改、读不成来源时清空后入库、在 skills.sh 上找到来源后写出并预填)、符号链接(用这里的修改更新、回滚、重新安装时说明并换成独立的一份，卸载被链着的文件夹前先给链着它的换上一份，指向 Agent 目录以外的默认不入库、不算进还有几个没入库)、全局和项目里的同名技能、Git 来源与 npx skills 记录(来源名字与来源版本、来源版本未知、装的是来源现在这一版又改过的不报有更新)、库里同名的技能、改项目位置、移除与注销、卸载 Mechbox(最后一步，之后 server 退出),并核对磁盘上已安装的文件夹。

```sh
pnpm install                 # 在 cmd/desktop/ 下，装好 workspace 里 frontend 与 e2e 的依赖
cd e2e
pnpm test                    # 离线步骤
E2E_NETWORK=1 pnpm test      # 连同技能市场与 GitHub 入库(需要联网)
pnpm test 重新安装           # 只跑名字包含"重新安装"的步骤(步骤之间有依赖，单跑可能缺前置状态;名字含"卸载"的也会跑到最后的卸载 Mechbox，之后 server 退出)
```

失败的步骤会截图到 `/tmp/mechbox-e2e-shots/`。步骤按中文界面写,`run.sh` 给 server 设 `LC_ALL=zh_CN.UTF-8`,界面不随跑测试的机器的系统语言变。server 模式(`server` 构建标签)在各平台上都不用系统的钥匙串 / 凭据管理器 / Secret Service,令牌和密钥只存在进程内存里，不碰本机的真实条目。server 模式下"在 Finder 中显示"什么都不做(e2e 只核对前端传过去的路径);卸载 Mechbox 时不清 `~/Library`、不动可执行文件，文件夹直接删除。离线时 `run.sh` 用 `E2E_HOSTS` 让 server 模式把 skills.sh 与假的 well-known 站点 `skills.e2e.test` 的请求改到本地的 HTTP 服务，不访问真实的 skills.sh;在 skills.sh 上找来源的步骤(`[离线]` 开头)只在离线时运行。server 模式没有原生窗口，所以系统目录选择框、窗口毛玻璃、单实例、Dock 角标与关闭窗口后留在菜单栏不在这里验证，需要在 `wails3 dev` 里手动确认。

`frontend/dist/` 里提交了一个占位文件 `.gitkeep`（源文件是 `frontend/public/.gitkeep`，每次 Vite 构建都会复制过去），所以没构建前端时 `go build/vet/test ./...` 也能通过，纯 Go 的开发不需要 Node 工具链。

## 代码结构

```
cmd/desktop/
├── main.go                 应用入口：窗口、单实例、错误编码
├── library_service.go      绑定服务：生命周期、环境信息（含应用版本）、配置状态
├── settings.go             这台机器的应用设置：字段、默认值、读写 settings.json
├── app_service.go          绑定服务：应用设置，以及自动检查更新、Dock 角标、关闭窗口后留在菜单栏
├── language.go             界面语言：设置值与按系统语言解析(language_darwin.go 读 macOS 的首选语言;其他平台与 server 模式见 language_other.go,读环境变量)
├── menu.go                 Go 这边画的菜单文字(菜单栏图标的菜单、macOS 顶部菜单栏),随界面语言切换
├── credentials.go          设置页的 GitHub 令牌与同步设置：存取并交给技能库
├── keychain_darwin.go      令牌与密钥存在系统里:macOS 钥匙串(keychain_windows.go 是 Windows 凭据管理器，超过单条上限时按 keychain_parts.go 分成几条;keychain_linux.go 是 D-Bus 上的 Secret Service,等解锁框的结果见 keychain_prompt.go;server 模式见 keychain_server.go,只存在内存里)
├── badge_darwin.go         Dock 角标（其他平台见 badge_other.go，不显示）
├── reveal_darwin.go        在 Finder 中显示(其他平台与 server 模式见 reveal_other.go,什么都不做)
├── uninstall.go            卸载 Mechbox：删令牌与密钥的条目、移走文件夹，退出后删数据库与设置(uninstall_<平台>.go 是各平台怎么移文件夹、清系统数据;uninstall_darwin.go 清 ~/Library 并把 .app 移到废纸篓;server 模式见 uninstall_server.go,文件夹直接删除)
├── trash_windows.go        Windows 回收站(IFileOperation,驱动器没有回收站时中止);trash_freedesktop.go 是 Linux 的 freedesktop 回收站
├── library_methods.go      绑定服务：逐个转发 Library 的全部方法
├── e2e/                    端到端验证(server 模式 + Chrome)
├── pnpm-workspace.yaml     pnpm workspace:frontend 与 e2e,共用 pnpm-lock.yaml
├── Taskfile.yml、build/    Wails 构建配置（图标、Info.plist、各平台任务）
└── frontend/
    ├── bindings/           wails3 生成的 TS 绑定与类型，入库，不手改
    └── src/
        ├── App.tsx         主界面：状态与三栏布局
        ├── index.css       样式入口:Tailwind 主题(原型的颜色、圆角、阴影、半透明表面)与少量全局样式
        ├── components/ui/  shadcn/ui 组件(按本应用的主题改过)与几个同风格的小组件
        ├── components/app/ 主界面组件、详情抽屉与各弹窗;layout.ts 是三栏与顶栏的公共样式
        └── lib/            library.ts 读取快照，model.ts 领域辅助，actions.tsx 写操作流程，update.tsx 更新流程(更新计划、回滚、历史),diff.ts 更新计划里的逐行差异，
                            ui.tsx 弹窗 / toast / run,errors.ts 错误文案，dnd.ts 拖放，
                            content.ts 技能文件内容解码与渲染，paths.ts 本地路径输入,platform.ts 当前平台(按平台换说法的文案用),
                            i18n/ 界面语言(index.tsx 当前语言与 useT,zh.tsx 中文文案，en.tsx 英文文案)
```

### 界面文案

界面上看得到的文字(JSX 文本、`title` / `placeholder` / `aria-label` 等属性、toast、弹窗、错误文案)都写进 `frontend/src/lib/i18n/` 的两份文案文件，新增文案时中英一起写：`zh.tsx` 是唯一来源，按界面区域分组;`en.tsx` 照它的结构翻译，`satisfies Messages` 让漏翻、参数不对的条目在 `tsc` 里报出。

- 组件里用 `useT()` 取当前语言的文案;组件外的函数(`model.ts`、`actions.tsx` 等)用 `messages()`。
- 带变量的句子写成函数，整句交给文案文件：不要把半句话当参数传，也不要在组件里拼接两句文案(英文语序不同，句子之间还要空格)。带格式的句子(如加粗的名字)让函数返回 ReactNode。
- 英文的单复数、列表(`Intl.ListFormat`)在 `en.tsx` 里处理;中文列表照旧用顿号。日期统一经 `formatDate`,数字的 `toLocaleString` 显式传语言。
- 英文用词：术语表(`CONTEXT.md`)里有的概念用术语表的英文名。
- 按平台换说法的文案(如卸载时的废纸篓 / 回收站)在文案函数里调 `lib/platform.ts` 的 `platform()`,组件不传平台。
- Go 这边的菜单文字在 `menu.go` 的 `nativeLabels`,同样中英两份。

### 给前端加一个技能库调用

1. 在 `library_methods.go` 里加一个方法，签名沿用技能库的：`ctx` 在前，返回 `(T, error)`。转发时经 `settle` 包一层：`return settle(s.lib.X(ctx))(ctx)`。前端取消调用后 Go 端若仍返回错误，Wails 会把它报成未处理的 rejection，`settle` 负责把这种错误吞掉。
2. 重新生成绑定。`wails3 dev` / `build` 会自动生成；也可以手动运行 `wails3 generate bindings -clean=true -ts -i`。生成的文件要提交。
3. 前端从 `@bindings/cmd/desktop` 导入 `LibraryService`，类型从 `@bindings/internal/library` 导入。只读调用返回可取消的 promise,组件卸载时记得 `cancel()`;写操作放进 `lib/actions.tsx`,经 `ui.run` 执行——它负责忙碌状态、按错误码提示，并在完成后重读快照。

错误：Go 端所有错误都经 `library.ErrorOf` 规范成技能库错误，再编码给前端。前端从 `err.cause` 拿到 `{code, message, details}`，用 `lib/library.ts` 的 `toLibraryError` 读取。

### 界面样式

主界面的外观以 UI 原型（分支 `worktree-prototype-wails-ui` 的 `cmd/prototype-wails`，变体 A）为准，用 shadcn/ui 组件 + Tailwind 实现(ADR 0008)。

- 控件用 `components/ui/` 里的组件(Button、Badge、Switch、Dialog、Sheet、Popover、Input、NativeSelect、Segmented、Card、Notice、ListBox / ListRow、Field / Hint),不在组件外另写一套同类控件。shadcn 的组件代码复制进来后按本应用改过，改动写在各文件开头。
- 颜色、圆角、阴影用 `index.css` 里的主题(`text-foreground`、`border-border`、`rounded-tile`、`bg-glass-overlay` 等),不写一次性的色值。
- 毛玻璃来自窗口背后的 macOS 原生层，界面只用半透明白色让它透出来，档位见 `index.css`:`glass-tile`(三栏底板)、`glass-card`(卡片与摘要条)、`glass-overlay`(抽屉、弹窗、弹层，另加 `backdrop-blur`,底下的内容糊掉而不是透出文字)。加 shadcn 组件时先把默认的不透明背景和深色遮罩换成这几档。
- 禁用的白底控件只把文字调淡，不整体降透明度，否则白色按钮会融进毛玻璃背景;蓝色主按钮禁用时降透明度。
- 按钮的 `loading` 让文字留着占位、上面盖一个转圈，按钮宽度不变;弹窗贴着窗口上部出现，内容变高时只往下长。
- 详情抽屉是非模态的 Radix Dialog,在主体区域里原地渲染、没有遮罩：抽屉开着时其他地方照常可用，点别的卡片切换、再点当前卡片关闭，Esc 或关闭按钮也能关。弹窗(`Modal`)是模态的，叠了几层时 Esc 只关最上面一层。
- `index.css` 只放工具类管不到的：页面基础样式、窗口底色(`window-backdrop`)、渲染出来的 Markdown(`.md`)与拖放中的高亮(按 `data-dragging`、`data-drop-hover` 等属性)。
- e2e 按两类钩子找元素：通用控件用 `data-slot`(`dialog-content`、`badge`、`list-row` 等)或角色(`role="switch"`、`aria-pressed`);应用自己的结构用类名(`a-card`、`nav-item`、`scope-pill`、`set-row` 等)。这些类名不挂样式，改结构时保留。
