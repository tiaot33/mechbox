package main

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"

	"github.com/tiaot33/mechbox/internal/library"
)

// settingsStore 是应用设置的读写：设置页里的一组设置(存在技能库的应用设置里)、这台机器上已经做过的一次性的事
// (第一次打开、按检测结果提示添加 Agent),以及各 Agent 全局目录所在的 WSL 发行版和它启动时没能打开的原因。
// 设置换了(保存或拉回)以后，依次交给 onChange 登记的各方，让它们立即生效。
type settingsStore struct {
	lib    *library.Library
	events emitter
	log    *slog.Logger

	saveMu sync.Mutex

	mu          sync.Mutex
	settings    Settings
	firstOpen   bool
	offerPool   bool
	distroError string
	changed     []func(context.Context, Settings)
}

func newSettingsStore(lib *library.Library, events emitter, log *slog.Logger) *settingsStore {
	return &settingsStore{lib: lib, events: events, log: log, settings: defaultSettings()}
}

// onChange 登记设置换了以后要做的事;保存、拉回之后按登记的先后调用。
func (s *settingsStore) onChange(f func(context.Context, Settings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changed = append(s.changed, f)
}

// load 在启动时从技能库读出设置，换到存着的 WSL 发行版;选了发行版而打不开时，技能库留在 Windows 本机的全局目录，
// 原因由 wslDistroError 给出。设置读不出时用默认值，并且不当作第一次打开。
func (s *settingsStore) load(ctx context.Context) {
	values, err := s.lib.AppSettings(ctx)
	warn(s.log, err, "settings unreadable, using defaults")
	settings, marks := settingsFrom(values)
	distro, distroErr := s.lib.WSLDistro(ctx)
	warn(s.log, distroErr, "WSL distro setting unreadable")
	settings.WSLDistro = distro
	distroError := ""
	if distro != "" {
		if err := s.lib.SetWSLDistro(ctx, distro); err != nil {
			distroError = library.ErrorOf(err).Message
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.settings, s.firstOpen = settings, err == nil && !marks.firstOpen
	s.offerPool = err == nil && marks.firstOpen && !marks.agentPool
	s.distroError = distroError
}

func (s *settingsStore) current() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.settings
}

// save 校验并保存一整组设置，存好后立即生效。改了 WSL 发行版时先交给技能库，打不开就不保存、返回错误;
// 发行版换过去了而设置没写成时换回原来的。
func (s *settingsStore) save(ctx context.Context, settings Settings) (Settings, error) {
	if !settings.CheckInterval.valid() {
		return Settings{}, fmt.Errorf("unknown check interval %q", settings.CheckInterval)
	}
	if !settings.Language.valid() {
		return Settings{}, fmt.Errorf("unknown language %q", settings.Language)
	}
	if !settings.Appearance.valid() {
		return Settings{}, fmt.Errorf("unknown appearance %q", settings.Appearance)
	}
	if !foldBelowValid(settings.FoldBelow) {
		return Settings{}, fmt.Errorf("fold threshold %d is outside %d to %d", settings.FoldBelow, minFoldBelow, maxFoldBelow)
	}
	previous := s.current().WSLDistro
	switched := settings.WSLDistro != previous
	if switched {
		if err := s.lib.SetWSLDistro(ctx, settings.WSLDistro); err != nil {
			return Settings{}, err
		}
	}
	if err := s.write(ctx, settings, nil); err != nil {
		if switched {
			_ = s.lib.SetWSLDistro(context.WithoutCancel(ctx), previous)
		}
		return Settings{}, err
	}
	if switched {
		s.mu.Lock()
		s.distroError = ""
		s.mu.Unlock()
	}
	s.notify(ctx, settings)
	return settings, nil
}

// marks 是按这台机器上现在的状态,已经做过了的一次性的事。调用方持有 s.mu。
func (s *settingsStore) marks() openedMarks {
	return openedMarks{firstOpen: !s.firstOpen, agentPool: !s.firstOpen && !s.offerPool}
}

// write 把设置写进技能库的应用设置，写成了才换掉 s.settings;marks 为 nil 时按现在的记录。
// 写入依次进行，免得较早的一次后写、盖掉较新的设置。
func (s *settingsStore) write(ctx context.Context, settings Settings, marks *openedMarks) error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	s.mu.Lock()
	done := s.marks()
	s.mu.Unlock()
	if marks != nil {
		done = *marks
	}
	if err := s.lib.SetAppSettings(ctx, settings.appValues(done)); err != nil {
		return err
	}
	s.mu.Lock()
	s.settings = settings
	s.mu.Unlock()
	return nil
}

// reload 在拉回之后重新读出设置并立即生效：拉回的库带着推送那台机器的设置。WSL 发行版只属于这台机器，不变;
// 这台机器已经打开过 app 时，把这条记录写回去，免得拉回一份没有它的库后下次启动又当作第一次打开。
// 换完发 settings-reloaded 事件，开着的设置页据此重读，免得之后改一项时拿旧值整组盖掉拉回来的设置。
func (s *settingsStore) reload(ctx context.Context) {
	values, err := s.lib.AppSettings(ctx)
	if err != nil {
		warn(s.log, err, "settings unreadable after pull")
		return
	}
	settings, pulled := settingsFrom(values)
	s.mu.Lock()
	settings.WSLDistro = s.settings.WSLDistro
	here := s.marks()
	keep := openedMarks{firstOpen: pulled.firstOpen || here.firstOpen, agentPool: pulled.agentPool || here.agentPool}
	s.settings = settings
	s.mu.Unlock()
	if keep != pulled {
		warn(s.log, s.write(ctx, settings, &keep), "settings unwritable, first open not kept after pull")
	}
	s.notify(ctx, settings)
	go s.events.Emit(settingsReloadedEvent)
}

func (s *settingsStore) notify(ctx context.Context, settings Settings) {
	s.mu.Lock()
	changed := slices.Clone(s.changed)
	s.mu.Unlock()
	for _, f := range changed {
		f(context.WithoutCancel(ctx), settings)
	}
}

// takeFirstOpen 见 AppService.FirstOpen。
func (s *settingsStore) takeFirstOpen() bool {
	s.mu.Lock()
	first := s.firstOpen
	s.firstOpen = false
	settings := s.settings
	s.mu.Unlock()
	if !first {
		return false
	}
	warn(s.log, s.write(context.Background(), settings, nil), "settings unwritable, first open not recorded")
	return true
}

// agentPoolPending 见 AppService.AgentPoolPending。
func (s *settingsStore) agentPoolPending() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.offerPool && s.distroError == ""
}

// agentPoolOffered 见 AppService.AgentPoolOffered。
func (s *settingsStore) agentPoolOffered() {
	s.mu.Lock()
	offer := s.offerPool
	s.offerPool = false
	settings := s.settings
	s.mu.Unlock()
	if offer {
		warn(s.log, s.write(context.Background(), settings, nil), "settings unwritable, agent pool offer not recorded")
	}
}

func (s *settingsStore) wslDistroError() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.distroError
}

// retryWSLDistro 见 AppService.RetryWSLDistro。
func (s *settingsStore) retryWSLDistro(ctx context.Context) error {
	err := s.lib.SetWSLDistro(ctx, s.current().WSLDistro)
	message := ""
	if err != nil {
		message = library.ErrorOf(err).Message
	}
	s.mu.Lock()
	s.distroError = message
	s.mu.Unlock()
	return err
}
