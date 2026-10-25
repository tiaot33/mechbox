# 测试改动

本指南说明改完代码以后怎样验证：先按改动的位置选要跑的检查，再按对应小节运行检查、补测试或在 app 里看界面。所有测试数据都在仓库里。除了名字以 `[网络]` 开头的 e2e 步骤，测试都不访问网络。

- [按改动选要跑的检查](#按改动选要跑的检查)
- [运行 Go 测试](#运行-go-测试)
- [写 Go 测试](#写-go-测试)
- [运行前端检查](#运行前端检查)
- [运行端到端验证](#运行端到端验证)
- [写端到端验证的步骤](#写端到端验证的步骤)
- [改端到端验证的测试数据](#改端到端验证的测试数据)
- [在 app 里验证界面](#在-app-里验证界面)
- [更换开发数据的技能快照](#更换开发数据的技能快照)

## 按改动选要跑的检查

先找到改动所在的行，跑「必跑」一列；改动会改变界面上看到的行为时，再做「另外」一列。

| 改动的位置 | 必跑 | 另外 |
| --- | --- | --- |
| `internal/library/` 及其子包 | Go 测试、`go vet` | 行为会出现在界面上时，跑 e2e |
| `cmd/desktop/` 下的 Go 代码 | Go 测试、`go vet` | 同上 |
| `cmd/desktop/frontend/src/` | 前端类型检查、前端单元测试 | 在 app 里看界面；改了操作流程时跑 e2e |
| `cmd/desktop/e2e/`、`cmd/desktop/testdata/e2e/` | e2e | — |
| `cmd/desktop/devseed/`、`cmd/desktop/testdata/dev/` | `wails3 task dev:seed`，再在 app 里核对 | — |

各项检查覆盖的范围：

- **Go 测试**：技能库的全部公开方法（`test/library/`），各子包的内部实现，以及桌面端的 Go 代码（绑定服务、设置、菜单、卸载、回收站）。
- **前端单元测试**：`frontend/src/lib/` 里不碰界面、不调技能库的纯函数。
- **e2e**：把桌面端编译成 server 模式，用 Chrome 按界面操作走一遍技能库的全部功能，并核对磁盘上的技能文件夹。
- **在 app 里看界面**：e2e 覆盖不到的视觉细节，以及只有原生窗口才有的行为（见[在 app 里验证界面](#在-app-里验证界面)）。

## 运行 Go 测试

### 前置条件

- Go，版本不低于 `go.mod` 里写的 1.26
- `git`：集成测试用真实的 Git 仓库

### 步骤

1. 在仓库根目录运行全部 Go 测试，包括 `cmd/desktop` 的测试：

   ```sh
   go test ./...
   ```

   全部通过时，每个包输出一行 `ok`。全量运行约 2.5 min，几乎都花在 `test/library` 上。代码没变的包，`go test` 直接用缓存的结果，输出里带 `(cached)`。

2. 运行静态检查：

   ```sh
   go vet ./...
   ```

   没有问题时没有输出。

3. 可选：只跑技能库的集成测试，或只跑名字匹配的测试：

   ```sh
   go test ./test/library
   go test ./test/library -run TEST_NAME
   ```

   把 `TEST_NAME` 替换为测试函数名或它的正则，如 `TestInstall`。

4. 可选：统计技能库的覆盖率：

   ```sh
   go test ./test/library -coverpkg=./internal/library/...
   ```

   输出末尾的 `coverage:` 一行是技能库所有包合起来的语句覆盖率。

### 有些测试会跳过

下面几种测试在条件不满足时跳过，`go test -v` 的输出里写着原因：

| 条件 | 跳过的测试 |
| --- | --- |
| 以 root 运行 | 注入目录权限故障的测试，root 不受目录权限限制 |
| 文件系统区分大小写 | 只在大小写不敏感的文件系统上才会出现的情况，如 `.AGENTS` 和 `.agents` 是同一个目录 |
| Windows | 检查数据库文件权限的测试，Windows 上权限由 ACL 决定 |
| 没有 `git` 或 POSIX shell | `agentdir`、`wsl` 子包的部分单元测试 |

WSL 相关的测试不需要 Windows。测试把环境变量 `MECHBOX_TEST_WSL_SHELL` 设成本机的 `sh`，让本机的 shell 充当 WSL 发行版，所以在 macOS 和 Linux 上照常运行。

## 写 Go 测试

### 放在哪里

按测试经过的接口选位置：

| 测试经过 | 放在 | 包名 |
| --- | --- | --- |
| 技能库的公开方法 | `test/library/` | `library_test` |
| 某个文件内部的实现 | 被测文件旁边 | 被测文件的包 |
| 桌面端的 Go 代码 | `cmd/desktop/` 下被测文件旁边 | `main` |

只经公开 API 的集成测试放在顶层 `test/` 下，按被测对象分目录。新的被测对象另建目录，如 `test/<对象>/`。

### `test/library/` 的文件怎样命名

测试文件按被测方法所在的源文件命名：

- 文件名前缀就是那个源文件，后面可以加方面。例如 `PreviewFetch` 在 `fetch.go`，它的测试在 `fetch_preview_test.go`。
- 某种来源特有的行为放 `source_<来源>_test.go`，如 `source_github_test.go`。
- 技能库整体的性质放 `library_<方面>_test.go`。方面包括并发、多进程、建表、错误、JSON 契约。
- 一个测试用到几个方法时，按它主要验证的那个方法归属。

找某个方法的测试，可以按方法名搜索（如 `lib.Install(`），或用 `go test -run`。

### 测试数据由每个测试自己造

集成测试不用 `testdata/` 里的共享数据，每个测试只造它要的最少数据。数据和断言写在同一处，读测试就能看出它测什么。共享数据集里加一个用例，会让断言「库里正好有这几个技能」的其他测试失败；各造各的数据就没有这个问题。

造数据用 `test_helpers_test.go` 里的辅助函数，一个技能一行：

| 要造的数据 | 辅助函数 |
| --- | --- |
| 入库一个本地技能，正文是 `# <名字>` | `fetchNamedSkill` |
| 入库一个本地技能，指定正文 | `fetchSkillWithBody` |
| 入库一个本地技能，之后要改来源再更新 | `fetchLocalSkill`，返回来源目录 |
| 入库一个带可执行脚本的技能 | `fetchExampleSkill` |
| 从直接下载的 zip 入库 | `zipArchive` 打包，`serveArchive` 用 `httptest` 提供下载并返回入库预览请求 |
| 在 Git 仓库里提交 | `runGit`、`commitGit` |
| 打开一个临时数据库 | `openTestLibrary` |

辅助函数用真实的 SQLite 文件、Git 仓库和 `httptest` 服务，不用 mock。要隔离本机的 `gh` 登录时用 `stubGitHubCLI`。

### 新增来源或绑定方法时要改的测试

- 新增一种来源：在 `test/library/library_sources_test.go` 的 `sourceKinds` 里加一行，让它和其他来源走同一遍入库、检查更新、更新、新技能、删除。其余步骤见 `AGENTS.md` 的「新增一种来源」。
- 技能库新增方法：`cmd/desktop` 的 `TestLibraryServiceExposesEveryLibraryMethod` 检查每个方法都开放给了前端。绑定服务没跟上时，这个测试失败。

## 运行前端检查

### 前置条件

- Node.js 和 pnpm
- 在 `cmd/desktop/` 下运行过一次 `pnpm install`。`frontend/` 与 `e2e/` 是同一个 pnpm workspace，一次装好两边的依赖。

### 步骤

1. 在 `cmd/desktop/frontend/` 下运行类型检查：

   ```sh
   pnpm exec tsc
   ```

   没有类型错误时没有输出。

2. 运行前端单元测试（vitest）：

   ```sh
   pnpm test
   ```

   输出末尾是通过的测试数，如 `Tests  20 passed (20)`，用时约 1 s。

### 写前端单元测试

前端单元测试只测不碰界面、不调技能库的纯函数，测试文件放在被测文件旁边，如 `lib/consent.test.ts`。

默认勾哪些、写之前说明什么，这类守住[产品承诺](adr/0010-product-promises.md)的规则都放在 `lib/consent.ts`。改这些规则时，必须同时改 `lib/consent.test.ts`。

## 运行端到端验证

端到端验证（e2e）由 `cmd/desktop/e2e/run.sh` 完成。它把桌面端编译成 Wails 的 server 模式：同一套前端与绑定，以 HTTP 服务运行，不开窗口。然后它在临时 `HOME` 里放好测试数据，用本机的 Google Chrome 按界面操作走一遍，并核对磁盘上已安装的技能文件夹。完整的步骤列表见 `cmd/desktop/e2e/e2e.mjs` 里的各个 `step(...)`。

### 前置条件

- [运行前端检查](#运行前端检查)的前置条件
- Go
- Google Chrome
- `python3`、`zip`、`lsof`、`git`：`run.sh` 用它们起本地 HTTP 服务、打 zip、确认服务是自己起的、建 Git 仓库

### 步骤

1. 在 `cmd/desktop/e2e/` 下运行全部离线步骤：

   ```sh
   pnpm test
   ```

   脚本先构建前端和 server 模式二进制，再逐步运行。每步输出一行 `PASS`、`FAIL` 或 `SKIP`，跑完输出通过数，如 `80/80 通过`。最后一行是这次的截图目录，如 `截图：/tmp/mechbox-e2e-shots.Ab12Cd`。全量运行约 4 min，其中包括构建。

   不同工作区里的几个终端或 agent 可以同时运行 `pnpm test`，不用设环境变量。同一个工作区里一次只跑一个，因为两次构建会同时写 `frontend/dist`。

   日志里「页面错误 / 失败请求」下列出的 422 请求，多数来自故意触发错误的步骤。只有某一步 `FAIL` 时才需要看。

2. 如果有步骤失败，到最后一行列出的截图目录看失败时的截图。

3. 可选：只跑名字里含某段文字的步骤：

   ```sh
   pnpm test STEP_NAME
   ```

   把 `STEP_NAME` 替换为步骤名里的一段文字，如 `重新安装`。步骤之间有依赖，单跑某步可能缺前置状态。名字含「卸载」时也会跑到最后的「卸载 Mechbox」，之后 server 退出。

4. 可选：连同要联网的步骤（技能市场、GitHub 入库）一起跑：

   ```sh
   E2E_NETWORK=1 pnpm test
   ```

   要用 GitHub 令牌时再设 `E2E_GITHUB_TOKEN`。测试脚本在设置页里把它填进去；应用本身不读环境变量。

### e2e 怎样隔离环境

- **界面语言**：步骤按中文界面写，`run.sh` 给 server 设 `LC_ALL=zh_CN.UTF-8`，界面不随运行机器的系统语言变化。
- **设置**：都存在临时 `HOME` 里的数据库中，e2e 直接读它的 `config` 表核对。
- **端口**：`run.sh` 每次向系统要三个空闲端口，分别给 server、zip 服务和 hosts 服务，都只绑 `127.0.0.1`。要固定端口时设 `E2E_PORT`，`run.sh` 用它和后面两个端口；其中有端口被占用时，`run.sh` 报错退出。某个服务没起来时，`run.sh` 打出它的日志并退出，不会去测别人的服务。
- **截图**：每次放进一个新目录，名字是 `/tmp/mechbox-e2e-shots.` 加 6 位随机字符，跑完不删。要固定目录时设 `E2E_SHOTS`。
- **网络**：离线运行时，`run.sh` 用 `E2E_HOSTS` 把 skills.sh 和假的 well-known 站点 `skills.e2e.test` 的请求转到本地 HTTP 服务，不访问真实的 skills.sh。名字以 `[离线]` 开头的步骤只在离线时运行。
- **server 模式的差异**：「在 Finder 中显示」什么都不做，e2e 只核对前端传过去的路径。卸载 Mechbox 时不清 `~/Library`、不动可执行文件，文件夹直接删除。

### e2e 覆盖不到的部分

server 模式没有原生窗口，下面这些只能[在 app 里验证](#在-app-里验证界面)：

- 系统目录选择框
- 窗口毛玻璃
- 单实例
- Dock 角标
- 关闭窗口后留在菜单栏

## 写端到端验证的步骤

e2e 的每一步是 `cmd/desktop/e2e/e2e.mjs` 里的一次 `await step("步骤名", async () => { ... })`。所有步骤共用一个浏览器页面和一个临时 `HOME`，按文件里的顺序依次运行，后面的步骤用前面留下的状态。所以新步骤必须放在它依赖的状态之后。

- **步骤名**：按中文界面写，写出这一步验证的行为。以 `[网络]` 开头的步骤只在 `E2E_NETWORK=1` 时运行；以 `[离线]` 开头的只在离线时运行。
- **找界面元素**：通用控件用 `data-slot` 或角色，应用自己的结构用类名，规则见 `cmd/desktop/README.md` 的「界面样式」一节。`modal()`、`card(name)`、`drawer()`、`toast(...)` 已经封装了最常用的几种。
- **核对结果**：除了界面，还要核对磁盘上的技能文件夹（读临时 `HOME` 里的文件），设置则直接读数据库的 `config` 表。
- **失败时**：`step` 截图、按 Esc 关掉残留的弹窗和抽屉，再继续下一步。所以一步失败后，后面的步骤可能连带失败；先修第一个 `FAIL`。

## 改端到端验证的测试数据

e2e 的测试数据在 `cmd/desktop/testdata/e2e/`，`run.sh` 每次把它复制到临时目录：

| 目录 | 内容 |
| --- | --- |
| `home/` | 复制成临时 `HOME`：`code/my-skills/` 下三个本地技能（alpha 带可执行脚本），以及第一次打开时各 Agent 全局目录里早就有的、不是库装的技能 |
| `zips/` | 打成 zip 供 URL 入库（delta、pair、trio），`site/` 是 well-known 站点 |
| `git/` | 每个子目录是一次提交新增的文件：`v1.0`（打 tag）、`main`、`release-v2`（release/v2 分支） |
| `hosts/` | 离线时代替 skills.sh 的搜索结果和 well-known 索引 |

Git 不保存的部分由 `run.sh` 现场补上：空目录、zip 文件、Git 仓库、指向临时目录的 `.gitconfig`、文件修改时间。

`e2e.mjs` 的断言依赖这些技能的名字和内容。改 `testdata/e2e/` 时，必须同时改 `e2e.mjs` 里用到它们的步骤，再跑一遍完整的 e2e。

## 在 app 里验证界面

开发数据是一套现成的技能库和临时 `HOME`，用来在 `wails3 dev` 里看界面、验证改动，不用每次自己造技能、入库、装出去。生成开发数据不联网，约 5 s。

### 前置条件

- [运行前端检查](#运行前端检查)的前置条件
- `wails3`，版本和安装方法见 `cmd/desktop/README.md` 的「准备」一节
- macOS 或 Linux。Windows 上技能库按 `USERPROFILE` 取主目录，换 `HOME` 隔离不了，`dev:seed` 和 `dev:seeded` 都拒绝运行。

### 步骤

1. 在 `cmd/desktop/` 下生成开发数据：

   ```sh
   wails3 task dev:seed
   ```

   命令只清空并重建 `.dev/seed/`，不动 `dev:test-db` 用的 `.dev/library.db`。生成中途失败时，命令删掉 `.dev/seed/` 并报错，不会留下只生成了一半的数据。

2. 用开发数据以开发模式启动 app：

   ```sh
   wails3 task dev:seeded
   ```

   窗口打开后，左栏「所有技能」有 10 个技能，「更新」有 5 个要处理的更新。

   Vite 的端口每次启动时自动选一个空闲的，别的工作区开着 `wails3 dev` 也不冲突。要固定端口时设 `WAILS_VITE_PORT`，如 `WAILS_VITE_PORT=9345 wails3 task dev:seeded`。

3. 核对窗口用的是开发数据：左栏底部的数据库位置必须是 `…/.dev/seed/library.db`；在右栏选中 **Claude Code**，列表顶部写着 5 个已安装、1 个未纳管。

   **核对不通过时，先退出这个窗口，不要安装或卸载任何技能。** 这个窗口读写的可能是你真实的 Agent 目录或数据库。常见的原因有两个：

   - 启动的不是 `dev:seeded`，如 `wails3 dev` 或 `dev:test-db`。它们打开真实技能库或 `.dev/library.db`，`HOME` 也是真实的。
   - 你看的是另一个窗口。装好的 Mechbox 和别的工作区的开发版窗口在 Dock 里也都叫 Mechbox。

   退出后，在本工作区的 `cmd/desktop/` 下重新运行第 2 步。

   同一个工作区里已经开着 `dev:seeded` 时，再运行一次只会把已有的窗口带到前台，新的 `dev:seeded` 随之结束。

4. 在 app 里操作，验证你的改动。

5. 可选：要把数据恢复到初始状态时，先退出 app，再运行第 1 步。第 1 步会删掉 app 正开着的数据库。

`dev:seeded` 把 `HOME` 换成 `.dev/seed/home`，所以安装不会写进真实的 Agent 目录。代价是 `gh` 的登录和 `~/.ssh` 在这里用不了。要调试真实的 GitHub 来源，改用 `cmd/desktop/README.md` 里的 `dev:test-db`。

### 几个工作区同时开窗口

几个 git 工作区（或几个 agent）可以同时各开一个开发版窗口，前提是每个工作区都按上面的步骤，用自己的开发数据启动：

1. 在每个工作区的 `cmd/desktop/` 下各运行一次 `wails3 task dev:seed`。开发数据生成在这个工作区自己的 `.dev/seed/` 下。
2. 在每个工作区各运行 `wails3 task dev:seeded`。

这样启动的窗口互不干扰：

- **单实例按数据库区分**：打开不同数据库的进程各开各的窗口；打开同一个数据库的第二个进程把已有窗口带到前台后退出，路径经过符号链接换了写法也一样。`dev:seeded` 打开的是本工作区的 `.dev/seed/library.db`，所以各工作区的窗口能同时开着，也不会被装好的 Mechbox 带到前台。
- **端口**：每次启动时自动选空闲的 Vite 端口。
- **数据**：`HOME` 是本工作区的 `.dev/seed/home`，安装、卸载只改本工作区的开发数据。

几个窗口在 Dock 里都叫 Mechbox。分辨某个窗口属于哪个工作区，看左栏底部的数据库位置。

下面两种启动方式不能在几个工作区同时用：

| 启动方式 | 同时开时会怎样 |
| --- | --- |
| `wails3 dev`、`wails3 task dev` | 都打开真实技能库，和装好的 Mechbox 共用一个实例：后启动的进程把已有窗口带到前台后退出，你看到的是别的工作区的窗口 |
| `wails3 task dev:test-db` | 每个工作区有自己的 `.dev/library.db`，窗口能同时开着；但 Agent 目录都按真实 `HOME` 算，几个窗口往同一批真实的 Agent 目录里安装、卸载，互相覆盖 |

同时跑 e2e 时，各自换端口和截图目录，见[运行端到端验证](#运行端到端验证)的前置条件。

### 开发数据里有什么

技能来自 skills.sh 上的 anthropics/skills 和 openai/skills，是提交在 `cmd/desktop/testdata/dev/git/` 里的快照。`devseed` 把同一个仓库的几个快照依次提交成本地 Git 仓库。临时 `HOME` 的 `.gitconfig` 让 `https://git.dev.test/<owner>/skills.git` 指向这些仓库。`devseed` 先按旧快照入库，再提交新快照、检查更新，用两段真实的提交历史造出下面的状态：

| 方面 | 内容 |
| --- | --- |
| 集合 `git.dev.test/anthropics/skills` | frontend-design 有新版本；claude-academy-guide 可能改名为 academy-guide；来源里的新技能 discernment-nudge |
| 集合 `git.dev.test/openai/skills` | 更新过一次，有历史可以回滚；openai-docs 有新版本并改动脚本；sora 和 doc 来源里已删除；新技能 define-goal |
| 集合 `~/code/my-skills` | 本地目录来源，两个技能 |
| 没有来源的技能 | internal-comms，从 Claude Code 全局的未纳管技能入库 |
| 组 | 「前端」组安装到 Codex 全局；「文档」没有安装 |
| 项目 | web-app 是 Git 仓库，里面提交了 `.claude/skills/playwright`，和 Claude Code 全局的 playwright 同名；old-project 不在这台机器上 |
| 安装状态 | 当前版本；被手动改过（Claude Code 全局的 playwright）；文件夹不在了（Claude Code 全局的 sora）；项目不在这台机器上 |
| 未纳管技能 | webapp-testing 是 npx skills 的布局：真实文件夹在 Codex 全局，Claude Code 全局里是符号链接；来源记录认出来源 `git.dev.test/anthropics/skills` |

开发数据已经标记为打开过 app，启动时不弹第一次打开的确认。两个 `git.dev.test` 集合在界面上显示为「Git 仓库」，不是 GitHub。按 GitHub 来源入库只能跟踪真实的默认分支，造不出要处理的更新。

改了 `devseed/` 或快照以后，以重新生成的结果为准，同时改这张表。

## 更换开发数据的技能快照

只有要换开发数据的素材时才做这一节；平时生成开发数据不联网，也不需要这一步。

### 前置条件

- 能访问 github.com
- `git`

### 步骤

1. 编辑 `cmd/desktop/testdata/dev/pull.sh`，改要导出的仓库、完整提交号和技能目录。
2. 在仓库根目录导出快照：

   ```sh
   cmd/desktop/testdata/dev/pull.sh
   ```

   脚本对每个提交只浅拉取这一个提交，稀疏检出要的几个目录，写到 `testdata/dev/git/<仓库>/<提交号前 7 位>/`。两个仓库共 5 个提交，约 15 s。

3. 如果换了提交或技能，改 `cmd/desktop/devseed/main.go` 里对应的快照名和技能名。
4. 生成开发数据，在 app 里核对：

   ```sh
   wails3 task dev:seed
   ```

5. 改[开发数据里有什么](#开发数据里有什么)这张表。

快照必须带上每个技能自己的许可证文件。现在的快照都是 Apache 2.0，可以再分发。
