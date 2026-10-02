// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// What the board's detail shows in place of the suggested reply: nothing
// (the Suggest Reply control), the draft loading, the draft ready to be
// edited inline, or why it could not be opened. The suggested reply is a
// local draft the case links (`Board.Case.draft`); the editor opens it with
// draft.get and keeps it while the case is selected. Every autosave bumps
// the case's version and the board lists the case again, so the editor is
// keyed by case, account and draft, never by the case's version: a refresh
// of the same case never reloads it.
//
// Swift-first, like the board.

import Foundation
import os

/// The inline reply editor's state for the selected case.
@MainActor
public final class BoardReplyEditorController {
    /// What the editor edits: the draft of the case in its account.
    public struct Key: Hashable, Sendable {
        public let caseID: Board.CaseID
        public let account: AccountID
        public let draft: DraftID

        public init(caseID: Board.CaseID, account: AccountID, draft: DraftID) {
            self.caseID = caseID
            self.account = account
            self.draft = draft
        }
    }

    /// Why the draft could not be opened.
    public enum Failure: Sendable, Equatable {
        /// draft.get said draftNotFound: the board drops the link soon.
        case gone
        /// Anything else: Try Again (`retry`).
        case backend
    }

    public enum Phase: Sendable, Equatable {
        /// No suggested reply (or none to show): the Suggest control shows.
        case none
        case loading(Key)
        /// The draft as draft.get returned it, as a compose window would
        /// open it (`fromDraft(kind: .edit, …)`, its id and version
        /// included).
        case ready(Key, ComposeParams)
        /// `.backend` offers Try Again.
        case failed(Key, Failure)

        /// The key of every phase but `.none`.
        public var key: Key? {
            switch self {
            case .none: return nil
            case .loading(let k), .ready(let k, _), .failed(let k, _): return k
            }
        }
    }

    public private(set) var phase: Phase = .none {
        didSet {
            if phase != oldValue {
                observers.notify()
            }
        }
    }

    private let client: RPCClient
    private let observers = BoardObservers()
    /// Drafts the inline pane sent or discarded: hidden until the board
    /// no longer links them to their case.
    private var ended: Set<Key> = []
    /// Bumped by every load and every change of what is shown, so an
    /// answer that arrives later than another request is dropped.
    private var generation = 0
    private var load: Task<Void, Never>?
    private let log = Logger(subsystem: "io.github.schotek.Malachi", category: "board")

    public init(client: RPCClient) {
        self.client = client
    }

    /// Calls `f` after `phase` changed; the token removes it.
    public func observe(_ f: @escaping @MainActor () -> Void) -> BoardObserverToken {
        observers.add(f)
    }

    /// The selected case (nil: none). A case with a suggested reply loads
    /// it with draft.get unless the editor has it already: the same key
    /// changes nothing, whatever else changed in the case (an autosave
    /// bumps its version, never the editor). A draft `ended` stays hidden
    /// until the case no longer links it. The host does not call this for
    /// the invented samples, which have no draft behind them.
    public func show(_ c: Board.Case?) {
        guard let c else {
            reset()
            return
        }
        // The board dropped (or replaced) a link the pane ended: forget it.
        ended = ended.filter { $0.caseID != c.id || $0.draft == c.draft?.id }
        guard let draft = c.draft else {
            reset()
            return
        }
        let key = Key(caseID: c.id, account: c.account, draft: draft.id)
        if ended.contains(key) {
            reset()
            return
        }
        if phase.key == key {
            return
        }
        start(key)
    }

    /// Loads the draft again after a failure (`.failed(_, .backend)`; a
    /// draft that is gone is tried again too).
    public func retry() {
        guard case .failed(let key, _) = phase else { return }
        start(key)
    }

    /// The pane sent or the user discarded `key`: the editor hides it until
    /// the board drops the link (`show` with the case without it).
    public func ended(_ key: Key) {
        ended.insert(key)
        if phase.key == key {
            reset()
        }
    }

    private func reset() {
        generation += 1
        load?.cancel()
        load = nil
        phase = .none
    }

    private func start(_ key: Key) {
        generation += 1
        let mine = generation
        load?.cancel()
        phase = .loading(key)
        let client = client
        let params = DraftGetParams(accountId: key.account, draftId: key.draft)
        load = Task { [weak self] in
            let outcome: Result<DraftGetResult, any Error>
            do {
                outcome = .success(try await client.call(API.DraftGet.self, params))
            } catch {
                outcome = .failure(error)
            }
            guard let self, self.generation == mine else { return }
            self.load = nil
            switch outcome {
            case .success(let r):
                self.phase = .ready(key, fromDraft(kind: .edit, draft: r.draft, blocked: BlockedContent()))
            case .failure(let err):
                self.log.info("draft.get: \(String(describing: err), privacy: .public)")
                if let e = err as? RPCError, e.code == .draftNotFound {
                    self.phase = .failed(key, .gone)
                } else {
                    self.phase = .failed(key, .backend)
                }
            }
        }
    }
}
