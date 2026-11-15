# SearchInput

带放大镜图标的搜索框,用在顶栏和技能市场。

- 高 24 px,圆角 `radius-control`(7 px),和输入框、按钮一样〔已确认 2026-11-15:看过实际效果后改〕;`border` 的浅边框,`panel` 底,图标用 `assets/Icons/search.svg`。
- 聚焦时边框变成 `primary`(原来是蓝色),外面加 3 px 的 `primary-soft` 光圈。
- 后缀里的快捷键是一个小框:`border` 的边,圆角 4 px。

你提供:占位文字、图标、可选的后缀(如快捷键)。
