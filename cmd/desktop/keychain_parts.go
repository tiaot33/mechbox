package main

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

const credentialBlobLimit = 2560

var partsHeader = []byte("mechbox-parts:")

type credentialStore interface {
	read(target string) ([]byte, bool, error)
	write(target, user string, blob []byte) error
	remove(target string) error
	list(prefix string) ([]string, error)
}

type partedCredential struct {
	store  credentialStore
	target string
	user   string
}

func (p partedCredential) get() ([]byte, bool, error) {
	blob, ok, err := p.store.read(p.target)
	if err != nil || !ok {
		return nil, false, err
	}
	gen, count, parted, err := parseParts(blob)
	if err != nil {
		return nil, false, nil
	}
	if !parted {
		return blob, true, nil
	}
	var value []byte
	for i := range count {
		part, ok, err := p.store.read(p.partTarget(gen, i))
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, nil
		}
		value = append(value, part...)
	}
	return value, true, nil
}

func (p partedCredential) set(value []byte) error {
	old, _, err := p.store.read(p.target)
	if err != nil {
		return err
	}
	gen, _, _, _ := parseParts(old)
	keep := map[string]bool{}
	if len(value) <= credentialBlobLimit && !bytes.HasPrefix(value, partsHeader) {
		if err := p.store.write(p.target, p.user, value); err != nil {
			return err
		}
	} else {
		gen++
		var parts [][]byte
		for rest := value; len(rest) > 0; {
			n := min(len(rest), credentialBlobLimit)
			parts, rest = append(parts, rest[:n]), rest[n:]
		}
		for i, part := range parts {
			if err := p.store.write(p.partTarget(gen, i), p.user, part); err != nil {
				p.removeParts(gen, i)
				return err
			}
			keep[p.partTarget(gen, i)] = true
		}
		if err := p.store.write(p.target, p.user, fmt.Appendf(bytes.Clone(partsHeader), "%d:%d", gen, len(parts))); err != nil {
			p.removeParts(gen, len(parts))
			return err
		}
	}
	if targets, err := p.store.list(p.target + ":"); err == nil {
		for _, target := range targets {
			if !keep[target] {
				_ = p.store.remove(target)
			}
		}
	}
	return nil
}

func (p partedCredential) delete() error {
	if err := p.store.remove(p.target); err != nil {
		return err
	}
	targets, err := p.store.list(p.target + ":")
	if err != nil {
		return err
	}
	for _, target := range targets {
		if err := p.store.remove(target); err != nil {
			return err
		}
	}
	return nil
}

func (p partedCredential) partTarget(gen, i int) string {
	return fmt.Sprintf("%s:%d:%d", p.target, gen, i)
}

func (p partedCredential) removeParts(gen, count int) {
	for i := range count {
		_ = p.store.remove(p.partTarget(gen, i))
	}
}

func parseParts(blob []byte) (gen, count int, parted bool, err error) {
	rest, ok := bytes.CutPrefix(blob, partsHeader)
	if !ok {
		return 0, 0, false, nil
	}
	g, c, ok := strings.Cut(string(rest), ":")
	if ok {
		gen, err = strconv.Atoi(g)
	}
	if ok && err == nil {
		count, err = strconv.Atoi(c)
	}
	if !ok || err != nil || gen < 1 || count < 1 {
		return 0, 0, false, fmt.Errorf("malformed parts header %q", blob)
	}
	return gen, count, true, nil
}
