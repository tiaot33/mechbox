package main

import (
	"bytes"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// fakeCredentials 模拟 Windows 凭据管理器：每条凭据的内容不超过 credentialBlobLimit,超过时像系统一样拒绝写入。
type fakeCredentials struct {
	items     map[string][]byte
	failRead  bool
	failWrite func(target string) bool
}

var (
	errReadFailed  = errors.New("read failed")
	errWriteFailed = errors.New("write failed")
)

func (f *fakeCredentials) read(target string) ([]byte, bool, error) {
	if f.failRead {
		return nil, false, errReadFailed
	}
	blob, ok := f.items[target]
	return bytes.Clone(blob), ok, nil
}

func (f *fakeCredentials) write(target, _ string, blob []byte) error {
	if len(blob) > credentialBlobLimit {
		return errors.New("blob too large")
	}
	if f.failWrite != nil && f.failWrite(target) {
		return errWriteFailed
	}
	f.items[target] = bytes.Clone(blob)
	return nil
}

func (f *fakeCredentials) remove(target string) error {
	delete(f.items, target)
	return nil
}

func (f *fakeCredentials) list(prefix string) ([]string, error) {
	var targets []string
	for target := range f.items {
		if strings.HasPrefix(target, prefix) {
			targets = append(targets, target)
		}
	}
	return targets, nil
}

func newParted() (partedCredential, *fakeCredentials) {
	store := &fakeCredentials{items: map[string][]byte{}}
	return partedCredential{store: store, target: bundleID + ":credentials", user: "credentials"}, store
}

func mustGet(t *testing.T, p partedCredential) []byte {
	t.Helper()
	value, ok, err := p.get()
	if err != nil || !ok {
		t.Fatalf("get = %v, %v", ok, err)
	}
	return value
}

func TestPartedCredentialStoresSmallValueInOneItem(t *testing.T) {
	p, store := newParted()
	value := bytes.Repeat([]byte("a"), credentialBlobLimit)
	if err := p.set(value); err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(maps.Keys(store.items)); len(got) != 1 || got[0] != p.target {
		t.Fatalf("items = %v; want only %s", got, p.target)
	}
	if got := mustGet(t, p); !bytes.Equal(got, value) {
		t.Fatalf("get = %d bytes; want %d", len(got), len(value))
	}
}

func TestPartedCredentialSplitsLargeValue(t *testing.T) {
	p, store := newParted()
	value := bytes.Repeat([]byte("0123456789"), credentialBlobLimit/2)
	if err := p.set(value); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1+5 {
		t.Fatalf("items = %d; want the header and 5 parts", len(store.items))
	}
	if got := mustGet(t, p); !bytes.Equal(got, value) {
		t.Fatalf("get = %d bytes; want %d", len(got), len(value))
	}

	// 换成另一份较大的内容：旧的几段删掉，只留新的。
	larger := bytes.Repeat([]byte("abcdefghij"), credentialBlobLimit)
	if err := p.set(larger); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1+10 {
		t.Fatalf("items after growing = %d; want the header and 10 parts", len(store.items))
	}
	if got := mustGet(t, p); !bytes.Equal(got, larger) {
		t.Fatalf("get after growing = %d bytes; want %d", len(got), len(larger))
	}

	// 变小后回到一条，分段全部删掉。
	if err := p.set([]byte(`{"github_token":"t"}`)); err != nil {
		t.Fatal(err)
	}
	if len(store.items) != 1 {
		t.Fatalf("items after shrinking = %v; want one", slices.Collect(maps.Keys(store.items)))
	}
	if got := mustGet(t, p); string(got) != `{"github_token":"t"}` {
		t.Fatalf("get after shrinking = %s", got)
	}
}

func TestPartedCredentialKeepsOldValueWhenWriteFails(t *testing.T) {
	for _, c := range []struct {
		name string
		fail func(p partedCredential) func(string) bool
	}{
		{"part", func(p partedCredential) func(string) bool {
			return func(target string) bool { return target == p.partTarget(2, 1) }
		}},
		{"header", func(p partedCredential) func(string) bool {
			return func(target string) bool { return target == p.target }
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, store := newParted()
			old := bytes.Repeat([]byte("o"), credentialBlobLimit*2)
			if err := p.set(old); err != nil {
				t.Fatal(err)
			}
			before := len(store.items)
			store.failWrite = c.fail(p)
			if err := p.set(bytes.Repeat([]byte("n"), credentialBlobLimit*3)); !errors.Is(err, errWriteFailed) {
				t.Fatalf("set = %v; want the write error", err)
			}
			if got := mustGet(t, p); !bytes.Equal(got, old) {
				t.Fatalf("get after a failed write = %d bytes of %q; want the old value", len(got), got[:1])
			}
			if len(store.items) != before {
				t.Fatalf("items after a failed write = %d; want %d (new parts cleaned up)", len(store.items), before)
			}
		})
	}
}

func TestPartedCredentialDeleteRemovesEveryPart(t *testing.T) {
	p, store := newParted()
	if err := p.set(bytes.Repeat([]byte("x"), credentialBlobLimit*3)); err != nil {
		t.Fatal(err)
	}
	// 上一次写到一半留下的分段也一起删掉。
	store.items[p.partTarget(9, 0)] = []byte("orphan")
	other := bundleID + ":credentials-other"
	store.items[other] = []byte("kept")
	if err := p.delete(); err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(maps.Keys(store.items)); len(got) != 1 || got[0] != other {
		t.Fatalf("items after delete = %v; want only %s", got, other)
	}
	if _, ok, err := p.get(); ok || err != nil {
		t.Fatalf("get after delete = %v, %v; want not found", ok, err)
	}
	if err := p.delete(); err != nil {
		t.Fatalf("delete when nothing is stored = %v", err)
	}
}

// 缺了一段或头坏了(例如在凭据管理器里手动删过)按没存过处理：设置页给出空表单，重填后覆盖掉，残留的分段一起清掉。
func TestPartedCredentialTreatsBrokenItemAsNotStored(t *testing.T) {
	p, store := newParted()
	if err := p.set(bytes.Repeat([]byte("x"), credentialBlobLimit*2)); err != nil {
		t.Fatal(err)
	}
	delete(store.items, p.partTarget(1, 1))
	if _, ok, err := p.get(); ok || err != nil {
		t.Fatalf("get with a missing part = %v, %v; want not stored", ok, err)
	}
	if err := p.set([]byte("fresh")); err != nil {
		t.Fatal(err)
	}
	if got := slices.Collect(maps.Keys(store.items)); len(got) != 1 || got[0] != p.target {
		t.Fatalf("items after re-entering = %v; want only %s", got, p.target)
	}
	if got := mustGet(t, p); string(got) != "fresh" {
		t.Fatalf("get after re-entering = %q", got)
	}

	for _, header := range []string{"mechbox-parts:", "mechbox-parts:1", "mechbox-parts:0:1", "mechbox-parts:1:0", "mechbox-parts:1:2:3", "mechbox-parts:a:1", "mechbox-parts:-1:1"} {
		store.items[p.target] = []byte(header)
		if _, ok, err := p.get(); ok || err != nil {
			t.Fatalf("get with header %q = %v, %v; want not stored", header, ok, err)
		}
	}
}

func TestPartedCredentialSetCleansStaleParts(t *testing.T) {
	p, store := newParted()
	store.items[p.partTarget(7, 0)] = []byte("orphan")
	store.items[p.partTarget(1, 5)] = []byte("orphan")
	if err := p.set(bytes.Repeat([]byte("x"), credentialBlobLimit+1)); err != nil {
		t.Fatal(err)
	}
	want := []string{p.target, p.partTarget(1, 0), p.partTarget(1, 1)}
	if got := slices.Sorted(maps.Keys(store.items)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
		t.Fatalf("items = %v; want %v", got, want)
	}
}

// 内容本身以分段头开头时也分段保存，读回来不会被当成分段头。
func TestPartedCredentialValueLookingLikeHeader(t *testing.T) {
	p, _ := newParted()
	value := append(bytes.Clone(partsHeader), "1:1"...)
	if err := p.set(value); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, p); !bytes.Equal(got, value) {
		t.Fatalf("get = %q; want %q", got, value)
	}
}

func TestPartedCredentialEmptyValue(t *testing.T) {
	p, _ := newParted()
	if err := p.set(nil); err != nil {
		t.Fatal(err)
	}
	if got := mustGet(t, p); len(got) != 0 {
		t.Fatalf("get = %q; want empty", got)
	}
}

// 读不出来(系统报错)时如实返回错误，设置页按读不到处理，不覆盖。
func TestPartedCredentialReportsReadError(t *testing.T) {
	p, store := newParted()
	if err := p.set([]byte("old")); err != nil {
		t.Fatal(err)
	}
	store.failRead = true
	if _, _, err := p.get(); !errors.Is(err, errReadFailed) {
		t.Fatalf("get = %v; want the read error", err)
	}
	if err := p.set([]byte("new")); !errors.Is(err, errReadFailed) {
		t.Fatalf("set = %v; want the read error", err)
	}
	store.failRead = false
	if got := mustGet(t, p); string(got) != "old" {
		t.Fatalf("get after a failed set = %q; want old", got)
	}
}
