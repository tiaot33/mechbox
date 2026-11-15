# SwipeToast

可下滑关掉的 toast:保留毛玻璃,圆角 `radius-card`(10 px,原来是 14 px)。

- 底用 `glass-clear`,`shadow-toast`。字用 `body`。
- 底边的引线用主要档的文字色 `foreground`(原来是蓝色,失败时是红色)。
- 失败时不换颜色,前面加图标 `assets/Icons/alert.svg`〔暂定〕。
- 升起 400 ms、收起 280 ms,`cubic-bezier(0.23, 1, 0.32, 1)`(10.2)。系统要求减少动态效果时不位移,只淡入淡出。

你提供:文字、是否失败、停留多久。
