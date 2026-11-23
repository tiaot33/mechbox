package main

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tiaot33/mechbox/cmd/desktop/internal/system"
	"github.com/tiaot33/mechbox/internal/library"
)

// autoCheck 是自动检查更新：按设置里的间隔(以技能库里记下的检查时间为准，从未检查过的技能从入库时算起)每分钟看一次是否到了，
// 「只在打开时」是启动应用、从菜单栏或 Dock 重新打开窗口时各检查一次。只检查非本地目录来源的集合，只读，不发系统通知;
// 检查完更新 Dock 角标，并发 updates-checked 事件让前端重读。
type autoCheck struct {
	lib      *library.Library
	settings *settingsStore
	reads    *sourceReads
	badge    *dockBadge
	events   emitter
	log      *slog.Logger
	wake     chan struct{}

	// mu 也包住读设置：同一份设置下消费「刚打开」的标记。
	mu          sync.Mutex
	opened      bool
	lastAttempt time.Time
}

func newAutoCheck(lib *library.Library, settings *settingsStore, reads *sourceReads, badge *dockBadge, events emitter, log *slog.Logger) *autoCheck {
	return &autoCheck{lib: lib, settings: settings, reads: reads, badge: badge, events: events, log: log, wake: make(chan struct{}, 1), opened: true}
}

// tick 看一次是否到了，到了就检查;由 AppService 的后台循环每分钟(或被唤醒时)调用。
func (c *autoCheck) tick(ctx context.Context) {
	if collections := c.due(ctx, time.Now()); len(collections) > 0 {
		c.check(ctx, collections)
	}
}

// poke 让后台循环不等下一分钟、马上再看一次(设置变了时)。
func (c *autoCheck) poke() {
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// reopened 记下窗口从菜单栏或 Dock 重新打开了一次。
func (c *autoCheck) reopened() {
	c.mu.Lock()
	c.opened = true
	c.mu.Unlock()
	c.poke()
}

func (c *autoCheck) due(ctx context.Context, now time.Time) []library.CollectionID {
	c.mu.Lock()
	settings := c.settings.current()
	opened, last := c.opened, c.lastAttempt
	c.opened = false
	c.mu.Unlock()
	if !settings.AutoCheck {
		return nil
	}
	collections, err := c.lib.ListCollections(ctx)
	if err != nil {
		return nil
	}
	var ids []library.CollectionID
	for _, collection := range collections {
		if collection.Source.Type != library.SourceLocal {
			ids = append(ids, collection.ID)
		}
	}
	period, timed := settings.CheckInterval.period()
	if !timed || len(ids) == 0 {
		if opened {
			return ids
		}
		return nil
	}
	skills, err := c.lib.ListSkills(ctx)
	if err != nil {
		return nil
	}
	since := oldestConfirmed(skills, now)
	if last.After(since) {
		since = last
	}
	if now.Sub(since) < period {
		return nil
	}
	return ids
}

func oldestConfirmed(skills []library.Skill, now time.Time) time.Time {
	oldest := now
	for _, skill := range skills {
		if skill.Source.Type == library.SourceLocal {
			continue
		}
		seen := skill.FetchedAt
		if skill.LastUpdateCheck != nil {
			seen = skill.LastUpdateCheck.CheckedAt
		}
		if seen.Before(oldest) {
			oldest = seen
		}
	}
	return oldest
}

func (c *autoCheck) check(ctx context.Context, collections []library.CollectionID) {
	c.mu.Lock()
	c.lastAttempt = time.Now().Round(0)
	c.mu.Unlock()
	var err error
	c.reads.take(ctx, func() { _, err = c.lib.CheckUpdates(ctx, collections) })
	if ctx.Err() != nil {
		return
	}
	warn(c.log, err, "automatic update check")
	c.badge.refresh(ctx)
	go c.events.Emit(updatesCheckedEvent)
}

// sourceReads 让读来源的几件事(自动检查更新、给未纳管技能找来源)轮流进行、不同时跑，各自做完把空闲内存还给系统。
type sourceReads struct {
	turn chan struct{}
}

func newSourceReads() *sourceReads {
	return &sourceReads{turn: make(chan struct{}, 1)}
}

// take 等到轮到时做 work;等的时候 ctx 取消了就不做。
func (r *sourceReads) take(ctx context.Context, work func()) {
	select {
	case r.turn <- struct{}{}:
	case <-ctx.Done():
		return
	}
	defer func() {
		debug.FreeOSMemory()
		<-r.turn
	}()
	work()
}

// dockBadge 是 Dock 角标：设置里打开了时显示计入角标的技能数(技能库的 UpdateCheck.Pending),关掉时不显示。
type dockBadge struct {
	lib      *library.Library
	settings *settingsStore

	mu sync.Mutex
}

func (b *dockBadge) refresh(ctx context.Context) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	if b.settings.current().DockBadge {
		skills, err := b.lib.ListSkills(ctx)
		if err != nil {
			return
		}
		n = badgeCount(skills)
	}
	system.SetBadge(n)
}

func badgeCount(skills []library.Skill) int {
	n := 0
	for _, skill := range skills {
		if skill.LastUpdateCheck != nil && skill.LastUpdateCheck.Pending {
			n++
		}
	}
	return n
}
