// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

// Package fsretry runs file operations that another process's open handle
// can hold up for a moment. Windows refuses to remove or rename a file, or
// to rename another file over it, while a handle of it is open without
// delete sharing, which is how Go's os.Open opens: a client reading the
// daemon's key file, one of the daemon's own readers of a raw message, a
// virus scanner perhaps. The operation goes through once that handle is
// closed. Elsewhere an open handle is no obstacle and the first attempt is
// the one that counts.
package fsretry

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

// Waits are the pauses of Do between its attempts: ten attempts over about
// 1.3 s. A variable so that tests can shorten it.
var Waits = []time.Duration{
	20 * time.Millisecond, 40 * time.Millisecond, 80 * time.Millisecond, 160 * time.Millisecond,
	200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond, 200 * time.Millisecond,
	200 * time.Millisecond,
}

// Do runs op until it succeeds, fails because the file does not exist, or
// has failed len(Waits)+1 times, and returns op's last error. Any other
// error is tried again: the refusal has no portable error value (a sharing
// violation, or access denied when a rename would replace the open file),
// so an error that lasts, such as a missing permission, is returned only
// after all the waits.
func Do(op func() error) error {
	err := op()
	for _, wait := range Waits {
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return err
		}
		time.Sleep(wait)
		err = op()
	}
	return err
}

// Remove is os.Remove through Do.
func Remove(path string) error {
	return Do(func() error { return os.Remove(path) })
}

// Rename is os.Rename through Do.
func Rename(oldpath, newpath string) error {
	return Do(func() error { return os.Rename(oldpath, newpath) })
}
