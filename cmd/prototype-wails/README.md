# PROTOTYPE · Wails 桌面端 UI 变体

> 一次性原型，不进 main。选定方向后按正式代码重写，本目录整体丢弃。

**要回答的问题**:参考设计 `skill-manager-electron-2.html` 的外观下，真实领域概念——集合(按来源自动)、组(自建场景、激活单元)、Scope(Agent 全局 / Agent + 已登记项目)、激活(独立 / 经由组、投影健康状态)——该怎么组织进界面。设计稿里的"场景 / 分组 / 来源"分类和 Cursor、Gemini CLI 都是占位，这里全部换成 Library 实际提供的东西。

## 运行

```sh
# 桌面窗口(Wails v2,无需安装 wails CLI)
go run -tags desktop,production ./cmd/prototype-wails

# 浏览器(可用 ?variant= 分享链接)
go run ./cmd/prototype-wails -browser    # 打开 http://localhost:34115/?variant=A
```

底部深色胶囊或 ← / → 键切换变体。

## 变体

| 键 | 名称 | 主轴 | 主要操作 |
|---|---|---|---|
| A | 设计稿三栏 | 技能库(左栏按集合 / 组筛选) | 右栏选中一个作用域 → 卡片上出现它的激活开关;选中组时出现"整组激活"横幅;抽屉里有全部作用域的激活表 |
| B | 作用域优先 | 作用域(左栏是全局 + 项目 × Agent 树) | 当前作用域的组开关 → 已激活列表(来源、状态、重写、停用)→ 从库中添加;详情固定在右侧 |
| C | 矩阵总览 | 技能 × 作用域网格 | 单元格切换独立激活;按组分节时节标题格 = 整组开关;一眼看出 stale / modified / missing |

## 数据

`stub.go` 是内存桩：直接复用 `internal/library` 的类型(SkillSummary、Collection、Group、Activation 状态等),照搬几条会影响 UI 的规则——独立与分组来源分开记录、仅经由分组激活的技能不能单独停用(FailedPrecondition)、停用组只撤回该组来源、加入组不自动投影、组名重名追加 `#N`。重启即复原，不连真实 Library、不写磁盘。安装、市场、同步、登记项目只弹提示。
