// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The assistant panel of the main window put together
/// (ui/internal/assistant, the In App target; GTK window/assistant_panel.go
/// follows this port): the `AssistantPanelController`, its view, and what it needs from
/// the rest of the application.
///
/// - The list's selection goes to the controller (`followSelection`,
///   chained by `Integration` after the reader): one message, or a
///   conversation row's folder members newest first (only its newest
///   message until the members are known; `resolveContext` asks
///   `ListController.selectedIDs`), with its subject and thread. Never an
///   Outbox message: it is not on the server yet. The chip follows it
///   until the conversation's first question; after that the conversation
///   keeps its context and the selection only brings the bar "Another
///   message is selected".
/// - The first question ever asks "Send Mail to Claude?" as a sheet on the
///   main window (the controller keeps the answer in `assistant-consent`).
/// - A draft card's Open Draft is `ActionsController.openSavedDraft`, a
///   link in an answer the actions' `openLink` with no listed links, so
///   its destination is always confirmed. Get Claude Code…, a page the
///   application itself names, opens in the browser as the sign-in pages
///   do (`openInBrowser`), without a question.
/// - The Assistant menu, the Message menu and an attachment's menu run
///   their actions here while In App is the target (`AssistantActions`):
///   the panel unfolds and runs the action on the selection (a
///   conversation that keeps its context gets the selection added first
///   when it is not part of it); from a message window the main window
///   comes forward and the action runs on that message, the list's
///   selection stays.
///
/// One per main window, owned by `Integration` for the application's life;
/// `close()` ends a running Claude Code when the application quits.
@MainActor
final class AssistantPanelHost {
    let controller: AssistantPanelController
    let viewController: AssistantPanelViewController

    private let state: AppState
    private let list: ListController
    private weak var mainWindow: MainWindowController?

    init(
        state: AppState, list: ListController, actions: ActionsController,
        messageActions: MessageActionsController, mainWindow: MainWindowController
    ) {
        self.state = state
        self.list = list
        self.mainWindow = mainWindow
        let controller = AssistantPanelController(
            settings: state.settings, locator: state.claudeCode, bridge: state.paths.mcpBridge?.path,
            socket: state.paths.socket)
        self.controller = controller
        viewController = AssistantPanelViewController(controller: controller)

        let alerts = state.alerts
        controller.consent = { [weak mainWindow] in
            let t = Assistant.panelTexts()
            return await alerts.confirm(
                on: mainWindow?.window, heading: t.consentHeading, body: t.consentBody, confirmLabel: t.allow,
                declineLabel: t.cancel)
        }
        controller.resolveContext = { [weak self] context, done in
            guard let self else {
                done(context.selection)
                return
            }
            self.resolve(context, done)
        }
        controller.openDraft = { [weak actions] ref in
            actions?.openSavedDraft(account: AccountID(rawValue: ref.accountID), id: DraftID(rawValue: ref.draftID))
        }
        viewController.onLink = { [weak messageActions] href, window in
            messageActions?.openLink(href, links: [], from: window)
        }
        let toasts = state.toasts
        viewController.onOpenPage = { url in
            openInBrowser(url) { text in
                toasts.show(text)
            }
        }
    }

    /// Ends the conversation's Claude Code for good.
    func close() {
        controller.close()
    }

    private var model: MailModel { list.mailbox.model }

    // MARK: The context

    /// The list's selection as the panel's context; nil for none or an
    /// Outbox message.
    func selectionContext() -> AssistantPanelController.Context? {
        guard let row = list.selectedRow, !model.inOutbox(row.message) else { return nil }
        let account = row.message.accountId.rawValue
        guard row.thread else {
            return Self.context(of: row.message)
        }
        // The conversation's subject is its newest member's without Re: and
        // Fwd:, as its row shows it.
        let subject = row.summary?.subject ?? row.message.subject
        let thread = row.summary?.id.rawValue ?? row.key.thread?.rawValue ?? row.message.threadId?.rawValue ?? ""
        if let ids = model.rowIDs(row) {
            // rowIDs lists the members oldest first.
            let newestFirst = ids.reversed().map(\.rawValue)
            return AssistantPanelController.Context(
                selection: Assistant.Selection(accountID: account, messageIDs: newestFirst), count: newestFirst.count,
                subject: subject, threadID: thread)
        }
        // A folded conversation whose members are not known yet: its newest
        // message stands for it until they are asked for.
        return AssistantPanelController.Context(
            selection: Assistant.Selection(accountID: account, messageIDs: [row.message.id.rawValue]),
            count: max(row.summary?.messageCount ?? 2, 2), partial: true, subject: subject, threadID: thread)
    }

    /// One message as the panel's context.
    static func context(of s: MessageSummary) -> AssistantPanelController.Context {
        AssistantPanelController.Context(
            selection: Assistant.Selection(accountID: s.accountId.rawValue, messageIDs: [s.id.rawValue]),
            subject: s.subject, threadID: s.threadId?.rawValue ?? "")
    }

    /// The list's selection changed: the panel follows it (or, once the
    /// conversation keeps its context, compares it with what the
    /// conversation is about).
    func followSelection() {
        controller.setContext(selectionContext())
    }

    /// Completes a folded conversation's members (newest first) when the
    /// list still shows it selected; otherwise the context stays what it
    /// was.
    private func resolve(
        _ context: AssistantPanelController.Context, _ done: @escaping @MainActor (Assistant.Selection) -> Void
    ) {
        guard let row = list.selectedRow, row.thread, row.message.accountId.rawValue == context.selection.accountID,
              context.selection.messageIDs.first == row.message.id.rawValue else {
            done(context.selection)
            return
        }
        list.selectedIDs { row, ids in
            done(Assistant.Selection(accountID: row.message.accountId.rawValue, messageIDs: ids.reversed().map(\.rawValue)))
        }
    }

    // MARK: Running from the menus

    /// Unfolds the panel (the main window comes forward first when asked).
    private func reveal(bringingMainWindow: Bool) {
        if bringingMainWindow {
            state.showMainWindow()
        }
        mainWindow?.revealAssistant()
    }

    /// A message action on the list's selection.
    func run(_ action: Assistant.Action) {
        guard let context = selectionContext() else { return }
        reveal(bringingMainWindow: false)
        controller.run(action, on: context)
    }

    /// A message action from a message window: the main window comes
    /// forward and the action runs on the window's message.
    func run(_ action: Assistant.Action, about s: MessageSummary) {
        guard !model.inOutbox(s) else { return }
        reveal(bringingMainWindow: true)
        controller.run(action, on: Self.context(of: s))
    }

    /// Summarize Unread in This Folder for the folder selected in the
    /// sidebar.
    func summarizeUnread(_ k: FolderKey) {
        reveal(bringingMainWindow: false)
        controller.summarizeUnread(accountID: k.account.rawValue, folderID: k.folder.rawValue)
    }

    /// A question about an attachment (P12): the panel waits for the
    /// user's words.
    func ask(about a: Attachment, of s: MessageSummary, from window: NSWindow?) {
        reveal(bringingMainWindow: window !== mainWindow?.window)
        controller.askAttachment(
            accountID: s.accountId.rawValue, messageID: s.id.rawValue, partID: a.partId, subject: s.subject,
            threadID: s.threadId?.rawValue ?? "")
    }
}
