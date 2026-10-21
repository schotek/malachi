// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantRequestTests.swift
// (oneShotsCarryTheProfile); GTK: ui/internal/assistantpanel/
// profile_test.go (TestOneShotsCarryTheProfile): a rewrite carries the
// profile as the window scoped it, a search carries it without account
// notes.

using System;
using System.Collections.Generic;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Tests.Assistants;
using Malachi.FakeClaude;
using Xunit;
using SearchOutcome = Malachi.Core.Controllers.SearchConversion.Outcome;

namespace Malachi.Core.Tests.Controllers;

public sealed partial class AssistantRequestTests
{
    private static UserProfile ProfileFixture() => new("Píšu stručně.", Malachi.Core.Api.ProfileGender.Feminine,
    [
        new ProfileNote("acc_1", "Studio", "s@example.org", "Podepisuj jako studio."),
        new ProfileNote("acc_2", "Osobní", "me@example.org", "Tykej."),
    ]);

    [Fact]
    public async Task OneShotsCarryTheProfile()
    {
        RequireWindows();
        await using var h = await Harness.CreateAsync(Fake(
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.Result("Dobrý den.")),
            CannedStreamJson.Turn(CannedStreamJson.Init, CannedStreamJson.StructuredResult("""{"query":"is:unread"}"""))));
        var rewrite = await h.Ui.RunAsync(() => new ComposeRewriteController(h.Request, h.Pending)
        {
            Profile = () => ProfileFixture().For("acc_1"),
        });
        await h.Ui.RunAsync(() =>
        {
            rewrite.StateChanged += (_, _) => h.Changed();
            Assert.True(rewrite.Start(AssistantRewrite.Politer, "", "Ahoj."));
        });
        await h.WhenAsync(() => !rewrite.Running, "the end of the rewrite");
        var system = SystemPromptOf(FakeClaudeScript.Args(h.Dir.Path));
        Assert.Equal(Assistant.WithProfile(Assistant.RewriteSystemPrompt(), ProfileFixture().For("acc_1")), system);
        Assert.Contains("Podepisuj jako studio.", system, StringComparison.Ordinal);
        Assert.DoesNotContain("Tykej.", system, StringComparison.Ordinal);

        SearchOutcome? got = null;
        var search = await h.Ui.RunAsync(() => new SearchConversion(h.Request, h.Pending)
        {
            Today = () => "2026-09-29",
            Profile = ProfileFixture,
        });
        await h.Ui.RunAsync(() => Assert.True(search.Convert("nepřečtené", o =>
        {
            got = o;
            h.Changed();
        })));
        await h.WhenAsync(() => got is not null, "the query");
        system = SystemPromptOf(FakeClaudeScript.Args(h.Dir.Path));
        Assert.Contains("Píšu stručně.", system, StringComparison.Ordinal);
        Assert.DoesNotContain("For the account", system, StringComparison.Ordinal);
    }

    // The argument after --system-prompt.
    private static string SystemPromptOf(IReadOnlyList<string> args)
    {
        for (var i = 0; i + 1 < args.Count; i++)
        {
            if (args[i] == "--system-prompt")
            {
                return args[i + 1];
            }
        }
        throw new InvalidOperationException("no --system-prompt");
    }
}
