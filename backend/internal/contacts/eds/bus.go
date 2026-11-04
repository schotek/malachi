// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: AGPL-3.0-only

package eds

import (
	"context"

	"github.com/godbus/dbus/v5"
)

// bus is the slice of *dbus.Conn the client uses, so tests can substitute a
// fake without a session bus.
type bus interface {
	Object(dest string, path dbus.ObjectPath) dbus.BusObject
	AddMatchSignalContext(ctx context.Context, options ...dbus.MatchOption) error
	RemoveMatchSignalContext(ctx context.Context, options ...dbus.MatchOption) error
	Signal(ch chan<- *dbus.Signal)
	RemoveSignal(ch chan<- *dbus.Signal)
	Close() error
}

var _ bus = (*dbus.Conn)(nil)

func dialSessionBus() (bus, error) { return dbus.ConnectSessionBus() }
