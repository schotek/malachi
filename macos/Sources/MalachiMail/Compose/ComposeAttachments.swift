// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import UniformTypeIdentifiers

// The attachments of a compose window (compose.go "Attachments"): picking
// files and images, importing them into the daemon's attachment store,
// the chips, removal, and the cid: registrations of inline pictures.

extension ComposeWindowController {
    /// `compose.attach`: files to attach, as many as chosen.
    @objc func attachFiles(_ sender: Any?) {
        guard let window else { return }
        let panel = NSOpenPanel()
        panel.title = L10n.T("Attach Files")
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = true
        Task { [weak self] in
            let response = await panel.beginSheetModal(for: window)
            guard let self, response == .OK, !self.draft.draft.closed else { return }
            for url in panel.urls {
                guard url.isFileURL else {
                    self.toast(L10n.T("Only local files can be attached"))
                    continue
                }
                self.importFile(path: url.path, name: url.lastPathComponent, inline: false, then: nil)
            }
        }
    }

    /// `compose.insert-image`: one picture, imported inline and inserted
    /// at the caret as `cid:<contentId>`.
    @objc func insertImage(_ sender: Any?) {
        guard let window else { return }
        let panel = NSOpenPanel()
        panel.title = L10n.T("Insert Image")
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        panel.allowedContentTypes = [.png, .jpeg, .gif, .webP]
        Task { [weak self] in
            let response = await panel.beginSheetModal(for: window)
            guard let self, response == .OK, !self.draft.draft.closed, let url = panel.url else { return }
            guard url.isFileURL else {
                self.toast(L10n.T("Only local images can be inserted"))
                return
            }
            let path = url.path
            self.importFile(path: path, name: url.lastPathComponent, inline: true) { [weak self] att in
                guard let self, let cid = att.contentId else { return }
                self.editor.registerCID(cid, path: path, contentType: att.contentType)
                self.editor.exec("insertImage", "cid:" + cid)
                self.focusEditor()
            }
        }
    }

    /// importFile hands the path to the backend and adds the attachment on
    /// success; `then` (optional) runs afterwards on the main actor.
    func importFile(path: String, name: String, inline: Bool, then: (@MainActor (DraftAttachment) -> Void)?) {
        setStatus(L10n.T("Attaching %s…", name))
        let params = AttachmentImportParams(accountId: account.id, path: path, filename: name, inline: inline)
        let client = state.client
        Task { [weak self] in
            let outcome: Result<AttachmentImportResult, any Error>
            do {
                outcome = .success(try await client.call(API.AttachmentImport.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self, !self.draft.draft.closed else { return }
            switch outcome {
            case .failure(let err):
                self.toast(rpcErrorText(L10n.T("Attaching %s", name), err))
                self.draft.refreshStatus()
            case .success(let res):
                let att = res.attachment
                self.attachments.append(att)
                self.chips.add(att)
                self.draft.markDirty()
                then?(att)
            }
        }
    }

    /// The ✕ of a chip: the attachment leaves the list and the store.
    func removeAttachment(_ id: String) {
        guard let i = attachments.firstIndex(where: { $0.id == id }) else { return }
        let removed = attachments.remove(at: i)
        if removed.inline, let cid = removed.contentId {
            editor.unregisterCID(cid)
        }
        chips.remove(id: id)
        draft.markDirty()
        let params = AttachmentRemoveParams(accountId: account.id, attachmentId: id)
        let client = state.client
        Task {
            do {
                _ = try await client.call(API.AttachmentRemove.self, params)
            } catch {
                // Best effort: the sweep takes what is left.
            }
        }
    }

    /// setAttachments replaces the list and chips with what the backend
    /// kept. An inline picture the window did not insert itself (the
    /// backend copied it out of a quoted original) is served to the editor
    /// from the backend; one that is gone from the list is forgotten.
    func setAttachments(_ atts: [DraftAttachment]) {
        chips.removeAll()
        let kept = Set(atts.filter(\.inline).compactMap(\.contentId))
        for a in attachments where a.inline {
            if let cid = a.contentId, !kept.contains(cid) {
                editor.unregisterCID(cid)
            }
        }
        attachments = []
        for a in atts {
            attachments.append(a)
            chips.add(a)
            if a.inline, let cid = a.contentId, !CIDRegistry.shared.isRegistered(cid) {
                registerInline(a)
            }
        }
    }

    /// registerInline makes the editor fetch the picture behind
    /// cid:<contentId> from the backend (attachment.get), for a copy the
    /// backend made.
    func registerInline(_ a: DraftAttachment) {
        guard let cid = a.contentId else { return }
        let client = state.client
        let params = AttachmentGetParams(accountId: account.id, attachmentId: a.id)
        editor.registerCIDFetcher(cid) {
            let res = try await client.call(API.AttachmentGet.self, params)
            return (res.data, res.contentType)
        }
    }
}
