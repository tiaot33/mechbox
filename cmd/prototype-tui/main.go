// prototype-tui 是一个一次性原型,回答一个问题:技能管理器的主界面该长什么样?
//
// 三个结构上彼此不同的变体跑在同一份真实数据上(真的 Library、真的磁盘投影),
// 用 ←/→ 切换。它不是产品代码:没有测试、没有错误处理、不做任何写操作。
// 用完即弃,见 docs/prototypes/ 里的结论记录。
//
// 运行:go run ./cmd/prototype-tui          (从变体 A 开始)
//       go run ./cmd/prototype-tui -variant b
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

func main() {
	start := flag.String("variant", "a", "起始变体:a(双栏浏览) / b(激活矩阵) / c(待办流)")
	dump := flag.Bool("dump", false, "不进交互界面,把三个变体依次打印到 stdout")
	flag.Parse()

	if *dump {
		if err := runDump(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(context.Background(), *start); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, start string) error {
	env, err := setupSeed(ctx)
	if err != nil {
		return fmt.Errorf("准备原型数据: %w", err)
	}
	defer env.cleanup()

	snap, err := loadSnapshot(ctx, env)
	if err != nil {
		return fmt.Errorf("读取数据: %w", err)
	}

	m := model{ctx: ctx, env: env, snap: snap, current: parseVariant(start)}
	_, err = tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// runDump 依次渲染三个变体,用于无 TTY 环境下的检查。
func runDump(ctx context.Context) error {
	env, err := setupSeed(ctx)
	if err != nil {
		return fmt.Errorf("准备原型数据: %w", err)
	}
	defer env.cleanup()
	snap, err := loadSnapshot(ctx, env)
	if err != nil {
		return fmt.Errorf("读取数据: %w", err)
	}
	for v := variantA; v < variantCount; v++ {
		m := model{ctx: ctx, env: env, snap: snap, current: v, width: 120, height: 40}
		fmt.Printf("\n\033[7m  变体 %d —— %s  \033[0m\n\n", int(v)+1, variantNames[v])
		fmt.Println(m.View())
	}
	return nil
}

func parseVariant(s string) variant {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "b", "2":
		return variantB
	case "c", "3":
		return variantC
	}
	return variantA
}

// ---- 排版助手 ----

// fit 把字符串裁剪并补齐到 w 个终端列宽。中文占两列,不能用 fmt 的 %-Ns。
func fit(s string, w int) string {
	return runewidth.FillRight(runewidth.Truncate(s, w, "…"), w)
}

// truncate 只裁不补。
func truncate(s string, w int) string {
	return runewidth.Truncate(s, w, "…")
}

// wrap 按列宽折行,用于详情里的描述文字。
func wrap(s string, w int) string {
	if s == "" {
		return "（无描述）"
	}
	// 中文逐字断行,ASCII 词尽量不拆开。
	var lines []string
	var cur, word string
	flushWord := func() {
		if word == "" {
			return
		}
		if runewidth.StringWidth(cur+word) > w && cur != "" {
			lines = append(lines, cur)
			cur = ""
		}
		cur += word
		word = ""
	}
	for _, r := range s {
		if r < 128 && r != ' ' {
			word += string(r)
			continue
		}
		flushWord()
		if r == ' ' && cur == "" {
			continue
		}
		if runewidth.StringWidth(cur+string(r)) > w {
			lines = append(lines, cur)
			cur = ""
			if r == ' ' {
				continue
			}
		}
		cur += string(r)
	}
	flushWord()
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return dimSt.Render("未知")
	}
	return t.Local().Format("2006-01-02 15:04")
}
