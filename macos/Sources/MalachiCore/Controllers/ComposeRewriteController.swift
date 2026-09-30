// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The compose window's rewrite (ui/internal/assistant rewrite.go, the In
/// App target; GTK assistantpanel.Rewriter is its port) without its popover: a
/// passage of the message, the selection or the user's own text, goes to
/// the user's Claude Code with a preset or the user's own instruction
/// (`Assistant.rewriteMessage` under `Assistant.rewriteSystemPrompt`, one
/// `AssistantRequest`), and the answer, cleaned (`Assistant.cleanRewrite`),
/// is offered for Replace or Insert Below, which the window does as plain
/// text.
///
/// `state`: `idle` (nothing asked, or the consent was declined),
/// `running` with the answer as it streams (cleaned the same way),
/// `done` with the text to insert, `failed` with the line to show (Claude
/// Code not found or not signed in, the panel's texts; "The assistant
/// stopped: …" otherwise, an empty answer included). A new rewrite
/// cancels the one under way; `cancel()` (the popover or the window
/// closed) ends it and goes back to `idle`.
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
    /// passage that is too long (or a rewrite that does not exist) fails at
    /// once.
    @discardableResult
    public func start(rewrite: Assistant.Rewrite, custom: String, passage: String) -> Bool {
        let message: String
        do {
            message = try Assistant.rewriteMessage(rewrite, custom: custom, passage: passage)
        } catch Assistant.RewriteError.noPassage, Assistant.RewriteError.noInstruction {
            return false
        } catch {
            request.cancel()
            state = .failed(Assistant.stoppedText(String(describing: error)))
            return true
        }
        state = .running(preview: "")
        request.start(
            systemPrompt: Assistant.rewriteSystemPrompt(), message: message,
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
        switch outcome {
        case .answered(let text, _):
            let clean = Assistant.cleanRewrite(text)
            state = clean.isEmpty ? .failed(Assistant.stoppedText("the answer is empty")) : .done(clean)
        case .failed(let failure):
            state = .failed(failure.text)
        case .declined:
            state = .idle
        }
    }
}
