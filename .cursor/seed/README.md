# 测试用种子数据 · Seed fixtures

这些是为方便手动测试 `library` 门面而预置的固件。`install`(见 `.cursor/install.sh`)
会调用 `.cursor/seed-sandbox.sh`,把本目录内容部署到一个**可写沙盒** `~/skills-sandbox/`,
避免激活投影写进仓库工作树而弄脏 git 状态。

沙盒布局:

```text
~/skills-sandbox/
├── skills/                 # 可作为 LocalSource 安装的 skill 目录
│   ├── hello-world/        #   最小 skill(仅 SKILL.md)
│   ├── git-commit-helper/  #   含可执行脚本 + references/
│   └── markdown-linter/    #   含 scripts/ + references/ + assets/
└── projects/               # 可注册为 Registered project 的项目根
    ├── demo-web-app/
    └── demo-cli-tool/
```

## 快速上手

```go
ctx := context.Background()
home, _ := os.UserHomeDir()
sandbox := filepath.Join(home, "skills-sandbox")

lib, _ := library.Open(filepath.Join(sandbox, "skills.db"))
defer lib.Close()

// 安装一个 skill
skill, _ := lib.Install(ctx, library.LocalSource{
    Path: filepath.Join(sandbox, "skills", "git-commit-helper"),
})

// 注册项目并激活到它的项目级 Scope
project, _ := lib.RegisterProject(ctx, filepath.Join(sandbox, "projects", "demo-web-app"))
_ = lib.Activate(ctx, skill.ID,
    library.ProjectScope{Agent: library.AgentClaudeCode, ProjectID: project.ID},
    library.ActivateOptions{})
```

沙盒是可丢弃的:删掉 `~/skills-sandbox/` 后重跑 `.cursor/seed-sandbox.sh` 即可还原固件。
重跑不会删除你在沙盒里新增的文件(如激活产生的投影副本)。
