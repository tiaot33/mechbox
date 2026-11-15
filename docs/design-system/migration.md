# 旧变量怎样对应新规则

现有代码(`cmd/desktop/frontend/src/index.css`)用 28 个颜色变量表示状态和动作。改代码时按下表把它们换成这里的变量。

| 旧变量 | 现在用在哪 | 换成什么 |
| --- | --- | --- |
| `primary` 一组,8 个 | 主要按钮、选中、开关、链接、焦点圈 | `primary`、`on-primary`、`primary-soft` |
| `ok` 一组,4 个 | 已是最新、完成的对勾 | 平常档的灰字 `muted-foreground`;对勾用 `foreground` |
| `warn` 一组,5 个 | 有新版本、可能改名、同名、改动脚本、需要重新安装 | 按提醒的三级重新归:有新版本、可能改名归「提示」(蓝);同名、改动脚本、需要重新安装归「注意」(橙)。现在的 `warn`、`warn-fg` 是「注意」一级的橙色(圆点和字) |
| `bad` 一组,5 个 | 失败、被手动改过、文件夹不在了、移除 | 失败、文件夹不在了归「严重」(红:`critical`、`critical-fg`);被手动改过归「注意」(橙);移除用普通或轻按钮，确认时用实心强调 |
| `group` 一组,6 个 | 组的标签、随组安装的开关和标记 | 列表里的组名只是 `muted-foreground` 的字;技能详情里的组是中性的小标签(Chip);标记用平常档;开关是灰色开着(`subtle` 的 80%) |
| `foreground`、`foreground-secondary`、`muted-foreground`、`subtle` | 四档文字 | 同名变量,色值换成按对比度定的新值 |

## 其他变量

| 旧变量 | 换成什么 |
| --- | --- |
| `border`、`border-strong`、`border-subtle`、`hairline` | 输入框、搜索框和表面的边都用 `border`;普通按钮和下拉框的细边在 `shadow-control` 里;中性的小标签用 `tag-border`;分隔线用 `row-line` |
| `row-line` | `row-line` |
| `fill-faint`、`fill-subtle`、`fill`、`fill-strong`、`neutral-soft`、`nav-active` | `fill` |
| `panel`、`control` | 列表框、输入框的底用 `panel`;普通按钮、下拉框、分段选择选中的一段用新的 `control` |
| `glass-tile`、`glass-card` | 不透明的 `tile`;卡片改成分区,没有自己的底 |
| `glass-row-hover`、`glass-row-selected` | 高亮用 `fill`;选中的行用 `primary-soft` |
| `--radius-card`(16 px)、`--radius-box`(12 px) | 列表框用 `radius-card`(10 px);卡片改成分区,没有圆角 |
| `--radius-tile`(14 px) | `radius-tile`,不变 |
| `rounded-full` 的按钮和小胶囊,以及 7、8、9 px 的圆角 | `radius-control`(7 px);Notice 用 `radius-card` |
| 认不出的 Agent 按代码取的 `hsl()` 颜色 | `subtle` |
| `mark-ring` | `panel` |
| `--glass-sat`(浅色 0.8、深色 0.6) | `glass-saturate` |
| `window-backdrop` 的四角淡彩渐变 | 去掉 |
| `body` 上的 `-webkit-font-smoothing: antialiased` | 去掉 |
| 10.5、11.5、12、12.5、13.5 px 的字 | 归到 11 或 13 px;分区标题和列表的节标题用 15 px |
| 500 的字重(`font-medium`) | 400 或 600 |
