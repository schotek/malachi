# Malachi Mail

A native email client for the Linux desktop, built because the existing
options are either showing their age or do not work reliably anymore.

> **Project status: early. IMAP reading and plain-text sending work; there
> is no HTML rendering yet.**
>
> The daemon synchronises IMAP accounts into a local store (folders,
> headers, bodies as plain text within a configurable retention window),
> pushes flag, move and delete changes back, and the window shows real
> folders and messages. Messages are composed and sent as plain text with
> attachments: they wait in a local Outbox, go out over SMTP with retries,
> and a copy lands in the Sent folder. Messages are displayed as text only;
> HTML bodies are withheld until the sanitiser exists. Do not point it at a
> mailbox you care about yet: it is young, and a bug in the sync engine can
> still touch messages on the server.

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
- **Offline first.** Mail lives in a local SQLite store with full-text
  search; the network is an optimisation.

## Non-goals

- A webmail or a hosted service.
- Mobile versions.
- Windows or macOS in the foreseeable future. The architecture does not
  prevent it, but no code will be written for it.
- Running a mail server, filtering spam server-side, calendaring.

## Architecture

Malachi Mail is two processes. `malachid` is a Go daemon that owns the mail
store, speaks IMAP and SMTP (and Microsoft Graph for Microsoft 365),
synchronises, threads, searches, sanitises HTML and manages credentials. `malachi` is a GTK 4 application that connects to
the daemon over a local unix socket and displays what it is given.

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
[docs/api.md](docs/api.md), and the threat model in
[docs/security.md](docs/security.md).

## Building from source

### Dependencies

Go ≥ 1.22 (developed with 1.25), a C compiler (gotk4 uses cgo), GTK 4,
libadwaita, Blueprint, WebKitGTK **6.0** (the GTK 4 flavour; it powers the
rich-text compose editor and, later, message rendering; Blueprint also
needs its typelib at build time) and gsound for the new-mail sound
(optional: without it `make` builds with `-tags nosound` and prints a
warning). The first build compiles the gotk4 and WebKitGTK bindings, which
takes a long time.

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
make run-frontend   # only the UI; shows a banner until a daemon is reachable
```

The first build compiles the gotk4 bindings, which takes a long time (tens
of minutes on a laptop) and a few gigabytes of build cache. It is not
stuck. Subsequent builds are fast.

Other targets: `make test`, `make lint`, `make clean`, `make flatpak`
(needs `flatpak-builder` on the host; the manifest is a skeleton for now).

Set `MALACHI_LOG_LEVEL=debug` to see every RPC call. Passwords go to the
system keyring over D-Bus; in a container without a Secret Service set
`MALACHI_KEYRING=none` to get a clean `keyringError` instead of a timeout
(accounts without a stored password still work).

UI preferences are stored in GSettings. `make build` compiles the schema
into `build/glib-2.0/schemas`, and `make run-dev` / `make run-frontend`
export `GSETTINGS_SCHEMA_DIR` so the uninstalled binary finds it. Running
`build/malachi` directly without that variable still works, but preferences
then live in memory and are lost on exit (a warning is logged).

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

"Launch at Login" asks the Background portal for autostart. Inside the
Toolbx container the portal cannot identify the application ("no AppId
detected") and refuses; test that setting on the host from the installed
desktop file or in the Flatpak.

### Where things go

| What | Path |
|---|---|
| Configuration | `~/.config/malachi/config.toml` |
| Mail store | `~/.local/share/malachi/store.db` |
| RPC socket | `$XDG_RUNTIME_DIR/malachi/rpc.sock` |
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
| Generic IMAP/SMTP with password | 🚧 reading and plain-text sending work |
| Microsoft 365 / Outlook.com via GNOME Online Accounts (Microsoft Graph) | 🚧 reading and plain-text sending work; sign in under Settings → Online Accounts first |
| Gmail via OAuth2 | ⏸️ deferred: requires a Google CASA security assessment or a bring-your-own-client-ID mode; see [docs/architecture.md](docs/architecture.md) |

"Planned" means "designed for, not implemented". See the status note at the
top.

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
