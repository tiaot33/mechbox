package main

// startupErrorText 是启动失败时提示框的标题与正文;libraryPath 为空时(还没定下数据库位置)不写那一行。
// 这时数据库可能还没打开，读不到界面语言的设置，所以按系统语言。
func startupErrorText(err error, libraryPath string) (title, message string) {
	if LanguageSystem.resolve() == LanguageZh {
		title, message = "Mechbox 无法启动", "启动时出错，Mechbox 已退出。\n\n"
		if libraryPath != "" {
			message += "数据库位置：" + libraryPath + "\n\n"
		}
		return title, message + err.Error()
	}
	title, message = "Mechbox can't start", "Mechbox ran into an error while starting and has quit.\n\n"
	if libraryPath != "" {
		message += "Database location: " + libraryPath + "\n\n"
	}
	return title, message + err.Error()
}
