# Popover

弹层:保留毛玻璃(9.1),圆角 `radius-card`(10 px,原来是 12 px)。

- 底用 `glass`,模糊 `glass-blur`(24 px),外面一圈 `glass-edge` 加 `shadow-popover`。
- 饱和度 `glass-saturate`〔暂定〕不提高;深色的底色〔暂定〕。
- 系统要求减少透明度或开了强制颜色时,换成 `tile` 的不透明底,不模糊(9.4)。
- 进场 140 ms ease-out(10.2)。

你提供:里面的内容。
