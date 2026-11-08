// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The compose window's rewrite (ui/internal/assistant rewrite.go, the In
/// App target; GTK assistantpanel.Rewriter is its port) without its
/// popover: a passage of the message, the selection or the user's own
/// text, goes to the user's assistant with a preset or the user's own
/// instruction (the daemon's prompt of purpose rewrite, `RewriteInput`,
/// one `AssistantRequest`), and the answer, cleaned
/// (`Assistant.cleanRewrite`), is offered for Replace or Insert Below,
/// which the window does as plain text.
///
/// `state`: `idle` (nothing asked, or the consent was declined),
/// `running` with the answer as it streams (cleaned the same way),
/// `done` with the text to insert, `failed` with the line to show (Claude
/// Code not found or not signed in, the panel's texts; "The assistant
/// stopped: …" otherwise, a passage the daemon refuses as too long and an
/// empty answer included). A new rewrite cancels the one under way;
/// `cancel()` (the popover or the window closed) ends it and goes back to
/// `idle`.
@MainActor
public final class ComposeRewriteController {
    public enum State: Sendable, Equatable {
        case idle
        /// Asked; the answer so far.
        case running(preview: String)
        /// The answer, cleaned and not empty.
        case done(String)
        /// What went wrong, as a line to show.
        case failed(String)
    }

    public let request: AssistantRequest

    public private(set) var state: State = .idle {
        didSet {
            if state != oldValue {
                onState?(state)
            }
        }
    }

    /// `state` changed.
    public var onState: (@MainActor (State) -> Void)?
    /// The account the window writes from, whose profile note the daemon
    /// appends to the system prompt; nil: none.
    public var account: @MainActor () -> AccountID? = { nil }

    public init(request: AssistantRequest) {
        self.request = request
    }

    public var running: Bool {
        if case .running = state {
            return true
        }
        return false
    }

    /// Asks for `rewrite` of `passage` (`custom`: the user's own
    /// instruction, for `.custom`). False, and nothing changes, when there
    /// is nothing to ask: an empty passage, or `.custom` without words. A
    /// passage the daemon refuses (too long) fails with its reason.
    @discardableResult
    public func start(rewrite: Assistant.Rewrite, custom: String, passage: String) -> Bool {
        if Assistant.trimmedString(passage).isEmpty || (rewrite == .custom && Assistant.trimmedString(custom).isEmpty) {
            return false
        }
        state = .running(preview: "")
        request.start(
            purpose: .rewrite,
            input: PromptInput(rewrite: RewriteInput(preset: rewrite.preset, instruction: custom, passage: passage)),
            noteAccountID: account(),
            onText: { [weak self] text in
                guard let self, self.running else { return }
                self.state = .running(preview: Assistant.cleanRewrite(text))
            },
            completion: { [weak self] outcome in
                self?.finished(outcome)
            })
        return true
    }

    /// Ends the rewrite under way (the popover or the window closed) and
    /// forgets the answer.
    public func cancel() {
        request.cancel()
        state = .idle
    }

    private func finished(_ outcome: AssistantRequest.Outcome) {
        state = Self.answerState(outcome)
    }

    /// assistantpanel.answerState: the state a rewrite or the compose
    /// window's writing ends in: the answer cleaned
    /// (`Assistant.cleanRewrite`), an empty one a failure; a failure's
    /// line; idle when the consent was declined.
    public static func answerState(_ outcome: AssistantRequest.Outcome) -> State {
        switch outcome {
        case .answered(let text, _):
            let clean = Assistant.cleanRewrite(text)
            return clean.isEmpty ? .failed(Assistant.stoppedText("the answer is empty")) : .done(clean)
        case .failed(let failure):
            return .failed(failure.text)
        case .declined:
            return .idle
        }
    }
}
