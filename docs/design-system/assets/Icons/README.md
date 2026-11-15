# Icons

应用里的内联 SVG 图标,从 `cmd/desktop/frontend/src/components/ui/icon.tsx`(来源图标在 `components/app/icons.tsx`)原样搬来,文件名和代码里的名字一致。

- 24 格,只描边、不填色,笔画 1.8 到 2.5。单色,墨色是 `currentColor`:内联使用时跟随文字颜色;当图片用时是黑色。
- 默认 14 px;按钮里 12 px 到 14 px,小图标按钮里 11 px。
- 31 个界面图标:`plus`、`search`、`folder`、`file`、`globe`、`bolt`、`layers`、`tag`、`gear`、`cloud`、`up`、`sidebarL`、`sidebarR`、`close`、`chev`、`chevL`、`check`、`expand`、`fold`、`down`、`graph`、`more`、`minus`、`fit`、`list`、`grid`、`refresh`、`edit`、`info`、`database`、`inbox`(「不在技能库里」一节的图标)。
- `more`:节标题上「⋯」菜单的三个点，笔画 3、圆头。
- `info`:新增〔暂定〕。圆圈里的 i,给 InfoTip(悬停看大段说明)。笔画 2、圆头，和 `alert` 同规格;墨色用 `muted-foreground`,悬停时 `foreground`。
- 5 个来源图标,按来源类型取:`SourceGitHub`、`SourceGit`(同一个图形)、`SourceURL`、`SourceWellKnown`(同一个图形)、`SourceLocal`(和 `folder` 相同)。
- `bot`:认不出的 Agent 的机器人图标,来自 `parts.tsx`。用 `subtle` 的灰色。
- `alert`:新增〔暂定〕。三角感叹号,给「必须处理」的标签、Notice、错误文字和失败的 toast。现有的图标里没有警示类图标。
