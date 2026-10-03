# GTK inbox sections: verification handoff

The implementation ports the macOS inbox sections to GTK: Flagged first,
then collapsible local-calendar date sections. Expanded conversations stay
with their parent; search and other folders keep their existing listing.
Selection and activation resolve message keys instead of physical GTK row
indices. Flag changes, incoming mail, paging, removals and rollback update
sections. Collapsing a selected message's section clears its selection.
The shared Czech translations now come from GTK sources in `po/POTFILES`;
the temporary Swift extraction list has been removed.

## Verification completed

- `go test ./ui/internal/maildate` on macOS: passed.
- Fedora 42 / arm64 Docker: Blueprint compilation, `make po`, and
  `make locale` passed.
- Fedora 42 Docker: `go test -p 1 ./internal/maildate ./internal/window`
  from `ui/` passed, including compilation of the GTK implementation.
- macOS string checker for the two inbox section source files: passed.
- GTK widget test under Xvfb: **failed** at
  `date_groups_test.go:82`, `selection lost when unflagging`.

After that failure, `syncDateRows` was changed to call `UnselectAll` with
selection signals suppressed before `RemoveAll`, then restore selection
by key. This is intended to address stale GTK selection state when reusing a removed
row. **That follow-up fix has not been verified.** The user requested
stopping local verification and handing the work over via a local commit.

The full application build was scheduled after the widget test and was
therefore **not reached**. Manual testing is also outstanding. No commit
or push beyond the requested local handoff is authorized.

## Next steps

Run on Linux with the repository's GTK 4.18 / libadwaita 1.7 / WebKitGTK 6.0
build dependencies. Native Windows cannot compile this GTK client; an
agent on Windows needs a Linux environment (for example WSL or Docker).
No Windows UI port is part of this change.

```sh
make blueprint schemas po locale
cd ui
go test ./internal/maildate ./internal/window
MALACHI_GTK_TEST=1 dbus-run-session -- xvfb-run -a \
  go test ./internal/window -run TestDateGroupsGTK -count=1 -v
go vet ./internal/maildate ./internal/window
cd ..
make ui
```

The widget test uses synthetic summaries and does not connect to a daemon
or a mailbox. Its remaining assertions cover collapse/expand, paging,
removal rollback, search, and expanded flagged conversations. Investigate
and fix any failures before treating the feature as verified.

Then manually check Czech headers, flag/unflag with a selected message,
selection and message actions after reordering, section collapse with a
selected message, conversation expansion, paging, switching folders, and
search. Confirm selection stays correct and headers never act as messages.

The first Docker build was killed with exit 137 while compiling large GTK
bindings. The second used serial compilation and these temporary settings:
`GOMAXPROCS=2 GOGC=20 GOMEMLIMIT=1500MiB CGO_CFLAGS='-O0 -g0'
CGO_CXXFLAGS='-O0 -g0'`, with `go test -p 1`. They are verification-only
settings, not changes to the project's build configuration.
