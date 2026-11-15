# NativeSelect

原生下拉框 NativeSelect,以及外观相同的触发按钮 SelectButton。

- 圆角 `radius-control`(6 px,原来是 7 px),`border` 的浅边框(聚焦时换成 `primary` 加光圈)〔已确认 2026-11-15〕;高度和同一行的按钮相同:`control` 或 `control-sm`(8.2)。
- 选项由系统菜单弹出,只给框本身套样式。SelectButton 给原生下拉框做不到的选择(如可勾选几项的弹层)当触发按钮。

你提供:选项和当前值。
