// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os
import UniformTypeIdentifiers

/// Opening and saving attachments (ui/internal/window/attachments.go
/// `openAttachment`, `saveAttachment`, `saveAllAttachments`, `saveInto`).
/// The chips are the reader's; this is what their Open, Save As… and Save
/// All do. Names and types are server data (the daemon sanitises the file
/// name, `fileName` is defence in depth); programs and scripts are never
/// opened directly (docs/security.md §4): judged by the name lists of
/// `executableAttachment` and, on macOS, by what the type system says the
/// name or the type conforms to (`executableByType`), before the fetch on
/// what the message lists and again after it on the name and type the
/// daemon served. The content comes through message.part, so a part over
/// `API.Limits.maxAttachmentDataBytes` is out of reach; a part kept on the
/// mail server is downloaded first, and one the daemon moved there since
/// the chip was drawn after its partNotDownloaded (`MessageCache.partData`,
/// the chips show the spinner meanwhile). Every file written
/// gets the quarantine attribute, so the system treats it like a download
/// (the plan's deviation from GTK); a file for opening is not opened
/// unless the attribute is on it.
@MainActor
final class AttachmentActions {
    let state: AppState
    let cache: MessageCache
    /// Where a part is written for opening (docs/security.md §8): a private
    /// directory, a fresh 0700 subdirectory per file, the file 0600.
    let openDir: OpenDir

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "attachments")

    init(state: AppState, cache: MessageCache, openDir: OpenDir = .default) {
        self.state = state
        self.cache = cache
        self.openDir = openDir
    }

    // MARK: Open

    /// Writes the part to a private file and hands it to the default
    /// application (attachments.go `openAttachment`): the chip menu's Open.
    /// An executable is saved instead: the chip keeps Open disabled for
    /// those already, this is the second look, and a third follows the
    /// fetch on the name and type the daemon served, which are what the
    /// file gets. `remote` (the chip showed the part on the mail server)
    /// downloads the message first.
    func open(_ a: Attachment, of s: MessageSummary, remote: Bool, from window: NSWindow?) {
        if Self.mustNotOpen(filename: a.filename, contentType: a.contentType) {
            saveAs(a, of: s, remote: remote, from: window)
            return
        }
        Task { @MainActor [weak self] in
            guard let self, let res = await self.fetchForViewing(a, of: s, remote: remote, from: window) else { return }
            let name = fileName(res, a)
            if Self.mustNotOpen(filename: name, contentType: res.contentType) {
                self.log.info("attachment \(a.partId, privacy: .public) turned out executable after the fetch; saving instead")
                // Downloaded by now; the part the daemon served.
                var served = a
                if !res.partId.isEmpty {
                    served.partId = res.partId
                }
                self.saveAs(served, of: s, remote: false, from: window)
                return
            }
            guard let url = await self.writeForViewing(name: name, data: res.data, from: window) else { return }
            do {
                _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
            } catch {
                self.log.warning("opening an attachment: \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                self.toast(L10n.T("The attachment could not be opened"), in: window)
            }
        }
    }

    /// Writes the part to a private file and shows it in Quick Look
    /// (attachments.go `previewAttachment`): a click on the chip. Quick
    /// Look only renders, so a program or script is previewed like any
    /// file; it carries the quarantine attribute all the same. `source`
    /// finds the chip of the part fetched, which the panel zooms out of,
    /// once the file is ready (the chip clicked may have been drawn again
    /// during a download).
    func preview(
        _ a: Attachment, of s: MessageSummary, remote: Bool, from window: NSWindow?,
        source: @escaping @MainActor (_ partId: String) -> NSView?
    ) {
        Task { @MainActor [weak self] in
            guard let self, let res = await self.fetchForViewing(a, of: s, remote: remote, from: window) else { return }
            let name = fileName(res, a)
            guard let url = await self.writeForViewing(name: name, data: res.data, from: window) else { return }
            AttachmentPreview.shared.show(url: url, source: source(res.partId.isEmpty ? a.partId : res.partId))
        }
    }

    /// The part through `MessageCache.partData` (attachments.go
    /// `partData`): the message downloaded first when the part is on the
    /// server (`remote`, the chip's `partState`).
    private func partData(_ a: Attachment, of s: MessageSummary, remote: Bool) async throws -> MessagePartResult {
        try await cache.partData(accountID: s.accountId, messageID: s.id, attachment: a, onServer: remote)
    }

    /// message.part for the attachment being opened or previewed; nil
    /// after a failure, which has had its toast.
    private func fetchForViewing(_ a: Attachment, of s: MessageSummary, remote: Bool, from window: NSWindow?) async -> MessagePartResult? {
        do {
            return try await partData(a, of: s, remote: remote)
        } catch {
            log.warning("message.part \(a.partId, privacy: .public): \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
            toast(rpcErrorText(L10n.T("Opening the attachment"), error), in: window)
            return nil
        }
    }

    /// Writes `data` as `name` into the open directory and quarantines it,
    /// off the main actor; nil after a failure, which has had its toast. A
    /// file whose attribute did not stick is not shown (`quarantine`).
    private func writeForViewing(name: String, data: Data, from window: NSWindow?) async -> URL? {
        let dir = openDir
        do {
            return try await Task.detached(priority: .userInitiated) {
                let written = try dir.write(name: name, data: data)
                try quarantine(written)
                return written
            }.value
        } catch {
            // The error may name the file, hence the attachment: private.
            log.warning("writing an attachment for opening: \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
            toast(L10n.T("The attachment could not be opened"), in: window)
            return nil
        }
    }

    /// Whether the file must go through Save As… rather than the default
    /// application: the name lists (`executableAttachment`), or a type the
    /// system says is run rather than shown (`executableByType`).
    static func mustNotOpen(filename: String, contentType: String) -> Bool {
        executableAttachment(filename: filename, contentType: contentType)
            || executableByType(filename: filename, contentType: contentType)
    }

    /// The types macOS runs, installs or loads rather than displays, by
    /// conformance: an executable of any kind, a script, an application or
    /// its bundle, a package or preference pane or plug-in bundle.
    private static let executableTypes: [UTType] = [
        .executable, .script, .shellScript, .application, .applicationBundle, .unixExecutable,
        .package, .systemPreferencesPane, .pluginBundle,
    ]

    /// Whether any type the attachment claims (`claimedTypes`: the media
    /// type or the extension) conforms to one of `executableTypes`.
    static func executableByType(filename: String, contentType: String) -> Bool {
        claimedTypes(filename: filename, contentType: contentType).contains { claimed in
            executableTypes.contains { claimed.conforms(to: $0) }
        }
    }

    /// An error reduced to what a log line may carry in the open: an RPC
    /// error's code, anything else its domain and code. The description
    /// (a Cocoa error names the file, hence the attachment) is logged as
    /// private beside it.
    private static func errorText(_ error: any Error) -> String {
        if let rpc = error as? RPCError {
            return "rpc \(rpc.code.rawValue)"
        }
        let ns = error as NSError
        return "\(ns.domain) \(ns.code)"
    }

    // MARK: Save As

    /// Asks where to put the part, then fetches and writes it
    /// (attachments.go `saveAttachment`), downloading the message first
    /// when `remote`. The panel already confirmed an overwrite, so the
    /// write replaces; only a failure gets a toast, a dismissal nothing.
    func saveAs(_ a: Attachment, of s: MessageSummary, remote: Bool, from window: NSWindow?) {
        let panel = NSSavePanel()
        panel.title = L10n.T("Save Attachment")
        panel.message = L10n.T("Save Attachment")
        panel.nameFieldStringValue = fileName(nil, a)
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false
        Task { @MainActor [weak self] in
            guard let self else { return }
            let response = await self.run(panel, on: window)
            guard response == .OK, let url = panel.url else {
                return // dismissed
            }
            do {
                let res = try await self.partData(a, of: s, remote: remote)
                let data = res.data
                try await Task.detached(priority: .userInitiated) {
                    try data.write(to: url, options: .atomic)
                    try? quarantine(url) // the user's file either way
                }.value
            } catch {
                self.log.warning("saving an attachment \(a.partId, privacy: .public): \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                self.toast(rpcErrorText(L10n.T("Saving the attachment"), error), in: window)
            }
        }
    }

    // MARK: Save All

    /// Asks for a folder and writes every attachment into it, one
    /// message.part at a time, never overwriting: a name that exists gets
    /// " (2)" and so on. When some are on the mail server (`remote`,
    /// `anyRemote`) the message is downloaded once first; if that fails,
    /// nothing is written and the toast says why. One toast sums it up.
    /// The Save All buttons of the message stay disabled while it runs
    /// (`MessageCache.beginSaveAll`: the chips may be drawn again
    /// meanwhile); a second one for the same message, started while the
    /// first runs, ends at its folder panel (attachments.go
    /// `saveAllAttachments`).
    func saveAll(_ atts: [Attachment], of s: MessageSummary, remote: Bool, from window: NSWindow?) {
        let panel = NSOpenPanel()
        panel.title = L10n.T("Save Attachments")
        panel.message = L10n.T("Save Attachments")
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = mn(L10n.T("_Save"))
        let cache = cache
        Task { @MainActor [weak self] in
            guard let self else { return }
            let response = await self.run(panel, on: window)
            guard response == .OK, let folder = panel.url, cache.beginSaveAll(s.id) else {
                return // dismissed, or another Save All of the message runs
            }
            defer { cache.endSaveAll(s.id) }
            var downloaded: Message?
            if remote {
                do {
                    downloaded = try await cache.download(accountID: s.accountId, messageID: s.id)
                } catch {
                    self.log.warning("message.download for Save All: \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                    self.toast(rpcErrorText(L10n.T("Saving the attachments"), error), in: window)
                    return
                }
            }
            var saved = 0
            var failed = 0
            for a in atts {
                do {
                    // Microsoft 365 may have moved the part ids; a part the
                    // downloaded message no longer lists is not fetched.
                    guard let part = partAfterDownload(a, downloaded) else {
                        throw partNotFoundAfterDownload
                    }
                    try await self.saveInto(folder, s, part)
                    saved += 1
                } catch {
                    self.log.warning("saving an attachment \(a.partId, privacy: .public): \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                    failed += 1
                }
            }
            self.toast(saveAllSummary(saved: saved, failed: failed), in: window)
        }
    }

    /// Fetches `a` and creates it in `folder` under a name that is not
    /// taken yet (attachments.go `saveInto`). The message was downloaded
    /// already when it had to be; a part the daemon answers
    /// partNotDownloaded for after all gets its one download and retry.
    private func saveInto(_ folder: URL, _ s: MessageSummary, _ a: Attachment) async throws {
        let res = try await cache.partData(accountID: s.accountId, messageID: s.id, attachment: a, onServer: false)
        let name = fileName(res, a)
        let data = res.data
        try await Task.detached(priority: .userInitiated) {
            try writeUnique(data, named: name, into: folder)
        }.value
    }

    // MARK: Helpers

    /// A sheet on `window`, or a stand-alone panel without one (or when
    /// the window already carries a sheet, where a second one would queue
    /// up invisibly).
    private func run(_ panel: NSSavePanel, on window: NSWindow?) async -> NSApplication.ModalResponse {
        if let window, window.isVisible, window.attachedSheet == nil {
            return await panel.beginSheetModal(for: window)
        }
        return await panel.begin()
    }

    /// A toast in the window where the click was, when that window has an
    /// overlay of its own; otherwise wherever toasts go now (attachments.go
    /// `say`).
    private func toast(_ text: String, in window: NSWindow?) {
        windowToast(text, in: window, or: state.toasts)
    }
}

/// Creates `data` in `folder` as `name`, or as `name` with " (2)", " (3)", …
/// when that is taken (attachments.go `saveInto`): the create fails on an
/// existing file, and a name that appeared between the check and the create
/// is tried again, a few times. Off the main actor.
private func writeUnique(_ data: Data, named name: String, into folder: URL) throws {
    let fm = FileManager.default
    var lastError: any Error = CocoaError(.fileWriteFileExists)
    for _ in 0..<8 {
        let candidate = uniqueName(name) { fm.fileExists(atPath: folder.appendingPathComponent($0).path) }
        let url = folder.appendingPathComponent(candidate)
        do {
            try data.write(to: url, options: .withoutOverwriting)
            try? quarantine(url) // the user's file either way
            return
        } catch let error as CocoaError where error.code == .fileWriteFileExists {
            lastError = error
        }
    }
    throw lastError
}

/// Marks a file the user got out of a message as such: the quarantine
/// attribute of a mail attachment, so Gatekeeper and the default
/// application treat it like any download (the plan's deviation from the
/// GTK UI, which has no such attribute). The attribute is read back, and
/// a file it is not on afterwards is an error: a file written for opening
/// is then not opened, a file the user saved is theirs regardless (the
/// file system refused the attribute, the file is where they asked).
private func quarantine(_ url: URL) throws {
    var values = URLResourceValues()
    values.quarantineProperties = [
        kLSQuarantineTypeKey as String: kLSQuarantineTypeEmailAttachment as String,
        kLSQuarantineAgentNameKey as String: "Malachi Mail",
        kLSQuarantineAgentBundleIdentifierKey as String: "io.github.schotek.Malachi",
    ]
    var target = url
    try target.setResourceValues(values)
    let readBack = try url.resourceValues(forKeys: [.quarantinePropertiesKey]).quarantineProperties
    guard let readBack, !readBack.isEmpty else {
        throw CocoaError(.fileWriteUnknown, userInfo: [NSLocalizedDescriptionKey: "the quarantine attribute did not stick"])
    }
}
