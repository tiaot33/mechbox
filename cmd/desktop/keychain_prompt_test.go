package main

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

const testPrompt = dbus.ObjectPath("/org/freedesktop/secrets/prompt/p1")

func completed(path dbus.ObjectPath, dismissed bool, result any) *dbus.Signal {
	return &dbus.Signal{Path: path, Name: secretsPrompt + ".Completed", Body: []any{dismissed, dbus.MakeVariant(result)}}
}

func TestAwaitPromptReturnsResult(t *testing.T) {
	signals := make(chan *dbus.Signal, 3)
	// 别的路径、别的信号都跳过，只认这个 prompt 的 Completed。
	signals <- completed("/org/freedesktop/secrets/prompt/other", false, "wrong")
	signals <- &dbus.Signal{Path: testPrompt, Name: "org.freedesktop.portal.Settings.SettingChanged"}
	signals <- completed(testPrompt, false, dbus.ObjectPath("/org/freedesktop/secrets/collection/login"))
	result, err := awaitPrompt(context.Background(), signals, testPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := result.Value().(dbus.ObjectPath); got != "/org/freedesktop/secrets/collection/login" {
		t.Fatalf("result = %v", result)
	}
}

func TestAwaitPromptErrors(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name    string
		ctx     context.Context
		signals func() chan *dbus.Signal
		want    error
	}{
		{"点了取消", context.Background(), func() chan *dbus.Signal {
			ch := make(chan *dbus.Signal, 1)
			ch <- completed(testPrompt, true, "")
			return ch
		}, errPromptDismissed},
		{"连接断开", context.Background(), func() chan *dbus.Signal {
			ch := make(chan *dbus.Signal)
			close(ch)
			return ch
		}, errPromptClosed},
		{"一直没人处理", cancelled, func() chan *dbus.Signal { return make(chan *dbus.Signal) }, errPromptTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := awaitPrompt(c.ctx, c.signals(), testPrompt); !errors.Is(err, c.want) {
				t.Fatalf("awaitPrompt = %v; want %v", err, c.want)
			}
		})
	}
}
