package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tiaot33/mechbox/internal/library"
)

type secretStore interface {
	get() ([]byte, bool, error)
	set(value []byte) error
	delete() error
}

type keychain struct {
	service string
	account string
}

func defaultKeychain() keychain {
	return keychain{service: bundleID, account: "credentials"}
}

type secrets struct {
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

// SyncSettings 返回设置页同步分区里填的 S3 设置。Secret Access Key、Session Token 存在系统钥匙串里
// (macOS 钥匙串、Windows 凭据管理器、Linux 的 Secret Service),其余几项存在 settings.json;
// 都只属于这台机器，不进技能库，也不随同步走(见 ADR 0011)。全部留空时技能库读 SKILLS_S3_*、AWS_*;填了任意一项就整组用这里的。
// 启动时没能读取钥匙串(例如用户拒绝了授权)时，这里再读一次并交给技能库;仍读不到就返回错误，同步分区不让保存，
// 免得用空值盖掉钥匙串里的内容。GitHub 令牌存在技能库里，不受影响。
func (a *AppService) SyncSettings() (library.SyncSettings, error) {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	if !a.secretsRead {
		if err := a.readSecrets(); err != nil {
			return library.SyncSettings{}, err
		}
		a.applySyncSettings(a.syncSettings())
	}
	return a.syncSettings(), nil
}

// SaveSyncSettings 保存同步设置并立即整组交给技能库;各项都留空即清掉，技能库回到读环境变量。
func (a *AppService) SaveSyncSettings(sync library.SyncSettings) (library.SyncSettings, error) {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	a.mu.Lock()
	uninstalling := a.uninstalling
	a.mu.Unlock()
	if uninstalling {
		return library.SyncSettings{}, errors.New("uninstalling: sync settings not saved")
	}
	if err := a.readSecrets(); err != nil {
		return library.SyncSettings{}, err
	}
	next := trimSyncSettings(sync)
	stored := secrets{SecretAccessKey: next.SecretAccessKey, SessionToken: next.SessionToken}
	fields := syncFields{Endpoint: next.Endpoint, Bucket: next.Bucket, Key: next.Key, Region: next.Region, AccessKeyID: next.AccessKeyID}

	previous := a.secrets
	if stored != previous {
		if err := writeSecrets(a.keychain, stored); err != nil {
			return library.SyncSettings{}, err
		}
	}
	a.mu.Lock()
	var err error
	if fields != a.sync {
		err = saveSettings(a.path, settingsFile{Settings: a.settings, Sync: fields, FirstOpenDone: !a.firstOpen})
	}
	if err == nil {
		a.sync = fields
	}
	a.mu.Unlock()
	if err != nil {
		if stored != previous {
			if restoreErr := writeSecrets(a.keychain, previous); restoreErr != nil {
				a.secrets = stored
				a.applySyncSettings(a.syncSettings())
				return library.SyncSettings{}, errors.Join(err, fmt.Errorf("restore keychain item: %w", restoreErr))
			}
		}
		return library.SyncSettings{}, err
	}
	a.secrets = stored
	a.applySyncSettings(next)
	return next, nil
}

func (a *AppService) syncSettings() library.SyncSettings {
	a.mu.Lock()
	sync := a.sync
	a.mu.Unlock()
	return library.SyncSettings{
		Endpoint:        sync.Endpoint,
		Bucket:          sync.Bucket,
		Key:             sync.Key,
		Region:          sync.Region,
		AccessKeyID:     sync.AccessKeyID,
		SecretAccessKey: a.secrets.SecretAccessKey,
		SessionToken:    a.secrets.SessionToken,
	}
}

func (a *AppService) readSecrets() error {
	if a.secretsRead {
		return nil
	}
	data, ok, err := a.keychain.get()
	if err != nil {
		return err
	}
	var stored secrets
	if ok {
		if err := json.Unmarshal(data, &stored); err != nil {
			return fmt.Errorf("parse keychain item: %w", err)
		}
	}
	a.secrets, a.secretsRead = stored, true
	return nil
}

func writeSecrets(k secretStore, stored secrets) error {
	if stored == (secrets{}) {
		return k.delete()
	}
	data, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	return k.set(data)
}

func (a *AppService) applySyncSettings(sync library.SyncSettings) {
	if sync == (library.SyncSettings{}) {
		a.library.SetSyncSettings(nil)
	} else {
		a.library.SetSyncSettings(&sync)
	}
}

func trimSyncSettings(s library.SyncSettings) library.SyncSettings {
	for _, field := range []*string{
		&s.Endpoint, &s.Bucket, &s.Key, &s.Region, &s.AccessKeyID, &s.SecretAccessKey, &s.SessionToken,
	} {
		*field = strings.TrimSpace(*field)
	}
	return s
}
