// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantButtonTests.swift
// (AssistantButtonTests), the counterpart of
// ui/internal/assistant/button_test.go (TestButtonOpensPanel,
// TestPanelActions). Go's unknown target is a value outside the enum here
// ((AssistantTarget)42), as in AssistantTests.

using Malachi.Core.Assistants;
using Malachi.Core.Settings;
using Xunit;

namespace Malachi.Core.Tests.Assistants;

public sealed class AssistantButtonTests
{
    [Theory]
    [InlineData(AssistantTarget.App, true, true)]
    [InlineData(AssistantTarget.App, false, false)]
    [InlineData(AssistantTarget.Desktop, true, false)]
    [InlineData(AssistantTarget.Code, true, false)]
    [InlineData(AssistantTarget.Desktop, false, false)]
    [InlineData(AssistantTarget.Code, false, false)]
    [InlineData((AssistantTarget)42, true, false)]
    public void ButtonOpensPanel(AssistantTarget target, bool hasPanel, bool want)
    {
        Assert.Equal(want, Assistant.ButtonOpensPanel(target, hasPanel));
    }

    [Fact]
    public void PanelActions()
    {
        Assert.Equal(
            [AssistantAction.Summarize, AssistantAction.DraftReply, AssistantAction.Tasks, AssistantAction.Unread],
            Assistant.PanelActions);
        foreach (var a in Assistant.PanelActions)
        {
            Assert.NotEqual("", Assistant.Label(a));
        }
    }
}
