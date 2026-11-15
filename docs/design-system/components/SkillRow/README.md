# SkillRow

技能行:列表里的一个技能。

- 技能名和描述同为 13 px:技能名用 `mono-strong` 加 `foreground`,描述用 `body` 加 `foreground-secondary`。两者靠字体和深浅区分,不靠字号(5.4)。
- 状态用 Badge 的三档;日期用 `caption`,开等宽数字。
- 选中的行用淡绿底 `primary-soft`;行与行之间一条 `row-line`。
- 开关:直接安装的是绿色开着;随组安装或来自全局的是灰色开着。

你提供:技能名、描述、状态、开关。
