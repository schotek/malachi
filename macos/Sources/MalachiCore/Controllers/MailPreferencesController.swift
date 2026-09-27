// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import os

/// The Mail group of the settings (ui/internal/window/preferences.go
/// `bindMail`) without the widgets: the daemon owns these preferences, so
/// the group is loaded with config.get and every change goes back with
/// config.set as a read-modify-write of the whole set.
///
/// The group is insensitive until the load succeeds and while a save is in
/// flight; a failed load puts the error into the group's description, a
/// failed save shows a toast and reverts the pop-ups to the last state the
/// daemon confirmed. The pane renders from `onPreferences` (nil until the
/// daemon answered) and `onEnabled`, and maps values onto pop-up positions
/// with `MailSelection`. Keep Attachments Offline For and Compress Stored
/// Mail exist only when the daemon reports them: an older daemon's rows are
/// hidden (`MailSelection` nil) and their setters do nothing, so what goes
/// back leaves the fields out, which config.set reads as unchanged.
@MainActor
public final class MailPreferencesController {
    /// The pop-up positions of a preference set, in the order of the
    /// StringLists of preferences.blp (`intervalChoices`, `remoteChoices`,
    /// `retentionChoices`).
    public struct MailSelection: Equatable, Sendable {
        public var interval: Int
        public var remoteContent: Int
        public var retention: Int
        /// The position in `attachmentChoices`; nil hides the row.
        public var attachments: Int?
        /// The switch; nil hides the row.
        public var compress: Bool?

        public init(interval: Int, remoteContent: Int, retention: Int, attachments: Int? = nil, compress: Bool? = nil) {
            self.interval = interval
            self.remoteContent = remoteContent
            self.retention = retention
            self.attachments = attachments
            self.compress = compress
        }

        /// The nearest positions of `p` (preferences.go `apply`).
        public init(_ p: Preferences) {
            interval = nearestInterval(p.syncIntervalSeconds)
            remoteContent = indexOfPolicy(p.remoteContent)
            retention = indexOfRetention(p.offlineDays)
            attachments = p.attachmentOfflineDays.map(indexOfAttachmentDays)
            compress = p.compressStore
        }
    }

    /// The last preference set the daemon confirmed; nil until config.get
    /// answered.
    public private(set) var preferences: Preferences?
    /// The group's sensitivity: loaded and no call in flight.
    public private(set) var isEnabled = false
    /// The window closed: late replies are dropped.
    public private(set) var closed = false

    /// Called with the values to render: after the load, after every save
    /// (the daemon's echo, which may differ from what was sent) and on a
    /// failed save (the previous values again, so the pop-ups revert).
    public var onPreferences: (@MainActor (Preferences?) -> Void)?
    /// Called on every change of the group's sensitivity.
    public var onEnabled: (@MainActor (Bool) -> Void)?
    /// Called with the text that replaces the group's description
    /// ("Stored by the mail daemon.") when the load fails.
    public var onDescription: (@MainActor (String) -> Void)?
    /// Called with the text of a toast (a failed save).
    public var onToast: (@MainActor (String) -> Void)?
    /// Called after the daemon confirmed a change (the disk space is
    /// measured again then; preferences.go `refreshStorage`).
    public var onSaved: (@MainActor () -> Void)?

    private let client: RPCClient
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "preferences")
    /// Bumped by every load and save; a reply of an older call is dropped
    /// so that it cannot overwrite a newer one (the GTK dialog cannot get
    /// there because the group is insensitive during a call; the
    /// controller is callable regardless).
    private var op = 0

    public init(client: RPCClient) {
        self.client = client
    }

    /// Drops every reply still in flight; nothing is emitted afterwards.
    public func close() {
        closed = true
    }

    // MARK: Loading

    /// Runs config.get. On success the values are rendered and the group
    /// enabled; on failure the error goes into the group's description and
    /// the group stays insensitive (preferences.go `bindMail`).
    public func load() {
        guard !closed else { return }
        op += 1
        let my = op
        let client = client
        Task { [weak self] in
            let outcome: Result<ConfigGetResult, any Error>
            do {
                outcome = .success(try await client.call(API.ConfigGet.self, EmptyParams()))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, my == self.op else { return }
            switch outcome {
            case .failure(let err):
                self.log.warning("config.get: \(String(describing: err), privacy: .public)")
                self.onDescription?(rpcErrorText(L10n.T("Loading mail settings"), err))
            case .success(let res):
                self.preferences = res.preferences
                self.onPreferences?(res.preferences)
                self.setEnabled(true)
            }
        }
    }

    // MARK: Changes

    /// Check for New Mail, in seconds (0 = manually).
    public func set(checkInterval seconds: Int) {
        guard var want = preferences else { return }
        want.syncIntervalSeconds = seconds
        save(want)
    }

    /// Load Remote Images.
    public func set(remoteContent policy: RemoteContentPolicy) {
        guard var want = preferences else { return }
        want.remoteContent = policy
        save(want)
    }

    /// Keep Mail Offline For, in days (0 = everything).
    public func set(offlineDays days: Int) {
        guard var want = preferences else { return }
        want.offlineDays = days
        save(want)
    }

    /// Compress Stored Mail; nothing while the daemon does not report it.
    public func set(compressStore on: Bool) {
        guard var want = preferences, want.compressStore != nil else { return }
        want.compressStore = on
        save(want)
    }

    /// Keep Attachments Offline For, in days (0 = everything, -1 = small
    /// attachments only); nothing while the daemon does not report it.
    public func set(attachmentOfflineDays days: Int) {
        guard var want = preferences, want.attachmentOfflineDays != nil else { return }
        want.attachmentOfflineDays = days
        save(want)
    }

    /// The pop-up positions, for the pane: a position outside the table is
    /// ignored (preferences.go `save`).
    public func selectInterval(at i: Int) {
        guard intervalChoices.indices.contains(i) else { return }
        set(checkInterval: intervalChoices[i])
    }

    public func selectRemoteContent(at i: Int) {
        guard remoteChoices.indices.contains(i) else { return }
        set(remoteContent: remoteChoices[i])
    }

    public func selectRetention(at i: Int) {
        guard retentionChoices.indices.contains(i) else { return }
        set(offlineDays: retentionChoices[i])
    }

    public func selectAttachmentDays(at i: Int) {
        guard attachmentChoices.indices.contains(i) else { return }
        set(attachmentOfflineDays: attachmentChoices[i])
    }

    /// Runs config.set with the whole set. The group is insensitive
    /// meanwhile; the daemon's echo is what the pop-ups show afterwards,
    /// or the previous values again when the call failed.
    private func save(_ want: Preferences) {
        guard !closed else { return }
        op += 1
        let my = op
        setEnabled(false)
        let client = client
        Task { [weak self] in
            let outcome: Result<ConfigSetResult, any Error>
            do {
                outcome = .success(try await client.call(API.ConfigSet.self, ConfigSetParams(preferences: want)))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.closed, my == self.op else { return }
            self.setEnabled(true)
            switch outcome {
            case .failure(let err):
                self.log.warning("config.set: \(String(describing: err), privacy: .public)")
                self.onToast?(rpcErrorText(L10n.T("Saving mail settings"), err))
                self.onPreferences?(self.preferences)
            case .success(let res):
                self.preferences = res.preferences
                self.onPreferences?(res.preferences)
                self.onSaved?()
            }
        }
    }

    private func setEnabled(_ on: Bool) {
        guard on != isEnabled else { return }
        isEnabled = on
        onEnabled?(on)
    }
}
