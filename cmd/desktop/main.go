// desktop 是技能管理器的桌面端：Wails v3 应用，与技能库(Library)同进程运行。
//
// 在本目录运行(需要 wails3 CLI 与 pnpm):
//
//	wails3 dev     # 开发模式，前端热更新
//	wails3 build   # 构建到 bin/
//
// 未构建前端时 frontend/dist 只有占位文件，纯 Go 的 go build / go vet / go test 仍然可以通过。
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v3/pkg/application"

	"github.com/tiaot33/skills-tui/internal/library"
)

// assets 是前端构建产物;all: 前缀让占位的点文件也被嵌入，保证目录非空时可编译。
//
//go:embed all:frontend/dist
var assets embed.FS

// mainWindowName 是主窗口的名字。
const mainWindowName = "main"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	path, err := defaultLibraryPath()
	if err != nil {
		return err
	}
	libraryService := NewLibraryService(path)
	appService := NewAppService(filepath.Join(filepath.Dir(path), settingsFileName), libraryService)
	app := application.New(application.Options{
		Name:        "技能管理器",
		Description: "管理 coding agent 的 Agent Skills",
		Services: []application.Service{
			application.NewService(libraryService),
			application.NewService(appService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		MarshalError: marshalError,
		// 单实例锁在 application.New 里检查：第二个实例在这里就退出，不会打开技能库。
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: "com.tiaot33.skills-manager",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				appService.showWindow()
			},
		},
		// 不让 macOS 在最后一个窗口关闭时退出：留在菜单栏时窗口只是隐藏，退出与否由 AppService 按设置决定。
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
	})

	window := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:      mainWindowName,
		Title:     "技能管理器",
		Width:     1400,
		Height:    880,
		MinWidth:  1000,
		MinHeight: 640,
		Mac: application.MacWindow{
			InvisibleTitleBarHeight: 50,
			// 毛玻璃要求 WebView 透明，这在 Wails v3 里只有带 private_mac_apis 构建标签时才生效
			// (Taskfile 已默认加上);不带时 WebView 仍画白底，毛玻璃透不出来。
			Backdrop: application.MacBackdropTranslucent,
			TitleBar: application.MacTitleBarHiddenInset,
		},
		// 透明背景让 macOS 原生毛玻璃透出来;底色由前端的半透明层给出。
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL:              "/",
	})
	appService.attach(app, window)

	return app.Run()
}

// marshalError 把绑定方法返回的任意 error 规范成 *library.Error 再编码，
// 前端从错误的 cause 里拿到稳定的 code 与 details;归类规则只在 Library 里写一份。
func marshalError(err error) []byte {
	data, marshalErr := json.Marshal(library.ErrorOf(err))
	if marshalErr != nil {
		// 返回 nil 时 Wails 退回默认的错误编码。
		return nil
	}
	return data
}
