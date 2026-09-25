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
/// `API.Limits.maxAttachmentDataBytes` is out of reach. Every file written
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
    /// application (attachments.go `openAttachment`). An executable is
    /// saved instead: the chip routes those to Save As… already, this is
    /// the second look, and a third follows the fetch on the name and type
    /// the daemon served, which are what the file gets.
    func open(_ a: Attachment, of s: MessageSummary, from window: NSWindow?) {
        if Self.mustNotOpen(filename: a.filename, contentType: a.contentType) {
            saveAs(a, of: s, from: window)
            return
        }
        let cache = cache
        let dir = openDir
        Task { @MainActor [weak self] in
            let res: MessagePartResult
            do {
                res = try await cache.fetchAttachment(accountID: s.accountId, messageID: s.id, partID: a.partId)
            } catch {
                guard let self else { return }
                self.log.warning("message.part \(a.partId, privacy: .public): \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                self.toast(rpcErrorText(L10n.T("Opening the attachment"), error), in: window)
                return
            }
            guard let self else { return }
            let name = fileName(res, a)
            if Self.mustNotOpen(filename: name, contentType: res.contentType) {
                self.log.info("attachment \(a.partId, privacy: .public) turned out executable after the fetch; saving instead")
                self.saveAs(a, of: s, from: window)
                return
            }
            let data = res.data
            let url: URL
            do {
                url = try await Task.detached(priority: .userInitiated) {
                    let written = try dir.write(name: name, data: data)
                    try quarantine(written)
                    return written
                }.value
            } catch {
                // The error may name the file, hence the attachment: private.
                self.log.warning("writing an attachment for opening: \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                self.toast(L10n.T("The attachment could not be opened"), in: window)
                return
            }
            do {
                _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
            } catch {
                self.log.warning("opening an attachment: \(Self.errorText(error), privacy: .public): \(String(describing: error), privacy: .private)")
                self.toast(L10n.T("The attachment could not be opened"), in: window)
            }
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
    /// (attachments.go `saveAttachment`). The panel already confirmed an
    /// overwrite, so the write replaces; only a failure gets a toast, a
    /// dismissal nothing.
    func saveAs(_ a: Attachment, of s: MessageSummary, from window: NSWindow?) {
        let panel = NSSavePanel()
        panel.title = L10n.T("Save Attachment")
        panel.message = L10n.T("Save Attachment")
        panel.nameFieldStringValue = fileName(nil, a)
        panel.canCreateDirectories = true
        panel.isExtensionHidden = false
        let cache = cache
        Task { @MainActor [weak self] in
            guard let self else { return }
            let response = await self.run(panel, on: window)
            guard response == .OK, let url = panel.url else {
                return // dismissed
            }
            do {
                let res = try await cache.fetchAttachment(accountID: s.accountId, messageID: s.id, partID: a.partId)
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
    /// " (2)" and so on. One toast sums it up, and `done` runs at the end,
    /// dismissal included, so the button that started the run can be
    /// disabled meanwhile (attachments.go `saveAllAttachments`).
    func saveAll(_ atts: [Attachment], of s: MessageSummary, from window: NSWindow?, done: @escaping @MainActor () -> Void) {
        let panel = NSOpenPanel()
        panel.title = L10n.T("Save Attachments")
        panel.message = L10n.T("Save Attachments")
        panel.canChooseDirectories = true
        panel.canChooseFiles = false
        panel.canCreateDirectories = true
        panel.allowsMultipleSelection = false
        panel.prompt = mn(L10n.T("_Save"))
        Task { @MainActor [weak self] in
            defer { done() }
            guard let self else { return }
            let response = await self.run(panel, on: window)
            guard response == .OK, let folder = panel.url else {
                return // dismissed
            }
            var saved = 0
            var failed = 0
            for a in atts {
                do {
                    try await self.saveInto(folder, s, a)
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
    /// taken yet (attachments.go `saveInto`).
    private func saveInto(_ folder: URL, _ s: MessageSummary, _ a: Attachment) async throws {
        let res = try await cache.fetchAttachment(accountID: s.accountId, messageID: s.id, partID: a.partId)
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
