// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore
import os

/// What the Assistant menu and the attachment chips' "Ask the Assistant…"
/// do (ui/internal/assistant; GTK window/assistant.go follows this port): build the
/// prompt for what they act on, the link for the chosen Claude app (the
/// `assistant-target` preference; `AssistantController.pick` says whether
/// it can, and nothing goes to the other app instead), and hand the link
/// to macOS. Claude
/// opens with the prompt prefilled and sends nothing: the user reads it,
/// finishes it and sends it there, and Claude reads the mail itself
/// through the registered malachi-mcp bridge. No confirmation is asked:
/// the user registered the bridge and sends the prompt themselves.
///
/// A prompt carries only the API's opaque ids, never a subject, a name, a
/// folder's name or a file's name (they are written by third parties). A
/// file goes over as a path: written like Open writes it (the private
/// directory for opening, the quarantine attribute), attached to a Cowork
/// task in Claude Desktop, which asks the user to confirm it, or as the
/// working directory of Claude Code.
///
/// With In App chosen (the assistant panel) nothing leaves the
/// application: the actions run in the panel (`AssistantPanelHost`), which
/// unfolds, takes the selection (or a message window's message) as its
/// context and asks the user's Claude Code; an attachment is asked about
/// through the bridge's get_attachment, so only the types it reads
/// (`Assistant.attachmentReadable`) can be.
///
/// `MessageActionsController` owns one and forwards the menus' and the
/// chips' requests.
@MainActor
final class AssistantActions {
    let state: AppState
    let list: ListController
    let attachments: AttachmentActions
    /// The assistant panel of the main window; `Integration` sets it.
    weak var panel: AssistantPanelHost?

    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "assistant")

    init(state: AppState, list: ListController, attachments: AttachmentActions) {
        self.state = state
        self.list = list
        self.attachments = attachments
    }

    private var model: MailModel { list.mailbox.model }

    // MARK: Messages

    /// A message action on the list's selection: a conversation row's
    /// folder members, newest first (`ListController.selectedIDs` hands
    /// them oldest first, fetched first when not known yet), or the one
    /// message. Never an Outbox message: it is not on the server yet.
    func ask(_ action: Assistant.Action, from window: NSWindow?) {
        let (target, ok) = state.assistant.pick(needsBridge: true)
        if target == .app {
            if ok {
                panel?.run(action)
            }
            return
        }
        list.selectedIDs { [weak self] row, ids in
            guard let self, !self.model.inOutbox(row.message) else { return }
            let newestFirst = row.thread ? Array(ids.reversed()) : ids
            self.ask(action, account: row.message.accountId, ids: newestFirst, from: window)
        }
    }

    /// A message action on one message (a message window).
    func ask(_ action: Assistant.Action, about s: MessageSummary, from window: NSWindow?) {
        guard !model.inOutbox(s) else { return }
        let (target, ok) = state.assistant.pick(needsBridge: true)
        if target == .app {
            if ok {
                panel?.run(action, about: s)
            }
            return
        }
        ask(action, account: s.accountId, ids: [s.id], from: window)
    }

    private func ask(_ action: Assistant.Action, account: AccountID, ids: [MessageID], from window: NSWindow?) {
        let (target, ok) = state.assistant.pick(needsBridge: true)
        guard ok else { return }
        let prompt: String
        do {
            prompt = try Assistant.prompt(
                target, action, Assistant.Selection(accountID: account.rawValue, messageIDs: ids.map(\.rawValue)))
        } catch {
            failed(error, from: window)
            return
        }
        open(Assistant.link(target, prompt), from: window)
    }

    // MARK: The folder

    /// Summarize Unread in This Folder is possible: a folder is selected
    /// in the sidebar and it is not an Outbox (whose messages are not on
    /// the server).
    var canSummarizeUnread: Bool {
        guard let k = model.selected else { return false }
        return model.folderRole(k) != .outbox
    }

    /// Summarize Unread in This Folder, for the folder selected in the
    /// sidebar.
    func summarizeUnread(from window: NSWindow?) {
        guard canSummarizeUnread, let k = model.selected else { return }
        let (target, ok) = state.assistant.pick(needsBridge: true)
        guard ok else { return }
        if target == .app {
            panel?.summarizeUnread(k)
            return
        }
        let prompt: String
        do {
            prompt = try Assistant.unreadPrompt(accountID: k.account.rawValue, folderID: k.folder.rawValue)
        } catch {
            failed(error, from: window)
            return
        }
        open(Assistant.link(target, prompt), from: window)
    }

    // MARK: Files

    /// An attachment chip's "Ask the Assistant…": the part fetched (the
    /// message downloaded first when `remote`) and written as Open writes
    /// it, then handed to the chosen Claude app, which needs its handler
    /// but not the bridge (Claude reads the file, not the mail). Failures
    /// of the fetch and the write have had their toasts.
    func ask(about a: Attachment, of s: MessageSummary, remote: Bool, from window: NSWindow?) {
        state.assistant.refreshHandlers()
        if state.settings.assistantTarget == .app {
            guard state.assistant.canAsk(about: a) else { return }
            panel?.ask(about: a, of: s, from: window)
            return
        }
        guard state.assistant.pick(needsBridge: false).ok else { return }
        Task { @MainActor [weak self] in
            guard let self, let url = await self.attachments.writeForHandOff(a, of: s, remote: remote, from: window) else { return }
            // Asked again: the download may have taken a while.
            let (target, ok) = self.state.assistant.pick(needsBridge: false)
            guard ok else { return }
            let link: String
            do {
                link = try Assistant.fileLink(target, path: url.path, prompt: Assistant.filePrompt(target))
            } catch {
                self.failed(error, from: window)
                return
            }
            self.open(link, from: window)
        }
    }

    // MARK: Opening

    /// Hands `link` to the app that handles its scheme (as remote.go
    /// `launchURI` does with a link); a failure is a toast.
    private func open(_ link: String, from window: NSWindow?) {
        guard let url = URL(string: link) else {
            log.warning("assistant link: not a URL")
            // TRANSLATORS: %s is a technical error message.
            toast(L10n.T("The link could not be opened: %s", "invalid URL"), in: window) // macOS-only string (the technical detail)
            return
        }
        Task { @MainActor [weak self] in
            do {
                _ = try await NSWorkspace.shared.open(url, configuration: NSWorkspace.OpenConfiguration())
            } catch {
                guard let self else { return }
                // The link of a file names the attachment: private.
                let ns = error as NSError
                self.log.warning("open assistant link: \(ns.domain, privacy: .public) \(ns.code, privacy: .public): \(String(describing: error), privacy: .private)")
                // TRANSLATORS: %s is a technical error message.
                self.toast(L10n.T("The link could not be opened: %s", error.localizedDescription), in: window)
            }
        }
    }

    /// A prompt or a link that could not be built: ids missing, a prompt
    /// too long even for one message, a path that is not clean.
    private func failed(_ error: any Error, from window: NSWindow?) {
        log.warning("assistant: \(String(describing: error), privacy: .public)")
        // TRANSLATORS: %s is a technical error message.
        toast(L10n.T("The link could not be opened: %s", String(describing: error)), in: window)
    }

    private func toast(_ text: String, in window: NSWindow?) {
        windowToast(text, in: window, or: state.toasts)
    }
}

extension AssistantController {
    /// Whether an attachment's "Ask the Assistant…" can run: the chosen
    /// Claude app is installed (the file goes without the bridge); for In
    /// App, the panel can run (it reads the file through the bridge's
    /// get_attachment) and the bridge returns this type's content
    /// (`Assistant.attachmentReadable`).
    func canAsk(about a: Attachment) -> Bool {
        if settings.assistantTarget == .app {
            return pick(needsBridge: true).ok && Assistant.attachmentReadable(a.contentType)
        }
        return pick(needsBridge: false).ok
    }
}
