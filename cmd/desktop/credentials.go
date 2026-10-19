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
	GitHubToken     string `json:"github_token,omitempty"`
	SecretAccessKey string `json:"secret_access_key,omitempty"`
	SessionToken    string `json:"session_token,omitempty"`
}

// Credentials 是设置页 GitHub 与同步分区里填的内容。令牌、Secret Access Key、Session Token 存在系统钥匙串里
// (macOS 钥匙串、Windows 凭据管理器、Linux 的 Secret Service),
// 其余几项存在 settings.json;都只属于这台机器，不进技能库，也不随同步走(见 ADR 0011)。
type Credentials struct {
	GitHubToken string               `json:"github_token"` // 留空时技能库读 GITHUB_TOKEN / GH_TOKEN
	Sync        library.SyncSettings `json:"sync"`         // 全部留空时技能库读 SKILLS_S3_*、AWS_*;填了任意一项就整组用这里的
}

// Credentials 返回设置页里填的 GitHub 令牌与同步设置。启动时没能读取钥匙串(例如用户拒绝了授权)时，
// 这里再读一次并交给技能库;仍读不到就返回错误，设置页不让保存，免得用空值盖掉钥匙串里的内容。
func (a *AppService) Credentials() (Credentials, error) {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	if !a.secretsRead {
		if err := a.readSecrets(); err != nil {
			return Credentials{}, err
		}
		a.applyCredentials(a.credentials())
	}
	return a.credentials(), nil
}

// SaveGitHubToken 保存 GitHub 令牌并立即交给技能库;传空字符串即清掉，技能库回到读环境变量。
func (a *AppService) SaveGitHubToken(token string) (Credentials, error) {
	return a.saveCredentials(func(c *Credentials) { c.GitHubToken = token })
}

// SaveSyncSettings 保存同步设置并立即整组交给技能库;各项都留空即清掉，技能库回到读环境变量。
func (a *AppService) SaveSyncSettings(sync library.SyncSettings) (Credentials, error) {
	return a.saveCredentials(func(c *Credentials) { c.Sync = sync })
}

func (a *AppService) credentials() Credentials {
	a.mu.Lock()
	sync := a.sync
	a.mu.Unlock()
	return Credentials{
		GitHubToken: a.secrets.GitHubToken,
		Sync: library.SyncSettings{
			Endpoint:        sync.Endpoint,
			Bucket:          sync.Bucket,
			Key:             sync.Key,
			Region:          sync.Region,
			AccessKeyID:     sync.AccessKeyID,
			SecretAccessKey: a.secrets.SecretAccessKey,
			SessionToken:    a.secrets.SessionToken,
		},
	}
}

func (a *AppService) saveCredentials(change func(*Credentials)) (Credentials, error) {
	a.credMu.Lock()
	defer a.credMu.Unlock()
	a.mu.Lock()
	uninstalling := a.uninstalling
	a.mu.Unlock()
	if uninstalling {
		return Credentials{}, errors.New("uninstalling: credentials not saved")
	}
	if err := a.readSecrets(); err != nil {
		return Credentials{}, err
	}
	next := a.credentials()
	change(&next)
	next = trimCredentials(next)
	stored := secrets{GitHubToken: next.GitHubToken, SecretAccessKey: next.Sync.SecretAccessKey, SessionToken: next.Sync.SessionToken}
	sync := syncFields{Endpoint: next.Sync.Endpoint, Bucket: next.Sync.Bucket, Key: next.Sync.Key, Region: next.Sync.Region, AccessKeyID: next.Sync.AccessKeyID}

	previous := a.secrets
	if stored != previous {
		if err := writeSecrets(a.keychain, stored); err != nil {
			return Credentials{}, err
		}
	}
	a.mu.Lock()
	var err error
	if sync != a.sync {
		err = saveSettings(a.path, settingsFile{Settings: a.settings, Sync: sync, FirstOpenDone: !a.firstOpen})
	}
	if err == nil {
		a.sync = sync
	}
	a.mu.Unlock()
	if err != nil {
		if stored != previous {
			if restoreErr := writeSecrets(a.keychain, previous); restoreErr != nil {
				a.secrets = stored
				a.applyCredentials(a.credentials())
				return Credentials{}, errors.Join(err, fmt.Errorf("restore keychain item: %w", restoreErr))
			}
		}
		return Credentials{}, err
	}
	a.secrets = stored
	a.applyCredentials(next)
	return next, nil
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

func (a *AppService) applyCredentials(c Credentials) {
	a.library.SetGitHubToken(c.GitHubToken)
	if c.Sync == (library.SyncSettings{}) {
		a.library.SetSyncSettings(nil)
	} else {
		sync := c.Sync
		a.library.SetSyncSettings(&sync)
	}
}

func trimCredentials(c Credentials) Credentials {
	for _, field := range []*string{
		&c.GitHubToken, &c.Sync.Endpoint, &c.Sync.Bucket, &c.Sync.Key, &c.Sync.Region,
		&c.Sync.AccessKeyID, &c.Sync.SecretAccessKey, &c.Sync.SessionToken,
	} {
		*field = strings.TrimSpace(*field)
	}
	return c
}
