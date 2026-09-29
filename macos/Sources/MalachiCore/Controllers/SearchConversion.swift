// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The search in the user's own words (ui/internal/assistant search.go,
/// the In App target; the GTK widgets follow this port) without the search
/// field: the typed words go to the user's Claude Code
/// (`Assistant.searchMessage` under `Assistant.searchSystemPrompt`, the
/// answer shaped by `Assistant.searchSchema`, one `AssistantRequest`), and
/// the query of its answer (`Assistant.parseSearchQuery` of the result's
/// structured_output, or of its text when Claude Code gave no
/// structured_output) is what the field then searches for.
///
/// A failure is the toast "The search could not be converted: %s" with
/// the reason (`Assistant.searchFailedText`); the caller keeps the typed
/// words. A new conversion cancels the one under way; `cancel()` ends it,
/// and its completion is not called.
@MainActor
public final class SearchConversion {
    public enum Outcome: Sendable, Equatable {
        /// The query to search for.
        case query(String)
        /// The toast.
        case failed(String)
        /// The user declined the consent question; nothing was sent.
        case declined
    }

    public let request: AssistantRequest
    /// The date for the system prompt, YYYY-MM-DD.
    public var today: @MainActor () -> String = AssistantPanelController.localDate

    public init(request: AssistantRequest) {
        self.request = request
    }

    /// A conversion is under way.
    public var running: Bool { request.running }

    /// Asks for the query of `words`. False, and nothing happens, when
    /// there are no words; otherwise `completion` is called once, later
    /// (also for words that are too long), unless the conversion is
    /// cancelled.
    @discardableResult
    public func convert(_ words: String, completion: @escaping @MainActor (Outcome) -> Void) -> Bool {
        let message: String
        do {
            message = try Assistant.searchMessage(words)
        } catch Assistant.SearchError.noWords {
            return false
        } catch {
            request.cancel()
            let text = Assistant.searchFailedText(String(describing: error))
            Task { @MainActor in
                completion(.failed(text))
            }
            return true
        }
        request.start(
            systemPrompt: Assistant.searchSystemPrompt(today: today()), message: message,
            jsonSchema: Assistant.searchSchema,
            completion: { outcome in
                completion(Self.outcome(outcome))
            })
        return true
    }

    /// Ends the conversion under way; its completion is not called.
    public func cancel() {
        request.cancel()
    }

    /// The query of an answer, or the toast.
    static func outcome(_ outcome: AssistantRequest.Outcome) -> Outcome {
        switch outcome {
        case .answered(let text, let structured):
            if let q = structured.flatMap(Assistant.parseSearchQuery) ?? Assistant.parseSearchQuery(Data(text.utf8)) {
                return .query(q)
            }
            return .failed(Assistant.searchFailedText("the answer holds no query"))
        case .failed(let failure):
            return .failed(Assistant.searchFailedText(failure.reason))
        case .declined:
            return .declined
        }
    }
}
