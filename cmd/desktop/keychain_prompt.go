package main

import (
	"context"
	"errors"

	"github.com/godbus/dbus/v5"
)

const secretsPrompt = "org.freedesktop.Secret.Prompt"

var (
	errPromptDismissed = errors.New("Secret Service prompt dismissed")
	errPromptTimeout   = errors.New("Secret Service prompt not answered in time")
	errPromptClosed    = errors.New("D-Bus connection closed while waiting for the Secret Service prompt")
)

func awaitPrompt(ctx context.Context, signals <-chan *dbus.Signal, prompt dbus.ObjectPath) (dbus.Variant, error) {
	for {
		select {
		case <-ctx.Done():
			return dbus.Variant{}, errPromptTimeout
		case signal, ok := <-signals:
			if !ok {
				return dbus.Variant{}, errPromptClosed
			}
			if signal.Path != prompt || signal.Name != secretsPrompt+".Completed" {
				continue
			}
			var dismissed bool
			var result dbus.Variant
			if err := dbus.Store(signal.Body, &dismissed, &result); err != nil {
				return dbus.Variant{}, err
			}
			if dismissed {
				return dbus.Variant{}, errPromptDismissed
			}
			return result, nil
		}
	}
}
