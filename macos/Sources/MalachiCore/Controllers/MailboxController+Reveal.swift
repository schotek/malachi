// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

// Show in Mail (the board's detail): the folder of a message is selected
// and, once its first page is listed, the message's row is handed to the
// view to select and focus (`onFocusRow`, as the first search result is).
// A message the page does not hold, a folder the sidebar does not have, a
// listing that failed or a list showing search results hand over to
// `otherwise` (the application opens the message in its own window). The
// request belongs to the listing it started (the list generation): the
// user choosing another folder or starting a search meanwhile drops it, as
// does the window leaving Mail (`cancelReveal`), so it never selects a row
// later than the user can see why.
//
// Swift-first, with the board: the GTK window ports it with
// ui/internal/board.

/// A message to select once its folder is listed (`ListController.reveal`).
struct PendingReveal {
    var folder: FolderKey
    var message: MessageID
    var thread: ThreadID?
    /// The list generation of the listing the request waits for.
    var generation: UInt64
    var found: (@MainActor () -> Void)?
    var otherwise: @MainActor () -> Void
}

extension ListController {
    /// Selects folder `folder` of account `account` and then the row of
    /// `message` (in a grouped listing the conversation row holding it,
    /// found by `thread` when its newest member is another message); calls
    /// `found` right before the row is handed to `onFocusRow`, or
    /// `otherwise` instead when the row cannot be shown. A newer request
    /// replaces one still waiting.
    public func reveal(
        account: AccountID, folder: FolderID, message: MessageID, thread: ThreadID? = nil,
        found: (@MainActor () -> Void)? = nil, otherwise: @escaping @MainActor () -> Void
    ) {
        let k = FolderKey(account: account, folder: folder)
        guard !mailbox.model.search.active, mailbox.model.folder(k) != nil else {
            pendingReveal = nil
            otherwise()
            return
        }
        pendingReveal = nil
        mailbox.selectFolder(k, fav: false)
        // Selecting the folder started its listing (or one is on its way
        // already): the request waits for that generation's reply.
        pendingReveal = PendingReveal(
            folder: k, message: message, thread: thread, generation: mailbox.model.listGen, found: found,
            otherwise: otherwise)
        // The folder was listed already (selecting it again lists nothing),
        // or has nothing to list: the rows are as they will be.
        if mailbox.model.listFolder == k, !mailbox.model.loading {
            finishReveal()
        }
    }

    /// Drops a request still waiting (the window left Mail).
    public func cancelReveal() {
        pendingReveal = nil
    }

    /// After the first page of the listed folder arrived (or failed): the
    /// waiting request's row, or its `otherwise`.
    func finishReveal() {
        guard let r = pendingReveal else { return }
        pendingReveal = nil
        let m = mailbox.model
        guard m.listGen == r.generation, m.listFolder == r.folder, !m.search.active else {
            // The user went elsewhere meanwhile.
            return
        }
        let row = rows.first { $0.message.id == r.message }
            ?? r.thread.flatMap { t in rows.first { $0.key.thread == t && !$0.member } }
        guard let row else {
            r.otherwise()
            return
        }
        r.found?()
        onFocusRow?(row.key)
    }
}
