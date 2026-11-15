# 旧变量怎样对应新规则

现有代码(`cmd/desktop/frontend/src/index.css`)用 28 个颜色变量表示状态和动作。改代码时按下表把它们换成这里的变量。

| 旧变量 | 现在用在哪 | 换成什么 |
| --- | --- | --- |
| `primary` 一组,8 个 | 主要按钮、选中、开关、链接、焦点圈 | `primary`、`on-primary`、`primary-soft` |
| `ok` 一组,4 个 | 已是最新、完成的对勾 | 平常档的灰字 `muted-foreground`;对勾用 `foreground` |
| `warn` 一组,5 个 | 有新版本、可能改名、同名、改动脚本、需要重新安装 | 按 Badge 的三档分到「要处理」或「必须处理」 |
| `bad` 一组,5 个 | 失败、被手动改过、文件夹不在了、移除 | 「必须处理」(较深的灰底、半粗字加图标);移除用普通按钮，确认时用实心强调 |
| `group` 一组,6 个 | 组的标签、随组安装的开关和标记 | 平常档;开关是灰色开着(`subtle` 的 80%) |
| `foreground`、`foreground-secondary`、`muted-foreground`、`subtle` | 四档文字 | 同名变量,色值换成按对比度定的新值 |

## 其他变量

| 旧变量 | 换成什么 |
| --- | --- |
| `border`、`border-strong`、`border-subtle`、`hairline` | 输入框等控件和表面的边都用 `border`,普通按钮不描边;分隔线用 `row-line` |
| `row-line` | `row-line` |
| `fill-faint`、`fill-subtle`、`fill`、`fill-strong`、`neutral-soft`、`nav-active` | `fill` |
| `panel`、`control` | `panel` |
| `glass-tile`、`glass-card` | 不透明的 `tile` 和 `panel` |
| `glass-row-hover`、`glass-row-selected` | 高亮用 `fill`;选中的行用 `primary-soft` |
| `--radius-card`(16 px)、`--radius-box`(12 px) | `radius-card`(10 px) |
| `--radius-tile`(14 px) | `radius-tile`,不变 |
| `rounded-full` 的按钮和小胶囊,以及 7、8、9 px 的圆角 | `radius-control`(6 px);Notice 用 `radius-card` |
| 认不出的 Agent 按代码取的 `hsl()` 颜色 | `subtle` |
| `mark-ring` | `panel` |
| `--glass-sat`(浅色 0.8、深色 0.6) | `glass-saturate` |
| `window-backdrop` 的四角淡彩渐变 | 去掉 |
| `body` 上的 `-webkit-font-smoothing: antialiased` | 去掉 |
| 10.5、11.5、12、12.5、13.5 px 的字 | 归到 11 或 13 px;卡片标题用 15 px |
| 500 的字重(`font-medium`) | 400 或 600 |
