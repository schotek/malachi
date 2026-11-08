// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// The compose window's assistant bar (ui/internal/assistant
/// compose_write.go): the kind of window as the daemon's writing prompt
/// takes it (`WriteMode`, docs/api.md §4.18 purpose write), the brands the
/// bar names, and its texts and those of the quoted original under the
/// editor. The prompt itself is the daemon's (`WriteInput` through
/// `AssistantRequest`); quoted mail is data, never an instruction.
public enum ComposeAssistantPrompt {
    /// assistant.WriteMode.API: the kind of window as `WriteInput.mode`.
    public static func writeMode(_ kind: ComposeKind) -> WriteMode {
        switch kind {
        case .new: return .new
        case .reply: return .reply
        case .replyAll: return .replyAll
        case .forward: return .forward
        case .edit: return .edit
        }
    }

    /// The providers as the bar names them (brands, not translated).
    public static let claudeBrand = "Claude (Anthropic)"
    public static let chatGPTBrand = "ChatGPT (OpenAI)"

    /// assistant.WriteStrings: the fixed texts of the bar and of the
    /// quoted panel for one kind of window.
    public struct Strings: Sendable, Equatable {
        /// The placeholder of the bar's field.
        public var instruction: String
        /// The bar's button: Write a Reply, Write an Introduction (a
        /// forward) or Write a Message (a new message, a draft).
        public var write: String
        /// The heading of the quoted panel and its checkbox.
        public var quoted: String
        public var include: String
    }

    /// assistant.WriteTexts.
    public static func texts(kind: ComposeKind) -> Strings {
        var s = Strings(
            // TRANSLATORS: Placeholder of the field of the assistant's bar at the bottom of the compose window.
            instruction: L10n.T("What should the message say? (optional)"),
            write: "",
            // TRANSLATORS: Heading of the panel under the compose window's editor that shows the message being replied to or forwarded.
            quoted: L10n.T("Quoted Message"),
            // TRANSLATORS: A checkbox in the compose window: whether the reply includes the quoted message.
            include: L10n.T("Include in Reply")
        )
        switch kind {
        case .forward:
            // TRANSLATORS: A button of the assistant's bar in the compose window of a forward.
            s.write = L10n.T("Write an Introduction")
            // TRANSLATORS: A checkbox in the compose window: whether the forward includes the forwarded message.
            s.include = L10n.T("Include in Forward")
        case .new, .edit:
            // TRANSLATORS: A button of the assistant's bar in the compose window of a new message.
            s.write = L10n.T("Write a Message")
        case .reply, .replyAll:
            // TRANSLATORS: A button of the assistant's bar in the compose window of a reply.
            s.write = L10n.T("Write a Reply")
        }
        return s
    }

    /// assistant.WriteBarTitleFor: "Assistant · " and the provider in
    /// effect by its brand (`claudeBrand`, `chatGPTBrand`, an `ai`
    /// account's name).
    public static func barTitle(for brand: String) -> String {
        // TRANSLATORS: Heading of the assistant's bar in the compose window; %s is the provider, e.g. "Claude (Anthropic)".
        L10n.T("Assistant · %s", brand)
    }
}
