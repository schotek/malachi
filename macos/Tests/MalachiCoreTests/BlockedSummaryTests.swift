// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

import Foundation
import Testing
@testable import MalachiCore

/// ui/internal/compose/draft_test.go TestBlockedSummary (English catalogue).
struct BlockedSummaryTests {
    @Test func blockedSummaryText() {
        #expect(blockedSummary(BlockedContent()) == "")
        #expect(blockedSummary(BlockedContent(remoteImages: 2, scripts: 1)) == "3 unsafe elements were removed from the message")
        #expect(blockedSummary(BlockedContent(forms: 1)) == "1 unsafe element was removed from the message")
        // Every counter is summed.
        let all = BlockedContent(remoteImages: 1, remoteStyles: 1, remoteFonts: 1, scripts: 1, forms: 1,
                                 eventHandlers: 1, dangerousUrls: 1, embeddedFrames: 1, trackingPixels: 1)
        #expect(blockedSummary(all) == "9 unsafe elements were removed from the message")
    }
}
