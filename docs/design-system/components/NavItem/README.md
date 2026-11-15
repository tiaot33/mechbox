# NavItem

左栏和作用域栏的导航行。

- 行高照搬:顶层 30 px,分节里的集合和组 28 px;圆角 `radius-control`。
- 平时用 `foreground-secondary`;悬停用 `fill` 底加 `foreground`。
- 选中:顶层的页面(技能库、更新、设置)用 `fill` 的灰底加半粗;集合、组、Agent 用淡绿底 `primary-soft` 加半粗的 `foreground`(原来是淡蓝底、蓝字)。
- 集合名是标识符,用 `mono`;组名用无衬线。
- 左栏默认宽 `sidebar`(210 px),作用域栏 `scope-bar`(270 px)。两栏透出窗口背后的系统材质(9.3),样例里画在 `tile` 上。

你提供:图标、文字、计数、是否选中。
