// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/schotek/malachi/ui/internal/assistant"
)

// Loop runs callbacks on the application's main loop, where the panel's
// state lives. GTK passes glib.IdleAdd and glib.TimeoutAdd; the tests a
// queue of their own.
type Loop interface {
	// Post runs f on the main loop, after what was posted before; it may
	// be called from any goroutine.
	Post(f func())
	// After runs f on the main loop once d has passed.
	After(d time.Duration, f func())
}

// Settings are the preferences the panel reads and writes: the model, the
// consent (assistant-consent) and the claude executable
// (assistant-claude-path). settings.Store has them.
type Settings interface {
	AssistantModel() assistant.Model
	AssistantConsent() bool
	SetAssistantConsent(bool)
	AssistantClaudePath() string
}

// DefaultLocatorTimeout bounds one run of claude --version or claude auth
// status.
const DefaultLocatorTimeout = 10 * time.Second

// versionLimit is the most of the version line that is kept.
const versionLimit = 100

// Locator finds the user's Claude Code for the panel and asks it two
// things: its version and whether it is signed in. Malachi Mail never
// signs in, never reads a credential and never shows the account: claude
// auth status --json is read for its loggedIn only. (macOS:
// ClaudeCodeLocator.)
//
// Locate looks every time (a few stats): the path of assistant-claude-path
// first, then assistant.CandidatePaths (with the Node versions under
// ~/.nvm/versions/node), and returns the first that is an executable
// regular file (a symbolic link counts by its target, but the link's own
// path is kept: an nvm claude is a link to a Node script whose node sits
// beside the link).
//
// Version and SignedIn run the executable once each, with
// assistant.ChildEnv, in the panel's private directory, bounded by the
// timeout; their answers are kept per path until Refresh. A run that fails
// answers "not known", which the panel treats as "go ahead" and the
// settings as nothing to show. Main loop only; the answers come on it.
type Locator struct {
	settings Settings
	env      []string
	loop     Loop
	log      *slog.Logger
	timeout  time.Duration
	// dir is the working directory of the runs; "" leaves the
	// application's.
	dir string
	// usable says whether a candidate is the one (the tests keep it to
	// their own directory).
	usable func(string) bool

	versions map[string]*future[string]
	signIns  map[string]*future[SignIn]
}

// SignIn is what claude auth status said: whether it answered, and whether
// Claude Code is signed in.
type SignIn struct {
	Known, SignedIn bool
}

// NewLocator reads the setting from s and looks with env, the
// application's environment (HOME, PATH, and what assistant.ChildEnv keeps
// for the child); dir is the panel's private directory.
func NewLocator(s Settings, env []string, loop Loop, dir string, log *slog.Logger) *Locator {
	return &Locator{
		settings: s,
		env:      env,
		loop:     loop,
		log:      log,
		timeout:  DefaultLocatorTimeout,
		dir:      dir,
		usable:   IsExecutableFile,
		versions: make(map[string]*future[string]),
		signIns:  make(map[string]*future[SignIn]),
	}
}

// getenv is the last value of key in env, "" without one.
func getenv(env []string, key string) string {
	v := ""
	for _, kv := range env {
		if k, val, ok := strings.Cut(kv, "="); ok && k == key {
			v = val
		}
	}
	return v
}

func (l *Locator) home() string {
	if h := getenv(l.env, "HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}

// automatic are the usual places, in order.
func (l *Locator) automatic() []string {
	home := l.home()
	var nvm []string
	if entries, err := os.ReadDir(path.Join(home, ".nvm/versions/node")); err == nil {
		for _, e := range entries {
			nvm = append(nvm, e.Name())
		}
	}
	return assistant.CandidatePaths(home, getenv(l.env, "PATH"), nvm)
}

// Candidates are every path looked at, in order: the setting's, then the
// usual places.
func (l *Locator) Candidates() []string {
	list := l.automatic()
	if chosen := l.settings.AssistantClaudePath(); path.IsAbs(chosen) {
		clean := path.Clean(chosen)
		out := []string{clean}
		for _, p := range list {
			if p != clean {
				out = append(out, p)
			}
		}
		list = out
	}
	return list
}

// Locate is the claude executable to run, "" when there is none.
func (l *Locator) Locate() string {
	for _, p := range l.Candidates() {
		if l.usable(p) {
			return p
		}
	}
	return ""
}

// AutomaticPath is the claude executable found without the setting: the
// usual places, then the PATH ("" for none). The settings store nothing
// when the user chooses this one.
func (l *Locator) AutomaticPath() string {
	for _, p := range l.automatic() {
		if l.usable(p) {
			return p
		}
	}
	return ""
}

// IsExecutableFile says whether path is (or links to) a regular file with
// an execute bit.
func IsExecutableFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular() && fi.Mode()&0o111 != 0
}

// Refresh forgets the versions and sign-in states asked so far.
func (l *Locator) Refresh() {
	l.versions = make(map[string]*future[string])
	l.signIns = make(map[string]*future[SignIn])
}

// Version calls done with claude --version's first line for the located
// executable ("2.1.178 (Claude Code)"), "" when there is none or it failed.
func (l *Locator) Version(done func(string)) {
	p := l.Locate()
	if p == "" {
		done("")
		return
	}
	f, ok := l.versions[p]
	if !ok {
		f = &future[string]{}
		l.versions[p] = f
		l.run(p, []string{"--version"}, func(out []byte, status int, err error) {
			v := ""
			if err == nil && status == 0 {
				v = firstLine(out, versionLimit)
			}
			f.resolve(v)
		})
	}
	f.then(done)
}

// SignedIn calls done with whether the located Claude Code is signed in
// (claude auth status --json, its loggedIn); not known when there is none,
// the run failed or the output said nothing.
func (l *Locator) SignedIn(done func(SignIn)) {
	p := l.Locate()
	if p == "" {
		done(SignIn{})
		return
	}
	f, ok := l.signIns[p]
	if !ok {
		f = &future[SignIn]{}
		l.signIns[p] = f
		// The status may be non-zero when signed out; the JSON counts.
		l.run(p, []string{"auth", "status", "--json"}, func(out []byte, _ int, err error) {
			var st struct {
				LoggedIn *bool `json:"loggedIn"`
			}
			if err != nil || json.Unmarshal(out, &st) != nil || st.LoggedIn == nil {
				f.resolve(SignIn{})
				return
			}
			f.resolve(SignIn{Known: true, SignedIn: *st.LoggedIn})
		})
	}
	f.then(done)
}

// run runs claude with args off the main loop and hands its stdout, its
// exit status and an error that kept it from running (a timeout among
// them) to read on the main loop.
func (l *Locator) run(p string, args []string, read func(out []byte, status int, err error)) {
	env := assistant.ChildEnv(l.env, p)
	dir := ""
	if l.dir != "" && ensureDirectory(l.dir) == nil {
		dir = l.dir
	}
	timeout := l.timeout
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		cmd := exec.CommandContext(ctx, p, args...)
		cmd.Env = env
		cmd.Dir = dir
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		status := 0
		var exit *exec.ExitError
		switch {
		case err == nil:
		case ctx.Err() != nil:
			err = ctx.Err()
		case errors.As(err, &exit):
			status, err = exit.ExitCode(), nil
		}
		if err != nil {
			l.log.Warn("claude "+args[0], "err", err)
		}
		l.loop.Post(func() { read(out, status, err) })
	}()
}

// ensureDirectory creates the private directory (0700) when it is missing.
func ensureDirectory(dir string) error {
	return os.MkdirAll(dir, 0o700)
}

// firstLine is the first line of b with text, trimmed and cut to at most
// limit bytes at a character boundary.
func firstLine(b []byte, limit int) string {
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > limit {
			cut := limit
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = strings.TrimSpace(line[:cut])
		}
		return line
	}
	return ""
}

// future is an answer on the main loop that callers may wait for before it
// has come.
type future[T any] struct {
	done    bool
	val     T
	waiters []func(T)
}

func (f *future[T]) then(fn func(T)) {
	if f.done {
		fn(f.val)
		return
	}
	f.waiters = append(f.waiters, fn)
}

func (f *future[T]) resolve(v T) {
	f.done, f.val = true, v
	ws := f.waiters
	f.waiters = nil
	for _, w := range ws {
		w(v)
	}
}
