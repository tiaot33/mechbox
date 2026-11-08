package main

// Appearance 是界面外观的设置值。
type Appearance string

const (
	AppearanceSystem Appearance = ""      // 跟随系统
	AppearanceLight  Appearance = "light" // 浅色
	AppearanceDark   Appearance = "dark"  // 深色
)

func (a Appearance) valid() bool {
	return a == AppearanceSystem || a == AppearanceLight || a == AppearanceDark
}

// applyAppearance 让窗口背后的系统材质跟着外观变：macOS 上是整个 app 的外观(窗口背后的毛玻璃、菜单与系统弹窗),
// Windows 上是窗口的深浅色(Mica 与标题栏)和没有 Mica 时的不透明底色。跟随系统时 macOS 交给系统;
// Windows 上由 watchSystemAppearance 在系统切换深浅色时再调一次。界面本身的配色由前端按设置与 prefers-color-scheme 切换。
func (a *AppService) applyAppearance(appearance Appearance) {
	setNativeAppearance(a.window, appearance)
}
