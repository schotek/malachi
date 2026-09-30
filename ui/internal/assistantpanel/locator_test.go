// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

package assistantpanel

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// testLocator counts only paths inside dir, so a Claude Code installed on
// this computer never answers.
func testLocator(dir string, s *memSettings, env []string, loop *testLoop) *Locator {
	l := NewLocator(s, env, loop, dir, discardLog())
	l.timeout = 5 * time.Second
	prefix := dir + "/"
	l.usable = func(p string) bool { return strings.HasPrefix(p, prefix) && IsExecutableFile(p) }
	return l
}

func TestLocatorTheSettingComesFirst(t *testing.T) {
	dir := scratch(t)
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, ".local/bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	native := writeScript(t, filepath.Join(home, ".local/bin/claude"), "exit 0")
	chosen := writeScript(t, filepath.Join(dir, "my-claude"), "exit 0")
	s := &memSettings{}
	l := testLocator(dir, s, []string{"HOME=" + home, "PATH="}, newTestLoop())
	if got := l.Locate(); got != native {
		t.Errorf("Locate = %q, want the native install", got)
	}
	s.claudePath = chosen
	if got := l.Locate(); got != chosen || l.Candidates()[0] != chosen {
		t.Errorf("Locate = %q, want the setting's", got)
	}
	if got := l.AutomaticPath(); got != native {
		t.Errorf("AutomaticPath = %q, want the native install", got)
	}
	// A chosen path that is not an executable file falls back.
	plain := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(plain, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{plain, dir, "relative/claude"} {
		s.claudePath = p
		if got := l.Locate(); got != native {
			t.Errorf("setting %q: Locate = %q, want the native install", p, got)
		}
	}
}

func TestLocatorLinksCountByTheirTargetAndKeepTheirPath(t *testing.T) {
	dir := scratch(t)
	home := filepath.Join(dir, "home")
	bin := filepath.Join(home, ".nvm/versions/node/v20.19.0/bin")
	lib := filepath.Join(home, ".nvm/versions/node/v20.19.0/lib")
	for _, d := range []string{bin, lib} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	target := writeScript(t, filepath.Join(lib, "cli.js"), "exit 0")
	link := filepath.Join(bin, "claude")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	l := testLocator(dir, &memSettings{}, []string{"HOME=" + home, "PATH="}, newTestLoop())
	if got := l.Locate(); got != link {
		t.Errorf("Locate = %q, want the link %q", got, link)
	}
	// A dangling link is nothing.
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if got := l.Locate(); got != "" {
		t.Errorf("Locate = %q, want nothing", got)
	}
}

func TestLocatorNothingFound(t *testing.T) {
	dir := scratch(t)
	loop := newTestLoop()
	l := testLocator(dir, &memSettings{}, []string{"HOME=" + dir, "PATH="}, loop)
	if got := l.Locate(); got != "" {
		t.Errorf("Locate = %q", got)
	}
	var version *string
	var signIn *SignIn
	l.Version(func(v string) { version = &v })
	l.SignedIn(func(s SignIn) { signIn = &s })
	loop.runUntil(t, func() bool { return version != nil && signIn != nil })
	if *version != "" || *signIn != (SignIn{}) {
		t.Errorf("version %q, sign-in %+v; want neither known", *version, *signIn)
	}
}

// --version and auth status --json run once each and are kept until
// Refresh.
func TestLocatorVersionAndSignInAreAskedOnce(t *testing.T) {
	dir := scratch(t)
	state := filepath.Join(dir, "logged-in")
	if err := os.WriteFile(state, []byte("true"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe := writeScript(t, filepath.Join(dir, "claude"), fmt.Sprintf(`echo "$*" >> '%s/calls'
case "$1" in
--version) echo '2.1.178 (Claude Code)'; echo 'more'; exit 0;;
auth) printf '{"loggedIn": %%s, "authMethod": "claude.ai", "email": "me@example.invalid"}\n' "$(cat '%s')"; exit 0;;
esac
exit 2`, dir, state))
	loop := newTestLoop()
	l := testLocator(dir, &memSettings{claudePath: exe}, []string{"HOME=" + dir, "PATH="}, loop)
	ask := func() (string, SignIn) {
		var v *string
		var s *SignIn
		l.Version(func(x string) { v = &x })
		l.SignedIn(func(x SignIn) { s = &x })
		loop.runUntil(t, func() bool { return v != nil && s != nil })
		return *v, *s
	}
	for range 2 {
		if v, s := ask(); v != "2.1.178 (Claude Code)" || s != (SignIn{Known: true, SignedIn: true}) {
			t.Errorf("version %q, sign-in %+v", v, s)
		}
	}
	// The two run side by side: their order is not known.
	calls := func() []string {
		c := strings.Split(strings.TrimSpace(readFile(t, filepath.Join(dir, "calls"))), "\n")
		slices.Sort(c)
		return c
	}
	if got := calls(); !slices.Equal(got, []string{"--version", "auth status --json"}) {
		t.Errorf("calls %q, want each once", got)
	}
	if err := os.WriteFile(state, []byte("false"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, s := ask(); s != (SignIn{Known: true, SignedIn: true}) {
		t.Errorf("sign-in %+v before Refresh, want the kept answer", s)
	}
	l.Refresh()
	if _, s := ask(); s != (SignIn{Known: true, SignedIn: false}) {
		t.Errorf("sign-in %+v after Refresh, want signed out", s)
	}
	if got := len(calls()); got != 4 {
		t.Errorf("%d calls after Refresh, want 4", got)
	}
	// The runs happened in the private directory, created on demand.
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Errorf("directory: %v", err)
	}
}

// A run that fails or says nothing is not known.
func TestLocatorFailedRunsAreUnknown(t *testing.T) {
	dir := scratch(t)
	exe := writeScript(t, filepath.Join(dir, "claude"), `case "$1" in
--version) exit 1;;
auth) echo 'not json'; exit 0;;
esac`)
	loop := newTestLoop()
	l := testLocator(dir, &memSettings{claudePath: exe}, []string{"HOME=" + dir, "PATH="}, loop)
	var v *string
	var s *SignIn
	l.Version(func(x string) { v = &x })
	l.SignedIn(func(x SignIn) { s = &x })
	loop.runUntil(t, func() bool { return v != nil && s != nil })
	if *v != "" || *s != (SignIn{}) {
		t.Errorf("version %q, sign-in %+v; want neither known", *v, *s)
	}
}

// signInClaude is a claude whose auth login runs login.sh in dir and whose
// auth status says what the file logged-in holds.
func signInClaude(t *testing.T, dir string) string {
	t.Helper()
	return writeScript(t, filepath.Join(dir, "claude"), fmt.Sprintf(`D='%s'
case "$1 $2" in
"auth login") echo "$*" >> "$D/calls"; pwd -P > "$D/cwd"; . "$D/login.sh"; exit 0;;
"auth status") printf '{"loggedIn": %%s}\n' "$(cat "$D/logged-in")"; exit 0;;
esac
exit 2`, dir))
}

// Claude Code's own sign-in: claude auth login in the private directory,
// its end reported once, and the kept sign-in state asked afresh after it.
func TestLocatorSignIn(t *testing.T) {
	dir := scratch(t)
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("logged-in", "false")
	write("login.sh", `echo 'Opening browser to sign in…'
echo 'If the browser did not open, visit: https://claude.example/authorize?state=secret'
printf true > "$D/logged-in"`)
	exe := signInClaude(t, dir)
	loop := newTestLoop()
	l := testLocator(dir, &memSettings{claudePath: exe}, []string{"HOME=" + dir, "PATH="}, loop)
	changes := 0
	remove := l.OnSignInChange(func() { changes++ })
	signedIn := func() SignIn {
		var s *SignIn
		l.SignedIn(func(x SignIn) { s = &x })
		loop.runUntil(t, func() bool { return s != nil })
		return *s
	}
	if s := signedIn(); s != (SignIn{Known: true}) {
		t.Fatalf("sign-in %+v before, want signed out", s)
	}
	var results []SignInResult
	record := func(r SignInResult) { results = append(results, r) }
	l.SignIn(record)
	if !l.SigningIn() || changes != 1 {
		t.Errorf("signing in %v, %d changes after SignIn", l.SigningIn(), changes)
	}
	loop.runUntil(t, func() bool { return len(results) == 1 })
	if results[0] != (SignInResult{Outcome: SignInDone}) || l.SigningIn() || changes != 2 {
		t.Errorf("result %+v, signing in %v, %d changes", results[0], l.SigningIn(), changes)
	}
	// No Refresh by the caller: the answer kept from before is gone.
	if s := signedIn(); s != (SignIn{Known: true, SignedIn: true}) {
		t.Errorf("sign-in %+v after, want signed in", s)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "calls"))); got != "auth login" {
		t.Errorf("calls %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(dir, "cwd"))); got != dir {
		t.Errorf("cwd %q, want the private directory %q", got, dir)
	}

	// A bad end: stderr's first line, else the status.
	write("login.sh", `echo 'Login failed: no' >&2; exit 3`)
	l.SignIn(record)
	loop.runUntil(t, func() bool { return len(results) == 2 })
	if results[1] != (SignInResult{Outcome: SignInFailed, Reason: "Login failed: no"}) {
		t.Errorf("result %+v", results[1])
	}
	write("login.sh", `exit 4`)
	l.SignIn(record)
	loop.runUntil(t, func() bool { return len(results) == 3 })
	if results[2] != (SignInResult{Outcome: SignInFailed, Reason: "claude exited with status 4"}) {
		t.Errorf("result %+v", results[2])
	}

	// Cancelled; then one that another takes the place of.
	write("login.sh", `exec sleep 30`)
	cancel := l.SignIn(record)
	loop.settle(t, 100*time.Millisecond)
	cancel()
	cancel()
	loop.runUntil(t, func() bool { return len(results) == 4 })
	if results[3].Outcome != SignInCancelled || l.SigningIn() {
		t.Errorf("result %+v, signing in %v", results[3], l.SigningIn())
	}
	stale := l.SignIn(record)
	loop.settle(t, 100*time.Millisecond)
	write("login.sh", `exit 0`)
	l.SignIn(record)
	loop.runUntil(t, func() bool { return len(results) == 6 })
	if results[4].Outcome != SignInCancelled || results[5].Outcome != SignInDone {
		t.Errorf("results %+v", results[4:])
	}
	// The cancel of a sign-in that is over ends no other.
	write("login.sh", `exec sleep 30`)
	l.SignIn(record)
	stale()
	if !l.SigningIn() {
		t.Error("a stale cancel ended the sign-in under way")
	}
	// Out of time.
	l.signInTimeout = 200 * time.Millisecond
	l.SignIn(record)
	loop.runUntil(t, func() bool { return len(results) == 8 })
	if results[6].Outcome != SignInCancelled || results[7].Outcome != SignInTimedOut {
		t.Errorf("results %+v", results[6:])
	}
	remove()
	before := changes
	l.SignIn(record)
	l.CancelSignIn()
	loop.runUntil(t, func() bool { return len(results) == 9 })
	if changes != before {
		t.Errorf("a removed watcher was called")
	}

	// No Claude Code at all.
	if err := os.Remove(exe); err != nil {
		t.Fatal(err)
	}
	l.SignIn(record)
	loop.runUntil(t, func() bool { return len(results) == 10 })
	if results[9].Outcome != SignInNotFound || l.SigningIn() {
		t.Errorf("result %+v", results[9])
	}
}
