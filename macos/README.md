# Malachi Mail for macOS

The macOS client of Malachi Mail: a Swift/AppKit application over the
`malachid` JSON-RPC socket, the third client of the daemon next to the GTK
UI and the MCP bridge. It mirrors the GTK UI, which is the primary one and
the template (CLAUDE.md rule 4); nothing here changes the daemon.

**Status: skeleton.** The app starts `malachid` from its own bundle, opens one
window that shows the connection state and the daemon's `system.info`, and
carries `malachi-mcp` for AI agents. There is no mail UI yet; that comes
feature by feature.

Licence: GPL-3.0-or-later (everything outside `backend/`). Every source file
starts with the SPDX header; in `Package.swift` it sits on lines 2–3 because
line 1 must be the `swift-tools-version` comment.

## Build and run

Needs Xcode 27 (Swift 6.4) and Go 1.25 for the daemon. From the repository
root:

```sh
make macos        # builds build/malachid and build/malachi-mcp, then the Swift package,
                  # and assembles "build/Malachi Mail.app" with both Go binaries inside
make run-macos    # runs the bundled executable from the terminal: the app's and the
                  # daemon's logs stay visible, Ctrl+C reaches both
make test-macos   # swift test
open "build/Malachi Mail.app"   # the Finder way: Dock icon, About panel with the version
```

The bundle is ad-hoc signed (`codesign --sign -`), which is enough for the
machine it was built on. Another Mac would show Gatekeeper's "damaged" dialog:
Developer ID signing and notarisation are not part of this phase.

Quick iteration without the bundle:

```sh
swift build --package-path macos                       # or open macos/Package.swift in Xcode
MALACHI_DAEMON=$PWD/build/malachid swift run --package-path macos MalachiMail
```

`git describe` without a tag yields a bare hash; the bundle then carries
`0.0.0` as its short version and the hash under `MalachiVersion`.

## Layout

```
macos/
  Package.swift                 SwiftPM, tools 6.0 (strict concurrency), macOS 14+
  Makefile                      app bundle assembly, driven by the root Makefile
  Resources/Info.plist.in       template; make substitutes version and bundle id
  Sources/MalachiCore/          no AppKit, tested with swift test
    JSONRPC.swift               wire shapes, two-pass decoding
    LineFramer.swift            newline framing, 32 MiB cap
    RPCClient.swift             actor over NWConnection (the ui/internal/client of macOS)
    UnixSocketProbe.swift       non-blocking POSIX probe, 103-byte path check
    DaemonSupervisor.swift      actor over Foundation.Process (the ui/internal/daemon of macOS)
    Paths.swift                 socket, data dir, MCP bridge location
    API.swift, Version.swift
  Sources/MalachiMail/          AppKit: main.swift, AppDelegate, MainMenu, MainWindowController
  Tests/MalachiCoreTests/       framing, JSON-RPC, client round trips against an in-process
                                fake daemon, supervisor with script stand-ins
```

## How it runs the daemon

Like the GTK UI: `malachid` is looked for beside the app's executable
(`Contents/MacOS/malachid`), else in `MALACHI_DAEMON` (`none` switches the
automatic start off), else on `PATH`. If nothing answers on the socket, the
daemon is started with `--socket`, `--config` and `--store`, and the app
waits up to 15 s for the socket. A daemon that already answers (`make
run-backend`, a debugger) is used as is and never stopped. On quit the app
sends SIGTERM to the daemon it started and waits up to 15 s (the daemon
gives its syncers 10 s), then SIGKILL; the window says "Stopping malachid…"
meanwhile. A daemon that exits is restarted with the GTK UI's backoff.

The daemon is started with `MALACHI_KEYRING=none` in this phase: there is no
Secret Service on macOS and no Keychain keyring yet, so adding an account
with a password fails with `keyringError`. That is the first backend piece a
usable macOS client needs (docs/macos-port.md §2).

## Where things are

| What | Path |
|---|---|
| Configuration | `~/Library/Application Support/Malachi Mail/config.toml` |
| Mail store | `~/Library/Application Support/Malachi Mail/store.db` |
| RPC socket | `~/.cache/malachi/run/rpc.sock` (`MALACHI_SOCKET` overrides; `XDG_RUNTIME_DIR`/`XDG_CACHE_HOME` honoured) |
| MCP bridge | `Contents/MacOS/malachi-mcp` in the bundle, `build/malachi-mcp` in a checkout |

The socket keeps the daemon's own default so that `malachi-mcp`, the
repository's `.mcp.json` and `make run-backend` agree with the app without
any environment variable. macOS limits a unix socket path to 103 bytes; the
app refuses a longer one with a clear message instead of the daemon's
"invalid argument". There is no App Sandbox: with one, the data would move
into the app container and the socket with it, which would break the MCP
bridge's default.

## AI agents

While the app runs, the repository's `.mcp.json` works unchanged: Claude Code
spawns `build/malachi-mcp`, which connects to the same socket. Another MCP
client points at `Contents/MacOS/malachi-mcp` in the bundle. Tools, flags and
the security model are in [docs/mcp.md](../docs/mcp.md).

## Not yet

Mail UI, Keychain keyring, sandbox, Developer ID signing and notarisation, a
LaunchAgent for the daemon, an update mechanism. See
[docs/macos-port.md](../docs/macos-port.md) for what each takes.
