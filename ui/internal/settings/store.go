// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Package settings persists UI-only preferences in GSettings.
//
// The schema lives in data/io.github.schotek.Malachi.gschema.xml. Only
// presentation options belong here; anything that affects mail handling is
// owned by the daemon and set through the RPC API (CLAUDE.md rule 1).
//
// When the schema is not installed (for example `go run` outside `make`,
// which exports GSETTINGS_SCHEMA_DIR) Open falls back to an in-memory store
// so the application keeps working; values then last until it exits.
package settings

import (
	"log/slog"
	"math"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
)

// SchemaID must match the gschema, desktop file and application ID.
const SchemaID = "io.github.schotek.Malachi"

// Keys of the general settings. They must match the gschema.
const (
	KeyLaunchAtLogin        = "launch-at-login"
	KeyRunInBackground      = "run-in-background"
	KeyMarkReadDelay        = "mark-read-delay"
	KeyConfirmDelete        = "confirm-delete"
	KeyDesktopNotifications = "desktop-notifications"
	KeyNotificationSound    = "notification-sound"
)

// MarkReadDelayMax is the largest accepted mark-read delay in seconds; must
// match the <range> in the gschema.
const MarkReadDelayMax = 60

// Keys of the appearance settings. They must match the gschema.
const (
	KeyColorScheme        = "color-scheme"
	KeyDensity            = "message-list-density"
	KeyShowPreviewLine    = "show-preview-line"
	KeyShowAvatars        = "show-avatars"
	KeyMonochromeAvatars  = "monochrome-avatars"
	KeyMonospacePlainText = "monospace-plain-text"
	KeyTextZoom           = "text-zoom"
	// KeyGroupByConversation switches the message list to one row per
	// conversation (thread.list) instead of one per message.
	KeyGroupByConversation = "group-by-conversation"
)

// Keys of the sidebar state: which parts of the folder tree the user folded
// away and which folders are pinned to the Favourites section. Each entry is
// one node; the encoding is the window package's business
// (internal/window/collapse.go, favourites.go). They must match the gschema.
const (
	KeyCollapsedFolders  = "collapsed-folders"
	KeyCollapsedAccounts = "collapsed-accounts"
	KeyFavouriteFolders  = "favourite-folders"
)

// ColorScheme is the nick of the ColorScheme enum in the gschema.
type ColorScheme string

// Density is the nick of the Density enum in the gschema.
type Density string

const (
	ColorSchemeSystem ColorScheme = "system"
	ColorSchemeLight  ColorScheme = "light"
	ColorSchemeDark   ColorScheme = "dark"

	DensityComfortable Density = "comfortable"
	DensityCompact     Density = "compact"

	// Text zoom bounds in percent; must match the <range> in the gschema.
	TextZoomMin  = 50
	TextZoomMax  = 200
	TextZoomStep = 10
)

// defaults mirror the gschema defaults for the in-memory fallback.
var defaults = map[string]any{
	KeyLaunchAtLogin:        false,
	KeyRunInBackground:      false,
	KeyMarkReadDelay:        2,
	KeyConfirmDelete:        true,
	KeyDesktopNotifications: true,
	KeyNotificationSound:    false,

	KeyColorScheme:         string(ColorSchemeSystem),
	KeyDensity:             string(DensityComfortable),
	KeyShowPreviewLine:     true,
	KeyShowAvatars:         true,
	KeyMonochromeAvatars:   false,
	KeyMonospacePlainText:  false,
	KeyTextZoom:            100,
	KeyGroupByConversation: false,

	KeyCollapsedFolders:  []string(nil),
	KeyCollapsedAccounts: []string(nil),
	KeyFavouriteFolders:  []string(nil),
}

// Store reads and writes preferences. All methods must be called from the
// GTK main loop; change handlers run there too, so no glib.IdleAdd is needed.
type Store struct {
	gs  *gio.Settings  // nil in the in-memory fallback
	mem map[string]any // in-memory values (fallback only)

	handlers map[string]map[int]func()
	nextID   int
}

// Open returns a GSettings-backed store, or an in-memory one (with a logged
// warning) when the schema cannot be found. It never aborts.
func Open(log *slog.Logger) *Store {
	return open(SchemaID, log)
}

func open(schemaID string, log *slog.Logger) *Store {
	// gio.NewSettings aborts the process on an unknown schema, so look it up
	// first. The default source itself is nil when no schemas are installed.
	src := gio.SettingsSchemaSourceGetDefault()
	if src == nil || src.Lookup(schemaID, true) == nil {
		log.Warn("GSettings schema not found; preferences will not persist",
			"schema", schemaID, "hint", "run via `make run-dev` or set GSETTINGS_SCHEMA_DIR")
		return NewMemory()
	}

	s := &Store{
		gs:       gio.NewSettings(schemaID),
		handlers: make(map[string]map[int]func()),
	}
	// GSettings only emits "changed" for keys that were read after the
	// handler was connected, so connect first and then prime every key.
	s.gs.ConnectChanged(func(key string) { s.fire(key) })
	for key := range defaults {
		_ = s.gs.Value(key)
	}
	return s
}

// NewMemory returns a store that keeps values only for the process lifetime.
func NewMemory() *Store {
	s := &Store{
		mem:      make(map[string]any, len(defaults)),
		handlers: make(map[string]map[int]func()),
	}
	for k, v := range defaults {
		s.mem[k] = v
	}
	return s
}

// Persistent reports whether values survive a restart.
func (s *Store) Persistent() bool { return s.gs != nil }

// LaunchAtLogin mirrors the last autostart decision granted by the
// Background portal; the portal, not this key, is authoritative.
func (s *Store) LaunchAtLogin() bool     { return s.boolean(KeyLaunchAtLogin) }
func (s *Store) SetLaunchAtLogin(v bool) { s.set(KeyLaunchAtLogin, v) }

func (s *Store) RunInBackground() bool     { return s.boolean(KeyRunInBackground) }
func (s *Store) SetRunInBackground(v bool) { s.set(KeyRunInBackground, v) }

// MarkReadDelay is in seconds; 0 marks a message read as soon as it is shown.
func (s *Store) MarkReadDelay() int { return s.integer(KeyMarkReadDelay) }
func (s *Store) SetMarkReadDelay(v int) {
	s.set(KeyMarkReadDelay, min(max(v, 0), MarkReadDelayMax))
}

func (s *Store) ConfirmDelete() bool     { return s.boolean(KeyConfirmDelete) }
func (s *Store) SetConfirmDelete(v bool) { s.set(KeyConfirmDelete, v) }

func (s *Store) DesktopNotifications() bool     { return s.boolean(KeyDesktopNotifications) }
func (s *Store) SetDesktopNotifications(v bool) { s.set(KeyDesktopNotifications, v) }

func (s *Store) NotificationSound() bool     { return s.boolean(KeyNotificationSound) }
func (s *Store) SetNotificationSound(v bool) { s.set(KeyNotificationSound, v) }

func (s *Store) ColorScheme() ColorScheme { return ColorScheme(s.str(KeyColorScheme)) }

// SetColorScheme ignores values outside the enum.
func (s *Store) SetColorScheme(v ColorScheme) {
	switch v {
	case ColorSchemeSystem, ColorSchemeLight, ColorSchemeDark:
		s.set(KeyColorScheme, string(v))
	}
}

func (s *Store) Density() Density { return Density(s.str(KeyDensity)) }

// SetDensity ignores values outside the enum.
func (s *Store) SetDensity(v Density) {
	switch v {
	case DensityComfortable, DensityCompact:
		s.set(KeyDensity, string(v))
	}
}

func (s *Store) ShowPreviewLine() bool     { return s.boolean(KeyShowPreviewLine) }
func (s *Store) SetShowPreviewLine(v bool) { s.set(KeyShowPreviewLine, v) }

func (s *Store) ShowAvatars() bool     { return s.boolean(KeyShowAvatars) }
func (s *Store) SetShowAvatars(v bool) { s.set(KeyShowAvatars, v) }

func (s *Store) MonochromeAvatars() bool     { return s.boolean(KeyMonochromeAvatars) }
func (s *Store) SetMonochromeAvatars(v bool) { s.set(KeyMonochromeAvatars, v) }

func (s *Store) GroupByConversation() bool     { return s.boolean(KeyGroupByConversation) }
func (s *Store) SetGroupByConversation(v bool) { s.set(KeyGroupByConversation, v) }

func (s *Store) MonospacePlainText() bool     { return s.boolean(KeyMonospacePlainText) }
func (s *Store) SetMonospacePlainText(v bool) { s.set(KeyMonospacePlainText, v) }

// TextZoom is the message body zoom in percent.
func (s *Store) TextZoom() int { return s.integer(KeyTextZoom) }

// SetTextZoom clamps v to [TextZoomMin, TextZoomMax].
func (s *Store) SetTextZoom(v int) {
	s.set(KeyTextZoom, min(max(v, TextZoomMin), TextZoomMax))
}

// CollapsedFolders and CollapsedAccounts are the folded-away nodes of the
// folder sidebar, each entry one node. The window package owns the encoding
// and tolerates entries it cannot parse, so an older or newer version of the
// application never loses more than the rows it does not understand.
func (s *Store) CollapsedFolders() []string     { return s.strv(KeyCollapsedFolders) }
func (s *Store) SetCollapsedFolders(v []string) { s.set(KeyCollapsedFolders, v) }

func (s *Store) CollapsedAccounts() []string     { return s.strv(KeyCollapsedAccounts) }
func (s *Store) SetCollapsedAccounts(v []string) { s.set(KeyCollapsedAccounts, v) }

// FavouriteFolders are the folders pinned to the sidebar's Favourites
// section, encoded like CollapsedFolders (internal/window/favourites.go).
func (s *Store) FavouriteFolders() []string     { return s.strv(KeyFavouriteFolders) }
func (s *Store) SetFavouriteFolders(v []string) { s.set(KeyFavouriteFolders, v) }

// OnChanged calls f whenever key changes, from any source (this process or,
// with the GSettings backend, another one). The returned function removes
// the handler; callers that outlive the store may ignore it.
func (s *Store) OnChanged(key string, f func()) (remove func()) {
	id := s.nextID
	s.nextID++
	if s.handlers[key] == nil {
		s.handlers[key] = make(map[int]func())
	}
	s.handlers[key][id] = f
	return func() { delete(s.handlers[key], id) }
}

// Bind keeps property of obj and key in sync in both directions, starting
// from the stored value. Numeric keys convert to the property's type (an
// integer key may drive a double property). Do not also listen to the
// property yourself. The returned function removes the binding; call it
// when obj is about to go away and the store is not (e.g. a dialog).
func (s *Store) Bind(key string, obj *coreglib.Object, property string) (unbind func()) {
	if s.gs != nil {
		s.gs.Bind(key, obj, property, gio.SettingsBindDefault)
		return func() { gio.SettingsUnbind(obj, property) }
	}

	push := func() {
		cur := obj.ObjectProperty(property)
		if want := coerce(s.mem[key], cur); want != cur {
			obj.SetObjectProperty(property, want)
		}
	}
	push()
	handle := obj.NotifyProperty(property, func() {
		s.set(key, coerce(obj.ObjectProperty(property), s.mem[key]))
	})
	remove := s.OnChanged(key, push)
	return func() {
		remove()
		obj.HandlerDisconnect(handle)
	}
}

func (s *Store) str(key string) string {
	if s.gs != nil {
		return s.gs.String(key)
	}
	v, _ := s.mem[key].(string)
	return v
}

func (s *Store) boolean(key string) bool {
	if s.gs != nil {
		return s.gs.Boolean(key)
	}
	v, _ := s.mem[key].(bool)
	return v
}

func (s *Store) integer(key string) int {
	if s.gs != nil {
		return s.gs.Int(key)
	}
	v, _ := s.mem[key].(int)
	return v
}

// strv reads a string-array key. The result is always a fresh slice, so a
// caller may keep and mutate it without touching the store.
func (s *Store) strv(key string) []string {
	if s.gs != nil {
		return s.gs.Strv(key)
	}
	v, _ := s.mem[key].([]string)
	return append([]string(nil), v...)
}

// set stores v; the GSettings backend emits "changed" itself, the memory
// backend fires handlers synchronously when the value actually changed.
func (s *Store) set(key string, v any) {
	if s.gs != nil {
		switch v := v.(type) {
		case string:
			s.gs.SetString(key, v)
		case bool:
			s.gs.SetBoolean(key, v)
		case int:
			s.gs.SetInt(key, v)
		case []string:
			s.gs.SetStrv(key, v)
		}
		return
	}
	// Slices are not comparable: == on two interfaces holding one panics, so
	// they are compared element by element and stored as a copy.
	if list, ok := v.([]string); ok {
		old, _ := s.mem[key].([]string)
		if equalStrings(old, list) {
			return
		}
		s.mem[key] = append([]string(nil), list...)
		s.fire(key)
		return
	}
	if s.mem[key] == v {
		return
	}
	s.mem[key] = v
	s.fire(key)
}

// equalStrings reports whether two string lists have the same contents.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *Store) fire(key string) {
	// Copy first: a handler may remove itself.
	fs := make([]func(), 0, len(s.handlers[key]))
	for _, f := range s.handlers[key] {
		fs = append(fs, f)
	}
	for _, f := range fs {
		f()
	}
}

// coerce converts v to the dynamic type of like, for the numeric conversions
// GSettings would do itself (int key ↔ double property).
func coerce(v, like any) any {
	switch like.(type) {
	case float64:
		switch v := v.(type) {
		case int:
			return float64(v)
		case uint:
			return float64(v)
		}
	case int:
		switch v := v.(type) {
		case float64:
			return int(math.Round(v))
		case uint:
			return int(v)
		}
	case uint:
		switch v := v.(type) {
		case int:
			return uint(v)
		case float64:
			return uint(math.Round(v))
		}
	}
	return v
}
