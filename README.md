# Malachi Mail

A native email client for the Linux desktop, built because the existing
options are either showing their age or do not work reliably anymore.

> **Project status: early bootstrap. Nothing works yet.**
>
> There is no IMAP, no sending, no rendering. The repository contains the
> architecture, the API contract, a daemon that answers "not implemented",
> and a window with placeholder data. Do not point it at a mailbox you care
> about; there is nothing to point it at yet.

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
store, speaks IMAP and SMTP, synchronises, threads, searches, sanitises HTML
and manages credentials. `malachi` is a GTK 4 application that connects to
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
libadwaita, and Blueprint. WebKitGTK is not used yet but will be required
soon; note that the GTK 4 flavour is API version **6.0**.

Fedora:

```sh
sudo dnf install golang gcc pkgconf-pkg-config git \
    gtk4-devel libadwaita-devel webkitgtk6.0-devel \
    gobject-introspection-devel sqlite-devel blueprint-compiler
```

Debian / Ubuntu (Debian 13 "trixie", Ubuntu 24.04 or newer):

```sh
sudo apt install golang-go gcc pkg-config git \
    libgtk-4-dev libadwaita-1-dev libwebkitgtk-6.0-dev \
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

Set `MALACHI_LOG_LEVEL=debug` to see every RPC call.

UI preferences are stored in GSettings. `make build` compiles the schema
into `build/glib-2.0/schemas`, and `make run-dev` / `make run-frontend`
export `GSETTINGS_SCHEMA_DIR` so the uninstalled binary finds it. Running
`build/malachi` directly without that variable still works, but preferences
then live in memory and are lost on exit (a warning is logged).

### Where things go

| What | Path |
|---|---|
| Configuration | `~/.config/malachi/config.toml` |
| Mail store | `~/.local/share/malachi/store.db` |
| RPC socket | `$XDG_RUNTIME_DIR/malachi/rpc.sock` |
| Secrets | system keyring (libsecret), never on disk in the clear |

The store is not encrypted at rest. Use full-disk encryption.

## Supported providers

| Provider | Status |
|---|---|
| Generic IMAP/SMTP with password | ✅ planned first (phase 1) |
| Microsoft 365 / Outlook.com via OAuth2 | 🚧 planned (phase 2) |
| Gmail via OAuth2 | ⏸️ deferred: requires a Google CASA security assessment or a bring-your-own-client-ID mode; see [docs/architecture.md](docs/architecture.md) |

"Planned" means "designed for, not implemented". See the status note at the
top.

## Contributing

Read [CLAUDE.md](CLAUDE.md) (it applies to humans too: it is the list of
rules we do not bend) and [docs/security.md](docs/security.md) before
touching anything that parses or renders mail. Conventional commits, `gofmt`,
`golangci-lint`.

## Licence

GPL-3.0-or-later. See [LICENSE](LICENSE).

---

*Malachi* is the prophet Malachi (Hebrew *malʼākî*, "my messenger").
