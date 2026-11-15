# NativeSelect

原生下拉框 NativeSelect,以及外观相同的触发按钮 SelectButton。

- 外观和普通按钮一样:圆角 `radius-control`(7 px),`control` 的灰底,没有边框和投影〔已确认 2026-11-15:按钮照 macOS 设置页〕。高 24 px,和同一行的按钮相同(8.2)。
- 细边用真的 0.5 px 边框画,颜色和 `shadow-control` 里那一圈细边相同:WKWebView 不给原生下拉框画 box-shadow,只靠投影的话浅色下白框压在白底上看不出是控件。
- 焦点:3 px 的 `primary-soft` 光圈。
- 选项由系统菜单弹出,只给框本身套样式。SelectButton 给原生下拉框做不到的选择(如可勾选几项的弹层)当触发按钮。

你提供:选项和当前值。
