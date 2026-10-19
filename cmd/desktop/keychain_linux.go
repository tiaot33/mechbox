//go:build !server

package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	secretsName       = "org.freedesktop.secrets"
	secretsPath       = dbus.ObjectPath("/org/freedesktop/secrets")
	secretsService    = "org.freedesktop.Secret.Service"
	secretsCollection = "org.freedesktop.Secret.Collection"
	secretsItem       = "org.freedesktop.Secret.Item"
	secretsSession    = "org.freedesktop.Secret.Session"
	noObject          = dbus.ObjectPath("/")
	secretsTimeout    = 30 * time.Second
	promptTimeout     = 5 * time.Minute
)

var errNoSecretService = errors.New("no Secret Service on the D-Bus session bus (install and start GNOME Keyring or KWallet)")

type secretValue struct {
	Session     dbus.ObjectPath
	Parameters  []byte
	Value       []byte
	ContentType string
}

func (k keychain) get() ([]byte, bool, error) {
	var value []byte
	var found bool
	err := withSecretService(func(s *secretService) error {
		items, err := s.find(k)
		if err != nil || len(items) == 0 {
			return err
		}
		return s.withSession(func(session dbus.ObjectPath) error {
			var secret secretValue
			if err := s.call(items[0], secretsItem+".GetSecret", []any{session}, &secret); err != nil {
				return err
			}
			value, found = secret.Value, true
			return nil
		})
	})
	if err != nil {
		return nil, false, fmt.Errorf("read Secret Service item: %w", err)
	}
	return value, found, nil
}

func (k keychain) set(value []byte) error {
	err := withSecretService(func(s *secretService) error {
		items, err := s.find(k)
		if err != nil {
			return err
		}
		return s.withSession(func(session dbus.ObjectPath) error {
			secret := secretValue{Session: session, Parameters: []byte{}, Value: value, ContentType: "text/plain"}
			if len(items) > 0 {
				for _, item := range items {
					if err := s.call(item, secretsItem+".SetSecret", []any{secret}); err != nil {
						return err
					}
				}
				return nil
			}
			collection, err := s.defaultCollection()
			if err != nil {
				return err
			}
			properties := map[string]dbus.Variant{
				secretsItem + ".Label":      dbus.MakeVariant(k.service),
				secretsItem + ".Attributes": dbus.MakeVariant(k.attributes()),
			}
			var item, prompt dbus.ObjectPath
			if err := s.call(collection, secretsCollection+".CreateItem", []any{properties, secret, true}, &item, &prompt); err != nil {
				return err
			}
			_, err = s.prompt(prompt)
			return err
		})
	})
	if err != nil {
		return fmt.Errorf("write Secret Service item: %w", err)
	}
	return nil
}

func (k keychain) delete() error {
	err := withSecretService(func(s *secretService) error {
		items, err := s.find(k)
		if err != nil {
			return err
		}
		for _, item := range items {
			var prompt dbus.ObjectPath
			if err := s.call(item, secretsItem+".Delete", nil, &prompt); err != nil {
				return err
			}
			if _, err := s.prompt(prompt); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("delete Secret Service item: %w", err)
	}
	return nil
}

func (k keychain) attributes() map[string]string {
	return map[string]string{"service": k.service, "account": k.account}
}

type secretService struct {
	conn    *dbus.Conn
	signals chan *dbus.Signal
}

func withSecretService(run func(*secretService) error) error {
	conn, err := dbus.SessionBusPrivateNoAutoStartup()
	if err != nil {
		return fmt.Errorf("%w: %w", errNoSecretService, err)
	}
	defer conn.Close()
	if err := conn.Auth(nil); err != nil {
		return fmt.Errorf("%w: %w", errNoSecretService, err)
	}
	if err := conn.Hello(); err != nil {
		return fmt.Errorf("%w: %w", errNoSecretService, err)
	}
	s := &secretService{conn: conn, signals: make(chan *dbus.Signal, 16)}
	conn.Signal(s.signals)
	return run(s)
}

func (s *secretService) call(object dbus.ObjectPath, method string, args []any, out ...any) error {
	ctx, cancel := context.WithTimeout(context.Background(), secretsTimeout)
	defer cancel()
	err := s.conn.Object(secretsName, object).CallWithContext(ctx, method, 0, args...).Store(out...)
	var dbusErr dbus.Error
	if errors.As(err, &dbusErr) && (dbusErr.Name == "org.freedesktop.DBus.Error.ServiceUnknown" ||
		dbusErr.Name == "org.freedesktop.DBus.Error.NameHasNoOwner" ||
		strings.HasPrefix(dbusErr.Name, "org.freedesktop.DBus.Error.Spawn.")) {
		return fmt.Errorf("%w: %w", errNoSecretService, err)
	}
	return err
}

func (s *secretService) find(k keychain) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	if err := s.call(secretsPath, secretsService+".SearchItems", []any{k.attributes()}, &unlocked, &locked); err != nil {
		return nil, err
	}
	if len(locked) > 0 {
		if err := s.unlock(locked); err != nil {
			return nil, err
		}
	}
	return append(unlocked, locked...), nil
}

func (s *secretService) unlock(objects []dbus.ObjectPath) error {
	var unlocked []dbus.ObjectPath
	var prompt dbus.ObjectPath
	if err := s.call(secretsPath, secretsService+".Unlock", []any{objects}, &unlocked, &prompt); err != nil {
		return err
	}
	_, err := s.prompt(prompt)
	return err
}

func (s *secretService) defaultCollection() (dbus.ObjectPath, error) {
	var collection dbus.ObjectPath
	if err := s.call(secretsPath, secretsService+".ReadAlias", []any{"default"}, &collection); err != nil {
		return "", err
	}
	if collection == noObject {
		properties := map[string]dbus.Variant{secretsCollection + ".Label": dbus.MakeVariant("Login")}
		var prompt dbus.ObjectPath
		if err := s.call(secretsPath, secretsService+".CreateCollection", []any{properties, "default"}, &collection, &prompt); err != nil {
			return "", err
		}
		if collection == noObject {
			result, err := s.prompt(prompt)
			if err != nil {
				return "", err
			}
			path, ok := result.Value().(dbus.ObjectPath)
			if !ok || path == noObject {
				return "", errors.New("Secret Service created no default collection")
			}
			collection = path
		}
	}
	return collection, s.unlock([]dbus.ObjectPath{collection})
}

func (s *secretService) withSession(run func(session dbus.ObjectPath) error) error {
	var output dbus.Variant
	var session dbus.ObjectPath
	if err := s.call(secretsPath, secretsService+".OpenSession", []any{"plain", dbus.MakeVariant("")}, &output, &session); err != nil {
		return err
	}
	defer s.call(session, secretsSession+".Close", nil)
	return run(session)
}

func (s *secretService) prompt(prompt dbus.ObjectPath) (dbus.Variant, error) {
	if prompt == noObject {
		return dbus.Variant{}, nil
	}
	match := []dbus.MatchOption{
		dbus.WithMatchObjectPath(prompt),
		dbus.WithMatchInterface(secretsPrompt),
		dbus.WithMatchMember("Completed"),
	}
	if err := s.conn.AddMatchSignal(match...); err != nil {
		return dbus.Variant{}, err
	}
	defer s.conn.RemoveMatchSignal(match...)
	if err := s.call(prompt, secretsPrompt+".Prompt", []any{""}); err != nil {
		return dbus.Variant{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), promptTimeout)
	defer cancel()
	result, err := awaitPrompt(ctx, s.signals, prompt)
	if errors.Is(err, errPromptTimeout) {
		_ = s.call(prompt, secretsPrompt+".Dismiss", nil)
	}
	return result, err
}
