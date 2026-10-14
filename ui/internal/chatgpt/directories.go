// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package chatgpt

import (
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var sessionName = regexp.MustCompile(`^session-[a-f0-9]{64}$`)

// Sweep removes only abandoned owned profiles with an exclusively acquired lease.
func Sweep(root string) error {
	if err := privateDirectory(root); err != nil {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() || !sessionName.MatchString(e.Name()) {
			continue
		}
		path := filepath.Join(root, e.Name())
		unsafe := false
		_ = filepath.WalkDir(path, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.Type()&os.ModeSymlink != 0 {
				unsafe = true
			}
			return err
		})
		if unsafe {
			continue
		}
		lease, err := openPrivate(filepath.Join(path, "lease"), syscall.O_RDWR)
		if err != nil {
			continue
		}
		if syscall.Flock(int(lease.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil {
			_ = os.RemoveAll(path)
		}
		_ = lease.Close()
	}
	return nil
}
