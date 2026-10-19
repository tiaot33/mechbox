//go:build unix

package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func moveToFreedesktopTrash(path string) error {
	if err := freedesktopTrash(path); err != nil {
		return fmt.Errorf("move %s to trash: %w", path, err)
	}
	return nil
}

func freedesktopTrash(path string) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return err
	}
	path = filepath.Join(parent, filepath.Base(path))
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	dev := device(info)
	home, err := homeTrash()
	if err != nil {
		return err
	}
	if err := prepareTrash(home); err != nil {
		return err
	}
	homeInfo, err := os.Stat(home)
	if err != nil {
		return err
	}
	if device(homeInfo) == dev {
		return trashInto(home, path, path)
	}
	top, err := mountTop(parent, dev)
	if err != nil {
		return err
	}
	trash, err := topdirTrash(top, os.Getuid())
	if err != nil {
		return fmt.Errorf("%s is on another file system and its trash can't be used: %w", top, err)
	}
	original, err := filepath.Rel(top, path)
	if err != nil {
		return err
	}
	return trashInto(trash, path, original)
}

func homeTrash() (string, error) {
	if data := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(data) {
		return filepath.Join(data, "Trash"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "Trash"), nil
}

func mountTop(dir string, dev uint64) (string, error) {
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir, nil
		}
		info, err := os.Stat(parent)
		if err != nil {
			return "", err
		}
		if device(info) != dev {
			return dir, nil
		}
		dir = parent
	}
}

func topdirTrash(top string, uid int) (string, error) {
	shared := filepath.Join(top, ".Trash")
	if info, err := os.Lstat(shared); err == nil && info.IsDir() && info.Mode()&fs.ModeSticky != 0 {
		trash := filepath.Join(shared, strconv.Itoa(uid))
		if err := ownTrashDir(trash, uid); err == nil {
			return trash, prepareTrash(trash)
		}
	}
	trash := filepath.Join(top, ".Trash-"+strconv.Itoa(uid))
	if err := ownTrashDir(trash, uid); err != nil {
		return "", err
	}
	return trash, prepareTrash(trash)
}

func ownTrashDir(dir string, uid int) error {
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || int(statOf(info).Uid) != uid {
		return fmt.Errorf("%s is not a directory owned by the current user", dir)
	}
	return nil
}

func prepareTrash(trash string) error {
	return errors.Join(os.MkdirAll(filepath.Join(trash, "files"), 0o700), os.MkdirAll(filepath.Join(trash, "info"), 0o700))
}

var errTrashNameTaken = errors.New("name already taken in trash")

func trashInto(trash, path, original string) error {
	content := trashInfo(original, time.Now())
	for i := 0; ; i++ {
		name := filepath.Base(path)
		if i > 0 {
			name += "." + strconv.Itoa(i)
		}
		if err := trashAs(trash, name, path, content); !errors.Is(err, errTrashNameTaken) {
			return err
		}
	}
}

func trashAs(trash, name, path, content string) error {
	info := filepath.Join(trash, "info", name+".trashinfo")
	file, err := os.OpenFile(info, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return errTrashNameTaken
	}
	if err != nil {
		return err
	}
	_, err = file.WriteString(content)
	err = errors.Join(err, file.Close())
	target := filepath.Join(trash, "files", name)
	if err == nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			err = errTrashNameTaken
		}
	}
	if err == nil {
		err = os.Rename(path, target)
	}
	if err != nil {
		_ = os.Remove(info)
	}
	return err
}

func trashInfo(original string, deleted time.Time) string {
	segments := strings.Split(filepath.ToSlash(original), "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", strings.Join(segments, "/"), deleted.Format("2006-01-02T15:04:05"))
}

func device(info fs.FileInfo) uint64 {
	return uint64(statOf(info).Dev)
}

func statOf(info fs.FileInfo) *syscall.Stat_t {
	return info.Sys().(*syscall.Stat_t)
}
