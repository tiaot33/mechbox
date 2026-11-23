package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// autoPushRetry 是自动推送失败后，最早多久再试一次。
const autoPushRetry = time.Hour

// PushProblem 是上一次推送(自动或手动)出的问题:推送失败,或备份已经传上去、删超出份数的旧备份失败(Uploaded)。
type PushProblem struct {
	At       time.Time      `json:"at"`
	Error    *library.Error `json:"error"`
	Uploaded bool           `json:"uploaded,omitempty"`
}

// autoPush 是自动推送：按同步设置里的间隔(以技能库记下的上次推送时间为准)每分钟看一次是否到了，到了就把库推送成 S3 上这台机器的一份新备份，
// 从不自动拉回;失败后一小时内不重试，改了同步设置就不等。它也记着上一次推送(自动或手动)出的问题。
type autoPush struct {
	lib    *library.Library
	events emitter
	log    *slog.Logger
	wake   chan struct{}

	mu          sync.Mutex
	lastAttempt time.Time
	problem     *PushProblem
}

func newAutoPush(lib *library.Library, events emitter, log *slog.Logger) *autoPush {
	return &autoPush{lib: lib, events: events, log: log, wake: make(chan struct{}, 1)}
}

// tick 看一次是否到了，到了就推送;由 AppService 的后台循环在自动检查之后调用。
func (p *autoPush) tick(ctx context.Context) {
	if p.due(ctx, time.Now()) {
		p.push(ctx)
	}
}

// syncSettingsChanged 在同步设置存好以后清掉上次的问题，按新设置马上重新判断要不要推送，不再等上次失败后的重试间隔。
func (p *autoPush) syncSettingsChanged() {
	p.mu.Lock()
	p.lastAttempt, p.problem = time.Time{}, nil
	p.mu.Unlock()
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *autoPush) due(ctx context.Context, now time.Time) bool {
	settings, err := p.lib.SyncSettings(ctx)
	if err != nil {
		return false
	}
	period, timed := settings.AutoPush.Period()
	if !timed || !syncConfigured(settings) {
		return false
	}
	status, err := p.lib.SyncStatus(ctx)
	if err != nil || now.Sub(status.LastPushedAt) < period {
		return false
	}
	p.mu.Lock()
	last := p.lastAttempt
	p.mu.Unlock()
	return now.Sub(last) >= min(period, autoPushRetry)
}

func (p *autoPush) push(ctx context.Context) {
	p.mu.Lock()
	p.lastAttempt = time.Now().Round(0)
	p.mu.Unlock()
	result, err := p.lib.SyncPush(ctx)
	if ctx.Err() != nil {
		return
	}
	p.record(result, err)
	warn(p.log, err, "automatic sync push")
	go p.events.Emit(syncPushedEvent)
}

// record 记下一次推送(自动或手动)的结果：手动推送和自动推送共用一份提示。
func (p *autoPush) record(result library.SyncPushResult, err error) {
	var problem *PushProblem
	switch {
	case err != nil:
		problem = &PushProblem{At: time.Now().Round(0), Error: library.ErrorOf(err)}
	case result.RemoveError != nil:
		problem = &PushProblem{At: time.Now().Round(0), Error: result.RemoveError, Uploaded: true}
	}
	p.mu.Lock()
	p.problem = problem
	p.mu.Unlock()
}

func (p *autoPush) lastProblem() *PushProblem {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.problem
}
