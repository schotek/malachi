<p align="center">
  <img src="data/icons/malachi_icon.png" width="160" alt="Malachi Mail icon: a winged messenger carrying an envelope">
</p>

<h1 align="center">Malachi Mail</h1>

<p align="center">
  A fast, native mail app for Linux, macOS and Windows.<br>
  Your mail stays on your computer, with no account at us and no servers of ours in between.
</p>

<p align="center">
  <a href="https://malachimail.app/download.html"><b>Download</b></a> ·
  <a href="https://malachimail.app/features.html">Features</a> ·
  <a href="https://malachimail.app/news.html">What's new</a> ·
  <a href="https://malachimail.app/#report">Report a problem</a> ·
  <a href="README.cs.md">Česky</a>
</p>

<p align="center">
  <a href="https://malachimail.app/assets/screenshots/macos/en/inbox.webp"><img src="https://malachimail.app/assets/screenshots/macos/en/inbox.webp" width="820" alt="Malachi Mail on macOS: the sidebar with every account, the message list grouped by conversation and the message itself"></a>
</p>

## What it does

- **Every account you have.** IMAP/SMTP with a setup assistant that finds
  the server, Microsoft 365 and Outlook.com signed in in your browser,
  Gmail, and GNOME Online Accounts on Linux.
- **Mail that works offline.** Folders are kept on your computer, search
  works without a connection, and changes go back to the server when it
  is reachable.
- **HTML mail, safely.** Messages are cleaned before they are shown, with
  no scripts and no remote images until you allow them.
- **Conversations as a chat**, a quick-reply bar, All Inboxes, rules,
  newsletters you can leave with one click, and S/MIME and OpenPGP
  signatures that are verified.
- **For work:** a calendar (Microsoft 365 and CalDAV) with invitations,
  Jira issues read like mail, saving mail to eWay-CRM, and a board that
  sorts what waits for you and what waits for others.
- **An assistant on your own account** (Claude or ChatGPT) that summarises,
  finds tasks and drafts replies, and only when you ask.
- English and Czech.

The full tour is on [malachimail.app](https://malachimail.app/features.html).

## Download

The ready-made downloads are on
[malachimail.app](https://malachimail.app/download.html) and on the
[releases page](https://github.com/schotek/malachi/releases):

| System | Download |
|---|---|
| macOS 14 or newer, Apple silicon and Intel | disk image, signed and notarised by Apple |
| Windows 11, x64 and ARM64 | installer, no administrator rights needed |
| Linux, x64 and arm64 | Flatpak, or a `.deb` / `.rpm` for a distribution with libadwaita 1.7 or newer |

The download page says which Linux package to take, what to do about the
SmartScreen warning on Windows and lists the checksums of every file.

## Reporting a problem

Use the form on [malachimail.app](https://malachimail.app/#report) (the
app opens it under *Report an Issue*), or open an
[issue](https://github.com/schotek/malachi/issues) here. Please report a
security problem through the form rather than a public issue.

## About this repository

Each commit here is the source of one release, exactly the files needed
to build it, and the packages on the releases page are built from it by
the workflows in `.github/workflows`. Development itself happens
elsewhere, so pull requests are not accepted here.

### Building from source

The source of a release builds on the system it is meant for:

- **Linux**: Go 1.25, a C compiler, GTK 4, libadwaita 1.7 or newer,
  WebKitGTK 6.0, Blueprint and gsound (optional). Then `make build`;
  the app is `build/malachi` and it starts its daemon `build/malachid`
  itself. `make deb`, `make rpm` and `make flatpak` build the packages.
  The first build compiles the GTK bindings and takes tens of minutes.
- **macOS**: macOS 14, Xcode with Swift 6 and Go. Then `make macos`;
  the app is `build/Malachi Mail.app`, signed ad hoc for the Mac it was
  built on.
- **Windows**: Windows 11, the .NET 10 SDK, the MSVC build tools (Visual
  Studio or its Build Tools), Go and Git for Windows. Then
  `windows\build.ps1 app` from a short path (the XAML compiler has no
  long-path support); the app is in `build\windows\<arch>\Malachi Mail\`.

## Licence

The core (`backend/`: the `malachid` daemon and its API) is
[AGPL-3.0-only](backend/LICENSE); the apps for Linux, macOS and Windows
and everything else are [GPL-3.0-or-later](LICENSE). The details,
including the permission for the Microsoft components the Windows app
carries, are in [LICENSING.md](LICENSING.md); the notices of third
parties in [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md).

---

*Malachi* is the prophet Malachi (Hebrew *malʼākî*, "my messenger").
