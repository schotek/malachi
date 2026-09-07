<p align="center">
  <img src="docs/malachi_icon.png" width="160" alt="Malachi Mail icon: a winged messenger carrying an envelope">
</p>

<h1 align="center">Malachi Mail</h1>

<p align="center">A native email client for the Linux desktop.</p>

Built because the existing options are either showing their age or do not
work reliably anymore.

> **Project status: early, usable with care.** Version 0.1.0 reads, writes
> and sends mail over IMAP/SMTP and Microsoft 365, renders HTML after
> sanitising it, and keeps mail available offline; Gmail support landed
> on `main` since. Search and conversation threading are not there yet.
>
> It is young, and a bug in the sync engine can still touch messages on the
> server. Keep a second mail program for anything that matters.

## What works today

- **Accounts.** IMAP/SMTP with a setup assistant that finds the server
  settings for most providers; Microsoft 365 / Outlook.com and Gmail /
  Google Workspace through GNOME Online Accounts, with no separate
  sign-in. Passwords and tokens live in the system keyring.
- **Reading offline.** Folders and messages are synchronised into a local
  store within a configurable retention window; new mail arrives as the
  server announces it (IMAP IDLE). Flags, moves and deletions are queued
  locally and pushed back.
- **HTML mail, safely.** Bodies are sanitised in the daemon before the UI
  sees them. Remote images stay blocked until you load them or trust the
  sender; the renderer runs with JavaScript off, a strict content policy
  and no network. Attached messages open read-only in their own window.
- **Writing.** Formatted text, attachments and inline images. A message
  that cannot go out right away waits in a local Outbox and is retried; a
  copy is filed in Sent. Recipients are completed from the system address
  books (Evolution Data Server) and from people you have written to.
- **Desktop integration.** `mailto:` links, new-mail notifications with an
  optional sound, launch at login (through the Background portal), light
  and dark styles, a message list with unread/flagged filters, a foldable
  sidebar with favourite folders.
- **Czech translation**, and the machinery to add more.

Not yet: search and conversation threading. The RPC contract already
defines both; the daemon answers `notImplemented`.

## Goals

- **Linux first, and only.** GNOME desktop conventions, Flatpak
  distribution, portals for everything that leaves the sandbox.
- **Native GTK 4 / libadwaita UI.** No web technology for the chrome.
- **Safe HTML mail.** Messages are sanitised in the backend before the UI
  ever sees them; remote content is blocked until you allow it; the
  renderer runs with JavaScript off and a strict content policy.
- **Separated backend.** All mail logic lives in a daemon with a documented
  JSON-RPC API. The UI is replaceable; the security model is not
  re-implemented per UI.
- **Offline first.** Mail lives in a local SQLite store; full-text search
  will run over it. The network is an optimisation.

## Non-goals

- A webmail or a hosted service.
- Mobile versions.
- Windows or macOS in the foreseeable future. The architecture does not
  prevent it, but no code will be written for it. What a macOS port would
  actually cost is written down in
  [docs/macos-port.md](docs/macos-port.md) so the question need not be
  re-researched.
- Running a mail server, filtering spam server-side, calendaring.

## Architecture

Malachi Mail is two processes. `malachid` is a Go daemon that owns the mail
store, speaks IMAP and SMTP (and Microsoft Graph for Microsoft 365),
synchronises, sanitises HTML and manages credentials; threading and search
will live there too. `malachi` is a GTK 4 application that connects to the
daemon over a local unix socket and displays what it is given.

```
┌──────────────────┐     JSON-RPC 2.0      ┌────────────────────┐
│  malachi (GTK4)  │ ◄── unix socket ────► │  malachid (Go)     │
│  displays, asks  │                       │  all the logic     │
└──────────────────┘                       └─────────┬──────────┘
                                                     │
                                              ┌──────┴──────┐
                                              │ SQLite/FTS5 │
                                              └─────────────┘
```

The boundary between them is deliberately hard: it is a socket, not a
package import. Anything the UI needs has to be added to the documented API,
which keeps business logic and security decisions in one place and makes a
second UI (or a command-line tool) a realistic option later.

Details: [docs/architecture.md](docs/architecture.md), the RPC contract in
[docs/api.md](docs/api.md), the threat model in
[docs/security.md](docs/security.md), and how versions and releases work in
[docs/releasing.md](docs/releasing.md).

## Installing

There is no Flathub listing yet. CI builds a Flatpak bundle for **x86_64**
and **aarch64** on every push to `main`; pick one up from the run's
artifacts under [Actions](https://github.com/schotek/malachi/actions)
(kept 30 days) and install it:

```sh
flatpak install --user ./malachi-<version>-x86_64.flatpak
```

Tagged versions will have their bundles attached to the GitHub release.
The bundle pulls the GNOME 48 runtime from Flathub.

## Building from source

### Dependencies

Go 1.25, a C compiler (gotk4 uses cgo), GTK 4, libadwaita, Blueprint,
WebKitGTK **6.0** (the GTK 4 flavour; it renders messages and powers the
rich-text compose editor; Blueprint also needs its typelib at build time)
and gsound for the new-mail sound (optional: without it `make` builds with
`-tags nosound` and prints a warning).

Fedora:

```sh
sudo dnf install golang gcc pkgconf-pkg-config git \
    gtk4-devel libadwaita-devel webkitgtk6.0-devel gsound-devel \
    gobject-introspection-devel sqlite-devel blueprint-compiler
```

Debian / Ubuntu (Debian 13 "trixie", Ubuntu 24.04 or newer):

```sh
sudo apt install golang-go gcc pkg-config git \
    libgtk-4-dev libadwaita-1-dev libwebkitgtk-6.0-dev gir1.2-webkit-6.0 libgsound-dev \
    libgirepository1.0-dev libsqlite3-dev blueprint-compiler
```

### Build and run

```sh
git clone https://github.com/schotek/malachi.git
cd malachi
make build          # backend → build/malachid, UI → build/malachi
make run-dev        # starts the daemon, waits for its socket, starts the UI
make run-backend    # only the daemon, in the foreground (Ctrl+C stops it)
make run-frontend   # the UI; it starts build/malachid itself unless one is already running
```

The UI runs the daemon: it looks for `malachid` beside its own executable
(then on `PATH`), starts it when nothing answers on the socket and stops it
when the application quits. A daemon started by other means (`make
run-backend`, a debugger) is used and left alone. `MALACHI_DAEMON=none`
turns the automatic start off, and `MALACHI_DAEMON=/path/to/malachid`
picks a different binary.

The first build compiles the gotk4 and WebKitGTK bindings, which takes a
long time (tens of minutes on a laptop) and a few gigabytes of build cache.
It is not stuck. Subsequent builds are fast.

Other targets: `make test`, `make lint`, `make clean`, `make help`.

Set `MALACHI_LOG_LEVEL=debug` to see every RPC call. Passwords go to the
system keyring over D-Bus; in a container without a Secret Service set
`MALACHI_KEYRING=none` to get a clean `keyringError` instead of a timeout
(accounts without a stored password still work).

UI preferences are stored in GSettings. `make build` compiles the schema
into `build/glib-2.0/schemas`, and `make run-dev` / `make run-frontend`
export `GSETTINGS_SCHEMA_DIR` so the uninstalled binary finds it. Running
`build/malachi` directly without that variable still works, but preferences
then live in memory and are lost on exit (a warning is logged).

"Launch at Login" asks the Background portal for autostart. Inside a
Toolbx container the portal cannot identify the application ("no AppId
detected") and refuses; test that setting on the host from the installed
desktop file or in the Flatpak.

### Flatpak

Build on the host, not inside a container (needs `flatpak-builder` and the
Flathub remote):

```sh
make flatpak        # vendors Go dependencies, builds into build/flatpak and ./repo
make flatpak-run    # runs the freshly built app from the build directory
```

The manifest is [packaging/flatpak/](packaging/flatpak/); the sandbox has
no network, so `scripts/flatpak-vendor.sh` vendors both Go modules first.
Only the keyring, notifications, GNOME Online Accounts, the Settings panel
and the address-book D-Bus names are granted; files, links and autostart go
through portals.

### Translating

The UI is translated with gettext (domain `malachi`); the daemon itself is
language-neutral and only returns codes. Blueprint files mark strings with
`_("…")`, Go code uses `i18n.T` / `i18n.N`. `make build` compiles `po/*.po`
into `build/locale`, which `make run-dev` / `make run-frontend` point the
uninstalled binary at (`MALACHI_LOCALE_DIR`); `scripts/build.sh install`
puts the `.mo` files under `share/locale`.

```sh
make po                      # refresh po/malachi.pot and merge it into every po/*.po
LANGUAGE=cs make run-dev     # try a translation
```

To add a language, append its code to `po/LINGUAS`, run
`msginit -l <code> -i po/malachi.pot -o po/<code>.po`, translate, and open a
pull request. `make lint` checks that every `.po` compiles and that the
committed template matches the sources.

### Where things go

| What | Path |
|---|---|
| Configuration | `~/.config/malachi/config.toml` |
| Mail store | `~/.local/share/malachi/store.db` |
| RPC socket | `$XDG_RUNTIME_DIR/malachi/rpc.sock`, or `~/.cache/malachi/run/rpc.sock` when the variable is unset (containers, ssh); inside Flatpak `$XDG_RUNTIME_DIR/app/io.github.schotek.Malachi/malachi/rpc.sock`. `MALACHI_SOCKET` moves it for `make run-dev` and the UI; the daemon takes `--socket` |
| Secrets | system keyring (libsecret), never on disk in the clear |

The store is not encrypted at rest. Use full-disk encryption.

Accounts are managed from *Preferences → Accounts* and stored in the mail
store. For testing, `config.toml` can seed an account; it is imported once,
at the next daemon start, and never written back:

```toml
[[accounts]]
name = "Work"
email = "me@example.org"

[accounts.imap]
host = "imap.example.org"
port = 993
security = "tls"
username = "me@example.org"
auth_method = "password"

[accounts.smtp]
host = "smtp.example.org"
port = 587
security = "starttls"
username = "me@example.org"
auth_method = "password"
```

Passwords never go into this file; they are asked for and kept in the
keyring.

## Supported providers

| Provider | Status |
|---|---|
| Generic IMAP/SMTP with password | ✅ reading and sending |
| Microsoft 365 / Outlook.com via GNOME Online Accounts (Microsoft Graph) | ✅ reading and sending; sign in under Settings → Online Accounts first |
| Gmail / Google Workspace via GNOME Online Accounts (IMAP/SMTP with XOAUTH2) | ✅ reading and sending (on `main`, not in 0.1.0); sign in under Settings → Online Accounts first. All Mail is the archive target and is not downloaded |
| Gmail with an app password | ✗ not offered |
| OAuth2 without GNOME Online Accounts | ✗ reserved for desktops without GOA, not implemented |

## Contributing

Read [CLAUDE.md](CLAUDE.md) (it applies to humans too: it is the list of
rules we do not bend) and [docs/security.md](docs/security.md) before
touching anything that parses or renders mail. Conventional commits, `gofmt`,
`golangci-lint`.

## Licence

The core (`backend/`, the `malachid` daemon and its API) is
[AGPL-3.0-only](backend/LICENSE) and is also available under a commercial
licence for use in proprietary clients. The reference GTK user interface and
everything else is [GPL-3.0-or-later](LICENSE). See
[LICENSING.md](LICENSING.md) for the details and
[CLA.md](CLA.md) before contributing.

---

*Malachi* is the prophet Malachi (Hebrew *malʼākî*, "my messenger").
