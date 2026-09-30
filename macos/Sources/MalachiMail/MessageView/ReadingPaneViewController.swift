// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import AppKit
import MalachiCore

/// The main window's reading pane: the single-message view
/// (`MessageViewController`, with its "No Message Selected" page and its
/// banners) or, for a selected conversation row, the whole conversation
/// (`ConversationViewController`), whichever the list's selection asks for
/// (window.go `onMessageRowSelected`, extended by ui/internal/conversation).
/// A member row and a single-message row keep the single-message view.
@MainActor
final class ReadingPaneViewController: NSViewController {
    let reader: MessageViewController
    let conversationView: ConversationViewController
    let controller: ConversationController

    /// The conversation view is the one on display.
    private(set) var showsConversation = false

    init(reader: MessageViewController, conversationView: ConversationViewController) {
        self.reader = reader
        self.conversationView = conversationView
        controller = conversationView.controller
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder: NSCoder) {
        fatalError("not used")
    }

    override func loadView() {
        let container = NSView()
        container.translatesAutoresizingMaskIntoConstraints = false
        view = container
        for vc in [reader, conversationView] as [NSViewController] {
            addChild(vc)
            let v = vc.view
            v.translatesAutoresizingMaskIntoConstraints = false
            container.addSubview(v)
            NSLayoutConstraint.activate([
                v.topAnchor.constraint(equalTo: container.topAnchor),
                v.bottomAnchor.constraint(equalTo: container.bottomAnchor),
                v.leadingAnchor.constraint(equalTo: container.leadingAnchor),
                v.trailingAnchor.constraint(equalTo: container.trailingAnchor),
            ])
        }
        conversationView.view.isHidden = true
    }

    /// The list's selection (`ListController.onSelectedRowChanged`): a
    /// conversation row shows the conversation, another row its message,
    /// none the empty page.
    func show(_ row: ListRow?) {
        _ = view
        if let row, row.showsConversation {
            // Visible before the controller builds (at once when the members
            // are known), so the stack is laid out at the pane's size.
            setConversation(true)
            // The single-message view drops the message it showed.
            reader.clear()
            controller.show(row)
            return
        }
        controller.clear()
        setConversation(false)
        if let row {
            reader.show(row.message)
        } else {
            reader.clear()
        }
    }

    /// Space and Shift-Space from the list: a page of the conversation;
    /// false when no conversation is shown (the key goes on as before).
    func page(up: Bool) -> Bool {
        guard showsConversation else { return false }
        return conversationView.page(up: up)
    }

    private func setConversation(_ on: Bool) {
        guard on != showsConversation else { return }
        showsConversation = on
        conversationView.view.isHidden = !on
        reader.view.isHidden = on
    }
}
