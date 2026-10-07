# 设计决策记录

每篇只记录一个现在仍然成立的决定，以及做这个决定的原因。术语以 [CONTEXT.md](../../CONTEXT.md) 为准；具体行为以 `internal/library` 的 doc comment（`go doc -all ./internal/library`）和测试为准。决定改了就直接改对应的那一篇，历史交给 git。

## 总览

- [0009 用户操作流程：先入库，再安装](0009-user-flow-fetch-then-install.md)
- [0010 产品承诺：用户没有点头，Agent 用的技能就不变](0010-product-promises.md)

## 库与数据

- [0001 库的数据集中存在一个数据库里](0001-library-data-in-one-database.md)
- [0002 每个版本完整保存，相同内容的文件只存一份](0002-versions-store-each-file-once.md)
- [0003 发布前不迁移旧库](0003-no-migrations-before-release.md)

## 来源

- [0004 来源分类型，集合由来源推导](0004-sources-are-typed.md)

## 安装

- [0005 作用域按 Agent 和层级记录，不按路径](0005-scope-is-agent-and-level-not-path.md)
- [0006 组安装是一个持续的选择](0006-group-install-is-a-standing-choice.md)

## 接口与应用

- [0007 错误码按界面要做的动作划分](0007-error-codes-follow-ui-actions.md)
- [0008 桌面端用 Wails v3，与库在同一个进程里](0008-desktop-is-wails-v3-in-process.md)
