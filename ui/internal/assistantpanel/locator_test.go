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
