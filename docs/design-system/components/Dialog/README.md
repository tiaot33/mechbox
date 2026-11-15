# Dialog

弹窗:改成不透明(9.2),`tile` 底,`border` 边框,圆角 `radius-tile`(14 px),`shadow-dialog`。

- 宽 `dialog`(560 px),宽版 `dialog-wide`(760 px),贴着窗口上部 10% 处出现(10.4)。样例按栏宽缩小了。
- 标题用 `heading`。页脚上面一条 `row-line`,按钮靠右,间隔 `space-8`。
- 一个弹窗里主要按钮只有一个(7.2)。确认删除的弹窗里,确认按钮用实心强调,不用绿色(7.3)。
- 背后的遮罩用 `scrim`〔暂定〕。
- 进场 140 ms ease-out;从触发的元素放大 380 ms、缩回 260 ms,`cubic-bezier(0.32, 0.72, 0, 1)`(10.2)。

你提供:标题、正文、页脚的按钮。
