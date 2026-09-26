// demo 演示命令入口如何调用同一 module 内的 internal/library。
package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	// 导入路径由 go.mod 的 module 路径加包目录组成，调用时仍使用包名 library。
	"github.com/tiaot33/skills-tui/internal/library"
)

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 管理资源生命周期，返回 main 时已经执行 Close，之后退出进程也不会跳过清理。
func run(ctx context.Context) (err error) {
	// 使用内存数据库便于反复试跑；需要持久化时，改为数据库文件路径，例如 "skills.db"。
	lib, err := library.Open(":memory:")
	if err != nil {
		return fmt.Errorf("open library: %w", err)
	}
	// 保留业务调用与关闭资源两处可能产生的错误。
	defer func() { err = errors.Join(err, lib.Close()) }()

	// 通过技能库(Library)的公开方法调用后端，context 随调用传入。
	agents, err := lib.ListAgents(ctx)
	if err != nil {
		return fmt.Errorf("list agents: %w", err)
	}

	fmt.Println("Library 已打开（内存数据库），支持的 Agent：")
	for _, agent := range agents {
		fmt.Printf("- %s (%s)\n", agent.Name, agent.Code)
	}
	return nil
}
