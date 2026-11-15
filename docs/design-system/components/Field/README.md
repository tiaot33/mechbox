# Field

表单零件:带标题的输入项 Field、字段下的错误 FieldError、灰色的说明 Hint。

- 标题用 `body` 加 `foreground-secondary`;说明用 `caption` 加 `muted-foreground`。
- 错误文字用主要档(`foreground`),前面加 `warn` 橙色的警示图标,不用红色(第 11 节)。图标是 `assets/Icons/alert.svg`〔暂定〕。
- 说明和错误都是 11 px,不得比标注档更淡(4.3)。

你提供:标题、输入控件、可选的说明或错误。
