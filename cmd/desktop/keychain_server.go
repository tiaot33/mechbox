//go:build server

package main

import "sync"

var memoryKeychain = struct {
	sync.Mutex
	items map[keychain][]byte
}{items: map[keychain][]byte{}}

func (k keychain) get() ([]byte, bool, error) {
	memoryKeychain.Lock()
	defer memoryKeychain.Unlock()
	value, ok := memoryKeychain.items[k]
	return value, ok, nil
}

func (k keychain) set(value []byte) error {
	memoryKeychain.Lock()
	defer memoryKeychain.Unlock()
	memoryKeychain.items[k] = append([]byte(nil), value...)
	return nil
}

func (k keychain) delete() error {
	memoryKeychain.Lock()
	defer memoryKeychain.Unlock()
	delete(memoryKeychain.items, k)
	return nil
}
