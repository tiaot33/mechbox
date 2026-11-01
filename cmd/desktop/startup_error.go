package main

// startupErrorText 是启动失败时提示框的标题与正文。这时数据库可能还没打开，读不到界面语言的设置，所以按系统语言。
func startupErrorText(err error) (title, message string) {
	if LanguageSystem.resolve() == LanguageZh {
		return "Mechbox 无法启动", "启动时出错，Mechbox 已退出。\n\n" + err.Error()
	}
	return "Mechbox can't start", "Mechbox ran into an error while starting and has quit.\n\n" + err.Error()
}
