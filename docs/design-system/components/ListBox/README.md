# ListBox

弹窗里的列表框 ListBox 和它的行 ListRow:候选、作用域、结果逐行列出。

- 列表框:`panel` 底,`border` 边框(它是表面的边,不是控件),圆角 `radius-card`(10 px,原来是 12 px)。
- 行:左右 `space-12`、上下 `space-8`,行与行之间一条 `row-line`。
- 选中的行用淡绿底 `primary-soft`(原来是淡蓝);悬停用 `fill`。选中的行上的字仍用 `foreground`。
- 技能名用 `body-strong`,日期用 `caption`。

你提供:每一行的内容、哪些行可点、哪一行选中。
