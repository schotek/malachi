// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// How long the client waits for each kind of call, as the GTK UI does
/// (ui/internal/window, ui/internal/compose, ui/internal/accountwizard).
/// Every `API` method carries one of these as its default; a caller may
/// pass another explicitly.
public enum RPCTimeouts {
    /// Everything not named below.
    public static let `default`: Duration = .seconds(5)
    /// `system.info`: the health check must answer at once.
    public static let systemInfo: Duration = .seconds(3)
    /// The connection handshake, `system.hello` and `system.authenticate`
    /// together (api.HandshakeTimeout); the daemon allows 10 s from accept.
    public static let handshake: Duration = .seconds(5)
    /// `message.part`, `attachment.get`: payloads up to 16 MiB.
    public static let part: Duration = .seconds(60)
    /// `message.body` (any call the policy may resolve to `allow`),
    /// `message.embedded`: the daemon may fetch images for up to 10 s.
    public static let remote: Duration = .seconds(30)
    /// `draft.create`: quoting copies parts into the attachment store.
    public static let compose: Duration = .seconds(30)
    /// `account.discover`: ISPDB, autoconfig, DNS and guesses, 20 s inside.
    public static let discover: Duration = .seconds(15)
    /// `account.test`: 10 s to connect and 20 s per endpoint inside.
    public static let test: Duration = .seconds(45)
    /// `account.add`, `account.update`: the keyring may prompt.
    public static let save: Duration = .seconds(30)
    /// `account.oauthStart`: the daemon opens a listener and builds the URL.
    public static let oauthStart: Duration = .seconds(10)
    /// One `account.oauthWait` call: the daemon blocks up to 60 s before it
    /// answers `pending`.
    public static let oauthWaitCall: Duration = .seconds(75)
}
