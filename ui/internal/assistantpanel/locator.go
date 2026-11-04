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
	"slices"
	"strings"
	"syscall"
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
// things: its version and whether it is signed in; SignIn runs Claude
// Code's own sign-in. Malachi Mail never reads a credential and never
// shows the account: claude auth status --json is read for its loggedIn
// only, and claude auth login does the signing in. (macOS:
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

	// signIn is the sign-in under way (SignIn), nil without one; watchers
	// hear when one starts or ends.
	signIn        *signInRun
	signInTimeout time.Duration
	watchers      map[int]func()
	nextWatcher   int
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

		signInTimeout: DefaultSignInTimeout,
		watchers:      make(map[int]func()),
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

// DefaultSignInTimeout is how long the browser is waited for.
const DefaultSignInTimeout = 10 * time.Minute

// signInGrace is the time from SIGTERM to SIGKILL for a sign-in that is
// cancelled or out of time.
const signInGrace = 2 * time.Second

// SignInOutcome is how Claude Code's sign-in ended.
type SignInOutcome int

// The outcomes of a sign-in.
const (
	// SignInDone: claude auth login ended with status 0.
	SignInDone SignInOutcome = iota
	// SignInFailed: it ended badly, or could not run; Reason says how.
	SignInFailed
	// SignInTimedOut: the browser brought no answer within the timeout.
	SignInTimedOut
	// SignInCancelled: it was cancelled, or another sign-in took its place.
	SignInCancelled
	// SignInNotFound: there is no Claude Code to sign in.
	SignInNotFound
)

// SignInResult is how a sign-in ended; Reason is technical (stderr's first
// line, else the exit status in words) and set for SignInFailed only.
type SignInResult struct {
	Outcome SignInOutcome
	Reason  string
}

// signInRun is the sign-in under way.
type signInRun struct {
	cancel context.CancelFunc
	done   func(SignInResult)
}

// SigningIn says whether a sign-in is under way.
func (l *Locator) SigningIn() bool { return l.signIn != nil }

// SignIn runs Claude Code's own sign-in for the located executable (claude
// auth login: assistant.SignInArgs with assistant.SignInEnv, in the panel's
// private directory). Claude Code opens the browser, the user signs in to
// Claude there, and Claude Code stores the sign-in itself. Malachi Mail
// only waits for the process to end: it sees no credential, and what the
// process prints is neither shown nor logged (the address it names belongs
// to the sign-in; only stderr's first line is the reason of a failure).
// done is called once, on the main loop; the answers kept for SignedIn are
// forgotten first, so the next question asks afresh.
//
// One sign-in at a time, for the panel and the settings alike: a new one
// takes the place of the one under way, which ends as SignInCancelled.
// cancel ends this sign-in the same way and does nothing once it is over.
func (l *Locator) SignIn(done func(SignInResult)) (cancel func()) {
	l.CancelSignIn()
	p := l.Locate()
	if p == "" {
		l.loop.Post(func() { done(SignInResult{Outcome: SignInNotFound}) })
		return func() {}
	}
	env := assistant.SignInEnv(l.env, p)
	dir := ""
	if l.dir != "" && ensureDirectory(l.dir) == nil {
		dir = l.dir
	}
	ctx, stop := context.WithCancel(context.Background())
	run := &signInRun{cancel: stop, done: done}
	l.signIn = run
	l.signInChanged()
	timeout := l.signInTimeout
	go func() {
		limit, release := context.WithTimeout(ctx, timeout)
		defer release()
		cmd := exec.CommandContext(limit, p, assistant.SignInArgs...)
		cmd.Env = env
		cmd.Dir = dir
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = signInGrace
		var stderr limitedBuffer
		cmd.Stderr = &stderr
		err := cmd.Run()
		if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil && cmd.ProcessState.Success() {
			// The browser it started still holds its stderr.
			err = nil
		}
		result := SignInResult{Outcome: SignInDone}
		var exit *exec.ExitError
		switch {
		case ctx.Err() != nil:
			result.Outcome = SignInCancelled
		case limit.Err() != nil:
			result.Outcome = SignInTimedOut
		case errors.As(err, &exit):
			reason := Exit{Status: exit.ExitCode(), Reason: firstLine(stderr.Bytes(), reasonLimit)}.Description()
			result = SignInResult{Outcome: SignInFailed, Reason: reason}
		case err != nil:
			result = SignInResult{Outcome: SignInFailed, Reason: "claude could not be started: " + err.Error()}
		}
		if result.Outcome != SignInDone {
			// The outcome alone: stderr may name the account.
			l.log.Warn("claude auth login", "outcome", int(result.Outcome))
		}
		l.loop.Post(func() {
			stop()
			if l.signIn != run {
				return // cancelled, and reported then
			}
			l.signIn = nil
			l.Refresh()
			l.signInChanged()
			done(result)
		})
	}()
	return func() {
		if l.signIn == run {
			l.CancelSignIn()
		}
	}
}

// CancelSignIn ends the sign-in under way, which is reported as
// SignInCancelled; nothing without one.
func (l *Locator) CancelSignIn() {
	run := l.signIn
	if run == nil {
		return
	}
	l.signIn = nil
	run.cancel()
	l.Refresh()
	l.signInChanged()
	done := run.done
	l.loop.Post(func() { done(SignInResult{Outcome: SignInCancelled}) })
}

// OnSignInChange calls f whenever a sign-in starts or ends (SigningIn),
// until the returned function is called.
func (l *Locator) OnSignInChange(f func()) (remove func()) {
	id := l.nextWatcher
	l.nextWatcher++
	l.watchers[id] = f
	return func() { delete(l.watchers, id) }
}

func (l *Locator) signInChanged() {
	ids := make([]int, 0, len(l.watchers))
	for id := range l.watchers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		if f, ok := l.watchers[id]; ok {
			f()
		}
	}
}

// run runs claude with args off the main loop and hands its stdout, its
// exit status and an error that kept it from running (a timeout among
// them) to read on the main loop. A run out of time is ended as a
// conversation is (Process.Terminate) and the sign-in: SIGTERM, so that
// Claude Code can let go of what it holds, and SIGKILL after
// DefaultKillGrace.
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
		cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
		cmd.WaitDelay = DefaultKillGrace
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
