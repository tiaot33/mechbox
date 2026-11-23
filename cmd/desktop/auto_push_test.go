package main

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tiaot33/mechbox/internal/library"
)

// 自动推送按同步设置里的间隔:没开、没配好时不推;从没推送过时推;推送成功后满一个间隔才再推;
// 失败后记下原因，一小时内不重试，改了同步设置就不等了。
func TestAutoPushDue(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	lib := s.lib
	deny := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case deny:
			http.Error(w, "denied", http.StatusForbidden)
		case r.Method == http.MethodGet:
			_, _ = w.Write([]byte("<ListBucketResult></ListBucketResult>"))
		case r.Method == http.MethodPut:
			w.Header().Set("ETag", `"pushed"`)
		}
	}))
	t.Cleanup(server.Close)
	a, p := s.app, s.app.pushes
	ctx := t.Context()
	settings := library.SyncSettings{Endpoint: server.URL, Bucket: "bucket", AccessKeyID: "key", SecretAccessKey: "secret"}
	save := func(s library.SyncSettings) {
		t.Helper()
		if err := lib.SetSyncSettings(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()

	save(settings)
	if p.due(ctx, now) {
		t.Error("自动推送没开时 due")
	}
	daily := settings
	daily.AutoPush = library.SyncDaily
	save(library.SyncSettings{Bucket: "bucket", AutoPush: library.SyncDaily})
	if p.due(ctx, now) {
		t.Error("没配好密钥时 due")
	}
	save(daily)
	if !p.due(ctx, now) {
		t.Fatal("从没推送过时不 due")
	}

	deny = true
	p.push(ctx)
	s.events.waitFor(t, syncPushedEvent)
	if problem := a.PushProblem(); problem == nil || problem.Error.Code != library.Permission || problem.Uploaded {
		t.Fatalf("PushProblem() after denied push = %+v; want Permission", problem)
	}
	if p.due(ctx, time.Now().Add(30*time.Minute)) {
		t.Error("失败后半小时内 due")
	}
	if !p.due(ctx, time.Now().Add(61*time.Minute)) {
		t.Error("失败后满一小时不 due")
	}
	save(daily)
	if !p.due(ctx, time.Now()) || a.PushProblem() != nil {
		t.Error("改了同步设置后仍要等重试间隔，或仍报着上次的失败")
	}

	deny = false
	p.push(ctx)
	if problem := a.PushProblem(); problem != nil {
		t.Fatalf("PushProblem() after push = %+v; want nil", problem)
	}
	if p.due(ctx, time.Now().Add(23*time.Hour)) {
		t.Error("推送成功后不到一天 due")
	}
	if !p.due(ctx, time.Now().Add(25*time.Hour)) {
		t.Error("推送成功后满一天不 due")
	}
}

// 手动推送和自动推送共用一份提示:备份传上去了、删旧备份被拒时记下(Uploaded),之后完全成功的手动推送清掉它。
func TestManualPushRecordsAndClearsPushProblem(t *testing.T) {
	s := newTestServices(t, tempLibrary(t))
	lib := s.lib
	var mu sync.Mutex
	var keys []string
	denyDelete := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		key := strings.TrimPrefix(r.URL.Path, "/bucket/")
		switch r.Method {
		case http.MethodGet:
			var list strings.Builder
			for _, k := range keys {
				list.WriteString("<Contents><Key>" + k + "</Key><ETag>\"e\"</ETag></Contents>")
			}
			_, _ = w.Write([]byte("<ListBucketResult>" + list.String() + "</ListBucketResult>"))
		case http.MethodPut:
			keys = append(keys, key)
			w.Header().Set("ETag", `"e"`)
		case http.MethodHead:
			w.Header().Set("ETag", `"e"`)
		case http.MethodDelete:
			if denyDelete {
				http.Error(w, "denied", http.StatusForbidden)
				return
			}
			keys = slices.DeleteFunc(keys, func(k string) bool { return k == key })
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(server.Close)
	a := s.app
	ctx := t.Context()
	if err := lib.SetSyncSettings(ctx, library.SyncSettings{Endpoint: server.URL, Bucket: "bucket", AccessKeyID: "key", SecretAccessKey: "secret", Keep: 1}); err != nil {
		t.Fatal(err)
	}
	push := func() library.SyncPushResult {
		t.Helper()
		time.Sleep(2 * time.Millisecond)
		result, err := lib.SyncPush(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	push()
	mu.Lock()
	denyDelete = true
	mu.Unlock()
	if result := push(); result.RemoveError == nil || result.RemoveError.Code != library.Permission {
		t.Fatalf("SyncPush() with removal denied = %+v; want RemoveError Permission", result)
	}
	if problem := a.PushProblem(); problem == nil || !problem.Uploaded || problem.Error.Code != library.Permission {
		t.Fatalf("PushProblem() = %+v; want an uploaded backup whose old ones could not be removed", problem)
	}
	mu.Lock()
	denyDelete = false
	mu.Unlock()
	push()
	if problem := a.PushProblem(); problem != nil {
		t.Fatalf("PushProblem() after a clean push = %+v; want nil", problem)
	}
}
