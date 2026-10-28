// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation

/// DEVELOPMENT AID, not a feature: the pure half of the app's
/// `MALACHI_START` steps (MalachiMail `MainWindowController+DevStart.swift`
/// and `+DevStartAssistant.swift`), here so that it is tested: when the
/// steps run, how a step is split into its name and value, and what the
/// values of the assistant's steps mean. Nothing here runs without the
/// variable, and nothing of it is translated.
public enum DevelopmentStart {
    /// When each step starts, in seconds from the first: `interval` after
    /// the step before it, or the seconds a `wait=<n>` step asks for after
    /// that one (a step that takes longer than the interval, such as a
    /// triage run, is followed by `wait=`).
    public static func schedule(_ steps: [String], interval: Double) -> [Double] {
        var starts: [Double] = []
        var at = 0.0
        for step in steps {
            starts.append(at)
            at += waitSeconds(step) ?? interval
        }
        return starts
    }

    /// The seconds a `wait=<n>` step asks for; nil for any other step or
    /// a number that is not one.
    public static func waitSeconds(_ step: String) -> Double? {
        let (name, value) = split(step)
        guard name == "wait", let n = Double(value), n >= 0 else { return nil }
        return n
    }

    /// A step's name and value: what is before the first "=" and after
    /// it ("" without one), so a value may hold "=" itself.
    public static func split(_ step: String) -> (name: String, value: String) {
        guard let eq = step.firstIndex(of: "=") else { return (step, "") }
        return (String(step[..<eq]), String(step[step.index(after: eq)...]))
    }

    /// The message action of `assistant=<value>`: summarize, tasks,
    /// draft-reply, ask.
    public static func assistantAction(_ value: String) -> Assistant.Action? {
        switch value {
        case "summarize": return .summarize
        case "tasks": return .tasks
        case "draft-reply": return .draftReply
        case "ask": return .ask
        default: return nil
        }
    }

    /// The preset of `compose-rewrite=<value>`: politer, shorter, fix,
    /// english.
    public static func rewrite(_ value: String) -> Assistant.Rewrite? {
        switch value {
        case "politer": return .politer
        case "shorter": return .shorter
        case "fix": return .fix
        case "english": return .toEnglish
        default: return nil
        }
    }

    /// What `compose-new=<to>|<subject>|<text>` prefills: the recipient
    /// ("Name <address>" or an address; none when empty), the subject and
    /// the text's paragraphs (`\n` in the text separates them). nil
    /// without the two separators.
    public static func composeFields(_ value: String) -> (to: Address?, subject: String, paragraphs: [String])? {
        let parts = value.split(separator: "|", maxSplits: 2, omittingEmptySubsequences: false).map(String.init)
        guard parts.count == 3 else { return nil }
        var to: Address?
        let recipient = parts[0].trimmingCharacters(in: .whitespaces)
        if !recipient.isEmpty {
            if let lt = recipient.firstIndex(of: "<"), recipient.hasSuffix(">") {
                let name = recipient[..<lt].trimmingCharacters(in: .whitespaces)
                let address = String(recipient[recipient.index(after: lt)...].dropLast())
                to = Address(name: name.isEmpty ? nil : name, address: address)
            } else {
                to = Address(address: recipient)
            }
        }
        let paragraphs = parts[2].replacingOccurrences(of: "\\n", with: "\n")
            .split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
        return (to, parts[1], paragraphs)
    }
}
