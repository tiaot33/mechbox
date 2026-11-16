package library

import "testing"

// 已经取了共享锁的调用里再要独占锁是代码写错了:直接 panic,不悄悄只拿着共享锁继续。
func TestExclusiveOperationUnderTheSharedLockPanics(t *testing.T) {
	l := &Library{}
	ctx, unlock := l.sharedOperation(t.Context())
	defer unlock()
	defer func() {
		if recover() == nil {
			t.Fatal("exclusiveOperation under the shared lock returned; want a panic")
		}
	}()
	l.exclusiveOperation(ctx)
}

// 同一次调用里已经取了锁再取时不重复加锁,最外层放开后锁就空出来。
func TestNestedOperationsReuseTheHeldLock(t *testing.T) {
	l := &Library{}
	ctx, unlock := l.exclusiveOperation(t.Context())
	_, inner := l.sharedOperation(ctx)
	inner()
	_, inner = l.exclusiveOperation(ctx)
	inner()
	unlock()
	ctx, unlock = l.sharedOperation(t.Context())
	_, inner = l.sharedOperation(ctx)
	inner()
	unlock()
	if !l.mu.TryLock() {
		t.Fatal("lock is still held after the outermost operations released it")
	}
}
