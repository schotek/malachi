// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Testing
@testable import MalachiCore

// The counterpart of ui/internal/assistant/button_test.go.

@Suite struct AssistantButtonTests {
    @Test func buttonOpensPanel() {
        let cases: [(Assistant.Target, Bool, Bool)] = [
            (.app, true, true),
            (.app, false, false),
            (.desktop, true, false),
            (.code, true, false),
            (.desktop, false, false),
            (.code, false, false),
            (Assistant.Target(""), true, false),
            (Assistant.Target("unknown"), true, false),
        ]
        for (target, hasPanel, want) in cases {
            #expect(
                Assistant.buttonOpensPanel(target, hasPanel: hasPanel) == want,
                "ButtonOpensPanel(\(target.rawValue), \(hasPanel))")
        }
    }

    @Test func panelActions() {
        #expect(Assistant.panelActions == [.summarize, .draftReply, .tasks, .unread])
        for a in Assistant.panelActions {
            #expect(!Assistant.label(a).isEmpty, "no label for \(a)")
        }
    }
}
