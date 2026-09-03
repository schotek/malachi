# Licensing

Malachi Mail is made of two independently licensed parts.

| Part | Path | Licence |
|---|---|---|
| Core (the `malachid` daemon and the `pkg/api` contract) | `backend/` | [AGPL-3.0-only](backend/LICENSE) |
| Reference GTK4 user interface | `ui/`, `data/`, `packaging/`, everything else | [GPL-3.0-or-later](LICENSE) |

Every source file carries an `SPDX-License-Identifier` header saying which
of the two applies to it.

## The core is dual-licensed

The core is designed to be reused: any mail client can be built on top of
`malachid` by talking JSON-RPC over a Unix socket, in any language, without
touching the daemon. We want that to be easy for open-source projects and
possible for commercial ones.

**Open source.** Under the AGPL-3.0 you may use, modify and redistribute the
core free of charge, provided that the client you build on it, and any
modifications to the daemon, are released under the AGPL-3.0 or a compatible
licence, including when the daemon is only run as a network service and
never shipped to users.

**Proprietary and commercial use.** If you want to use the core in software
whose source you do not release under AGPL-compatible terms, a commercial
licence is available. It grants the same code under terms without the
copyleft obligations. Contact the copyright holder to discuss it:

> Vladislav Janeček — vladislav.janecek@gmail.com

We consider a client written against the `malachid` API (whether it imports
`pkg/api` or reimplements the protocol from `docs/api.md`) to be a work based
on the core for the purposes of the AGPL. If you disagree, or your case is
unclear, ask; the commercial licence exists precisely so that nobody has to
argue about this.

## The user interface is GPL-only

The reference UI depends on [gotk4](https://github.com/diamondburned/gotk4),
which is AGPL-3.0, so it cannot be offered under any other terms. The
commercial licence covers the core only. Anyone building a proprietary
client needs to write their own UI, which is exactly what the process
boundary is for.

## Contributions

To keep the dual-licensing model workable, every contributor must sign the
[Contributor License Agreement](CLA.md) before their first pull request is
merged. It lets the project relicense contributions for the commercial
offering while leaving the contributor full rights to their own work. The
CLA bot on GitHub will ask for a signature automatically.

## Not legal advice

This document summarises intent. The licence texts themselves are
authoritative.
