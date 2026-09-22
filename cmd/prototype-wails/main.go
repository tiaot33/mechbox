// PROTOTYPE —— 用完即弃。Wails 桌面端 UI 原型:三个结构不同的变体,
// 回答"设计稿(skill-manager-electron-2.html)的外观下,集合 / 组 / Scope / 激活该怎么组织"。
// 数据来自 stub.go 的内存桩服务(复用 library 包的真实类型),不连真实 Library、不写磁盘。
//
// 桌面窗口:go run -tags desktop,production ./cmd/prototype-wails
// 浏览器:  go run ./cmd/prototype-wails -browser   然后打开 http://localhost:34115/?variant=A
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"reflect"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	browser := flag.Bool("browser", false, "不开窗口,改用浏览器访问 http://localhost:34115")
	flag.Parse()

	app := NewApp()
	if *browser {
		serveBrowser(app)
		return
	}

	err := wails.Run(&options.App{
		Title:            "技能管理器 · PROTOTYPE",
		Width:            1400,
		Height:           880,
		MinWidth:         1000,
		MinHeight:        640,
		AssetServer:      &assetserver.Options{Assets: frontendFS()},
		BackgroundColour: &options.RGBA{R: 238, G: 239, B: 242, A: 255},
		OnStartup:        func(ctx context.Context) {},
		Bind:             []any{app},
		Mac: &mac.Options{
			TitleBar:             mac.TitleBarHiddenInset(),
			WebviewIsTransparent: true,
			WindowIsTranslucent:  true,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func frontendFS() fs.FS {
	sub, err := fs.Sub(assets, "frontend")
	if err != nil {
		panic(err)
	}
	return sub
}

// serveBrowser 在浏览器里跑同一套前端:POST /call/<方法名> 按 JSON 数组参数反射调用 App 的方法,
// 模拟 Wails 的 window.go.main.App 绑定,返回 {"result": ...} 或 {"error": "..."}。
func serveBrowser(app *App) {
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServerFS(frontendFS()))
	mux.HandleFunc("POST /call/{method}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		result, err := callMethod(app, r.PathValue("method"), r)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	})
	addr := "localhost:34115"
	log.Printf("PROTOTYPE 浏览器模式:http://%s/?variant=A", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}

func callMethod(app *App, name string, r *http.Request) (any, error) {
	method := reflect.ValueOf(app).MethodByName(name)
	if !method.IsValid() {
		return nil, fmt.Errorf("未知方法 %s", name)
	}
	var raw []json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, err
	}
	kind := method.Type()
	if len(raw) != kind.NumIn() {
		return nil, fmt.Errorf("%s 需要 %d 个参数", name, kind.NumIn())
	}
	args := make([]reflect.Value, len(raw))
	for i, arg := range raw {
		value := reflect.New(kind.In(i))
		if err := json.Unmarshal(arg, value.Interface()); err != nil {
			return nil, err
		}
		args[i] = value.Elem()
	}
	out := method.Call(args)
	// 约定与 Wails 相同:最后一个返回值若是 error 则视为失败。
	if last := out[len(out)-1]; last.Type() == reflect.TypeFor[error]() {
		if !last.IsNil() {
			return nil, last.Interface().(error)
		}
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out[0].Interface(), nil
}
