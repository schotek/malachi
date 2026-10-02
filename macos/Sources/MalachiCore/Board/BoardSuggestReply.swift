// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The rules and the view model of the case detail's Suggest Reply control
// (`BoardReplyController` runs the request): when it is offered, when it
// can run, and what it shows. Pure; the detail only shows the view model.
// Swift-first, like `Board`; the texts' msgids are in ui/internal/board
// (reply.go).

import Foundation

extension Board {
    /// Why a suggested reply brought no draft.
    public enum SuggestReplyFailure: Sendable, Equatable, CaseIterable {
        case notFound
        case notSignedIn
        case toolsMissing
        case timeout
        /// The user pressed Stop.
        case cancelled
        /// Claude Code ended badly.
        case stopped
        /// The daemon did not answer (board.get, board.setDraft).
        case backend
        /// The model finished without creating a draft.
        case noDraft
    }

    /// What the application's one suggested reply is doing.
    public enum SuggestReplyState: Sendable, Equatable {
        case idle
        /// A request for case `CaseID` runs (asking, writing, linking).
        case running(CaseID)
        /// The last request, for case `CaseID`, failed; shown on that case
        /// until another request starts.
        case failed(CaseID, SuggestReplyFailure)

        public var isRunning: Bool {
            if case .running = self { return true }
            return false
        }
    }

    /// Whether the detail of `c` offers Suggest Reply: a real case (not the
    /// samples) with a message a reply answers, no suggested reply yet,
    /// not for reading only (its state in effect is not `info`), not done,
    /// in an account that can reply (a mail account's reply, an issue
    /// tracker's comment draft). An account not listed (yet) counts as a
    /// mail account unless the case is an issue.
    public static func suggestReplyOffered(_ c: Case, in s: Snapshot, samples: Bool) -> Bool {
        guard !samples, c.reply != nil, c.draft == nil, !c.visibility.isDone,
              state(of: c, annotated: s.annotated) != .info
        else { return false }
        if let a = s.accounts.first(where: { $0.id == c.account }) {
            return a.canReply
        }
        return c.issue == nil
    }

    /// What `suggestReplyView` looks at.
    public struct SuggestReplyInputs: Sendable, Equatable {
        public var provider: AssistantProviderID = .claude
        /// `suggestReplyOffered` for the case shown.
        public var offered: Bool
        /// The feature can exist: the assistant shown with the In App
        /// target (`AssistantController.panelShown`, as the compose
        /// rewrite) and the bridge beside the application.
        public var available: Bool
        public var claudeFound: Bool
        /// As last asked; nil when not known (counts as signed in).
        public var signedIn: Bool?
        public var state: SuggestReplyState
        /// The case shown.
        public var caseID: CaseID

        public init(
            offered: Bool, available: Bool, claudeFound: Bool, signedIn: Bool?, state: SuggestReplyState, caseID: CaseID, provider: AssistantProviderID = .claude
        ) {
            self.provider = provider
            self.offered = offered
            self.available = available
            self.claudeFound = claudeFound
            self.signedIn = signedIn
            self.state = state
            self.caseID = caseID
        }
    }

    /// The control in the place of the Suggested Reply block. Strings are
    /// fixed texts; nothing from mail or from the model is in it.
    public struct SuggestReplyView: Sendable, Equatable {
        /// The control is there at all.
        public var shown: Bool
        /// The field and the button take input.
        public var enabled: Bool
        /// The request runs for this case: the spinner, `progress` and
        /// Stop instead of the button.
        public var running: Bool
        /// The line under the field: why it is disabled, or how the last
        /// request for this case failed; "" for none.
        public var note: String
        /// `note` is a failure (shown as an error line).
        public var noteIsFailure: Bool
        public var title: String
        public var placeholder: String
        public var progress: String
        public var stop: String

        public static let hidden = SuggestReplyView(
            shown: false, enabled: false, running: false, note: "", noteIsFailure: false, title: "", placeholder: "",
            progress: "", stop: "")
    }

    /// The control for `i`: hidden unless offered and available; while the
    /// request runs for this case its progress and Stop; disabled, with
    /// the reason, while it runs for another case, without Claude Code, or
    /// signed out (the rewrite's hint pointing to Settings → AI); the last
    /// failure for this case under the usable control.
    public static func suggestReplyView(_ i: SuggestReplyInputs) -> SuggestReplyView {
        guard i.offered, i.available else { return .hidden }
        var v = SuggestReplyView(
            shown: true, enabled: true, running: false, note: "", noteIsFailure: false, title: Text.suggestReply,
            placeholder: Text.suggestReplyPlaceholder, progress: Text.suggestReplyRunning,
            stop: Assistant.panelTexts().stop)
        switch i.state {
        case .running(let id) where id == i.caseID:
            v.enabled = false
            v.running = true
            return v
        case .running:
            v.enabled = false
            v.note = Text.suggestReplyElsewhere
            return v
        case .idle, .failed:
            break
        }
        if !i.claudeFound {
            v.enabled = false
            v.note = i.provider == .chatgpt ? L10n.T("Codex was not found. Choose a native Codex executable.") : Assistant.panelTexts().notFound
            return v
        }
        if i.signedIn == false {
            v.enabled = false
            v.note = i.provider == .chatgpt ? L10n.T("Reconnect to ChatGPT") : Assistant.signInTexts().hint
            return v
        }
        if case .failed(let id, let f) = i.state, id == i.caseID {
            if i.provider == .chatgpt, f == .notFound || f == .notSignedIn {
                v.note = L10n.T("The suggested reply failed: %s.", f == .notFound ? L10n.T("Codex was not found. Choose a native Codex executable.") : L10n.T("Reconnect to ChatGPT"))
            } else { v.note = Text.suggestReplyFailed(f) }
            v.noteIsFailure = true
        }
        return v
    }
}
