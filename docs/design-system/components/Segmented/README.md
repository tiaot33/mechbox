# Segmented

分段选择:一组按钮,选中的一段垫着一块 `raised` 底。

- 外框圆角 `radius-control`(6 px),`fill` 的槽,`border-control` 边框,内边距 `space-2`;总高 28 px,和 `control` 一致。
- 选中的一段:`raised` 底加 `shadow-segment`,字用 `foreground`;没选中的用 `foreground-secondary`。里面那一段的圆角 4 px 照现有。
- 换选中项时滑块用不回弹的弹簧滑过去,320 ms;系统要求减少动态效果时直接跳过去(10.2、10.3)。
- 选中不用绿色:这是在几个并列的选项里切换,不是选中一行。

你提供:选项(值和文字)、当前值。
