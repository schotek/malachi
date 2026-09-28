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
	return Do(func() error { return remove(path) })
}

// remove is os.Remove, a variable so that tests can count the attempts.
var remove = os.Remove

// Rename is os.Rename through Do.
func Rename(oldpath, newpath string) error {
	return Do(func() error { return os.Rename(oldpath, newpath) })
}

// RemoveAll is os.RemoveAll through Do: an attempt removes what it can,
// the next one what a handle held up.
func RemoveAll(path string) error {
	return Do(func() error { return os.RemoveAll(path) })
}

// Batch removes many files for one caller. Each removal is retried as
// Remove does until one fails even so; from then on the batch tries each
// file once. What holds up a single file, a reader, is over in a moment;
// what outlasts the retries of one file, a directory without write
// permission say, likely holds up the others, and would otherwise cost the
// whole wait for every file. The zero value is ready; a Batch is for one
// goroutine.
type Batch struct {
	gaveUp bool
}

// Remove removes path within the batch.
func (b *Batch) Remove(path string) error {
	return b.Do(func() error { return remove(path) })
}

// Do runs op within the batch: through Do until an op fails even so, then
// once. A failure because the file does not exist does not count.
func (b *Batch) Do(op func() error) error {
	if b.gaveUp {
		return op()
	}
	err := Do(op)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		b.gaveUp = true
	}
	return err
}
