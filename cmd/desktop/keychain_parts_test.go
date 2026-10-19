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
	failWrite func(target string) bool
}

var errWriteFailed = errors.New("write failed")

func (f *fakeCredentials) read(target string) ([]byte, bool, error) {
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

func TestPartedCredentialReportsMissingPart(t *testing.T) {
	p, store := newParted()
	if err := p.set(bytes.Repeat([]byte("x"), credentialBlobLimit*2)); err != nil {
		t.Fatal(err)
	}
	delete(store.items, p.partTarget(1, 1))
	if _, _, err := p.get(); err == nil || !strings.Contains(err.Error(), "part 2 of 2 missing") {
		t.Fatalf("get with a missing part = %v; want an error", err)
	}
}
