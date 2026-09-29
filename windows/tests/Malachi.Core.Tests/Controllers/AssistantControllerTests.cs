// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantControllerTests.swift (the
// application's cache of what the Assistant menu may use: the handlers from
// an injected lookup, the registration from a stand-in malachi-mcp) and of
// the logic cases of ui/internal/window/assistant_test.go
// (TestAssistantPanelTarget, TestAssistantNotifies), against
// Malachi.FakeBridge. GTK's TestGtkTarget and TestAssistantPick are about
// Claude Desktop, which GTK does not support and Windows does, so they
// have no counterpart; its menu, chip and settings-group cases are the
// app's, and TestNewestFirst belongs to the window.
//
// Windows differences, as the controller's: every call carries --command
// with the bridge's canonical path (the calls are compared with it), the
// Microsoft Store's Claude Desktop is an empty package directory, and
// Changed follows assistant-target and assistant-claude-path as GTK's
// Assistant does (canRunInAppNeedsThePanelAndClaudeCode counts the change
// at the setting, where Swift counts it at refreshHandlers). A "sleep 0.3"
// of the Swift stand-in is a hold file the test deletes; a test waits for a
// report (UiConditions) or for the bridge's calls to end (IdleAsync), never
// for time.

using System;
using System.Collections.Generic;
using System.IO;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Assistants;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Settings;
using Malachi.Core.Tests.Assistants;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Platform;
using Malachi.FakeBridge;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class AssistantControllerTests
{
    private const string NotRegisteredProblem = "Turn on Register with Claude so that Claude can read your mail";

    /// <summary>What <c>malachi-mcp status --json</c> prints for the two clients.</summary>
    private static string StatusJson(bool desktop, bool code) =>
        "{\"command\": \"C:\\\\Program Files\\\\Malachi Mail\\\\malachi-mcp.exe\", \"clients\": [{\"id\": \"claude-desktop\", \"name\": \"Claude Desktop\", \"present\": true, \"registered\": "
        + (desktop ? "true" : "false")
        + "}, {\"id\": \"claude-code\", \"name\": \"Claude Code\", \"present\": true, \"registered\": "
        + (code ? "true" : "false") + "}]}";

    private static McpStatus Status(bool desktop, bool code) => McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(desktop, code)));

    private static FakeBridgeScript Printing(string json) => new(FakeBridgeStep.Prints(json));

    /// <summary>A pick as "target ok", for comparing.</summary>
    private static string Picked((AssistantTarget Target, bool Ok) p) =>
        (p.Target switch
        {
            AssistantTarget.Code => "code",
            AssistantTarget.App => "app",
            _ => "desktop",
        }) + (p.Ok ? " true" : " false");

    [Fact]
    public async Task NothingIsKnownBeforeTheFirstRefresh()
    {
        using var h = new Harness(Printing(StatusJson(desktop: true, code: true)), ["claude", "claude-cli"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(default, c.Availability(AssistantTarget.Desktop));
            Assert.Equal(default, c.Availability(AssistantTarget.Code));
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: false)));
            Assert.Equal("Claude Desktop is not installed", c.Problem(AssistantTarget.Desktop));
        });
        Assert.Empty(h.Handlers.Asked);
        Assert.Empty(h.Calls);
    }

    [Fact]
    public async Task RefreshLooksUpTheHandlersAtOnceAndTheStatusInTheBackground()
    {
        using var h = new Harness(Printing(StatusJson(desktop: true, code: false)), ["claude"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Refresh();
            Assert.Equal(["claude", "claude-cli"], h.Handlers.Asked);
            Assert.Equal(1, h.Changes); // the handlers changed
            // Not registered until the bridge answered: a file can go, mail not.
            Assert.Equal(new AssistantAvailability(Handler: true, Registered: false), c.Availability(AssistantTarget.Desktop));
            Assert.Equal("desktop true", Picked(c.Pick(needsBridge: false)));
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal(NotRegisteredProblem, c.Problem(AssistantTarget.Desktop));
        });

        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(2, h.Changes);
            Assert.Equal(new AssistantAvailability(Handler: true, Registered: true), c.Availability(AssistantTarget.Desktop));
            Assert.Equal(new AssistantAvailability(Handler: false, Registered: false), c.Availability(AssistantTarget.Code));
            Assert.Equal("desktop true", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("", c.Problem(AssistantTarget.Desktop));
            Assert.Equal("Claude Code is not installed, or has not been used in a terminal yet", c.Problem(AssistantTarget.Code));
        });
        Assert.Equal([h.Call], h.Calls);

        // The same answer again changes nothing.
        await h.Ui.RunAsync(c.Refresh);
        await h.IdleAsync();
        Assert.Equal([h.Call, h.Call], h.Calls);
        Assert.Equal(2, h.Changes);
    }

    [Fact]
    public async Task PickFollowsThePreferenceWithoutFallingBack()
    {
        using var h = new Harness(Printing(StatusJson(desktop: true, code: true)), ["claude", "claude-cli"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(c.Refresh);
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal("desktop true", Picked(c.Pick(needsBridge: true)));
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.Equal("code true", Picked(c.Pick(needsBridge: true)));

            // Claude Code's handler goes away (the menu opens again): still
            // Claude Code, not usable, and the menu says why; Claude Desktop
            // is not opened instead.
            h.Handlers.Installed = ["claude"];
            c.RefreshHandlers();
            Assert.Equal("code false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("code false", Picked(c.Pick(needsBridge: false)));
            Assert.Equal("Claude Code is not installed, or has not been used in a terminal yet", c.Problem(c.Pick(needsBridge: true).Target));
            // Neither: the preference, not usable.
            h.Handlers.Installed = [];
            c.RefreshHandlers();
            Assert.Equal("code false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("code false", Picked(c.Pick(needsBridge: false)));
            // Back to Claude Desktop, which is not installed either now.
            h.Settings.AssistantTarget = AssistantTarget.Desktop;
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("Claude Desktop is not installed", c.Problem(AssistantTarget.Desktop));
        });
    }

    [Fact]
    public async Task AStatusFromTheSettingsCountsAtOnce()
    {
        using var h = new Harness(Printing(StatusJson(desktop: false, code: false)), ["claude", "claude-cli"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            c.RefreshHandlers();
            h.Changes = 0;
            c.Apply(Status(desktop: true, code: true));
            Assert.Equal(1, h.Changes);
            Assert.Equal("desktop true", Picked(c.Pick(needsBridge: true)));
            Assert.True(c.Availability(AssistantTarget.Code).Registered);
            c.Apply(Status(desktop: true, code: true));
            Assert.Equal(1, h.Changes); // an unchanged status is no change
            c.Apply(Status(desktop: false, code: false));
            Assert.Equal(2, h.Changes);
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
        });
        Assert.Empty(h.Calls); // nothing was run
    }

    [Fact]
    public async Task ShownNeedsThePreferenceAndTheBridge()
    {
        using var h = new Harness(null, ["claude"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            // No status yet: not registered, so not shown, whatever the
            // preference says.
            Assert.True(h.Settings.AssistantMenu);
            Assert.False(c.Registered);
            Assert.False(c.Shown);
            c.Apply(Status(desktop: false, code: true));
            Assert.True(c.Registered); // one client is enough
            Assert.True(c.Shown);
            Assert.Equal(1, h.Changes);
            // The preference is reported too, so the menus follow one source.
            h.Settings.AssistantMenu = false;
            Assert.False(c.Shown);
            Assert.Equal(2, h.Changes);
            h.Settings.AssistantMenu = true;
            Assert.True(c.Shown);
            Assert.Equal(3, h.Changes);
            // The bridge unregistered: hidden again, the preference kept.
            c.Apply(Status(desktop: false, code: false));
            Assert.False(c.Shown);
            Assert.True(h.Settings.AssistantMenu);
            Assert.Equal(4, h.Changes);
            // Closed: a preference change is no longer reported.
            c.Close();
            h.Settings.AssistantMenu = false;
            Assert.Equal(4, h.Changes);
        });
    }

    [Fact]
    public async Task AFailedStatusKeepsTheLastKnownOne()
    {
        // Answers the first time, fails afterwards.
        using var flagDir = new TemporaryDirectory();
        var flag = Path.Combine(flagDir.Path, "flag");
        using var h = new Harness(
            new FakeBridgeScript([
                FakeBridgeStep.IfExists(flag, FakeBridgeStep.Fails("read config: permission denied"), [FakeBridgeStep.Touch(flag), .. FakeBridgeStep.Prints(StatusJson(desktop: true, code: false))]),
            ]),
            ["claude"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(c.Refresh);
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() => Assert.Equal("desktop true", Picked(c.Pick(needsBridge: true))));
        await h.Ui.RunAsync(c.Refresh);
        await h.IdleAsync();
        Assert.Equal(2, h.Calls.Count);
        await h.Ui.RunAsync(() =>
        {
            Assert.True(c.Availability(AssistantTarget.Desktop).Registered);
            Assert.Equal("desktop true", Picked(c.Pick(needsBridge: true)));
        });
    }

    [Fact]
    public async Task UnknownClientsAndGarbageCountAsNotRegistered()
    {
        const string Other = """{"command": "C:\\x\\malachi-mcp.exe", "clients": [{"id": "claude-web", "name": "Claude", "present": true, "registered": true}]}""";
        using var h = new Harness(Printing(Other), ["claude", "claude-cli"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(c.Refresh);
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.Availability(AssistantTarget.Desktop).Registered);
            Assert.False(c.Availability(AssistantTarget.Code).Registered);
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
            // An unknown target reads as Claude Desktop.
            Assert.Equal(c.Availability(AssistantTarget.Desktop), c.Availability((AssistantTarget)42));
        });

        using var garbage = new Harness(new FakeBridgeScript([FakeBridgeStep.Stdout("garbage\n")]), ["claude"]);
        var g = await garbage.MakeAsync();
        await garbage.Ui.RunAsync(g.Refresh);
        await garbage.IdleAsync();
        Assert.Single(garbage.Calls);
        await garbage.Ui.RunAsync(() =>
        {
            Assert.Null(g.Status);
            Assert.Equal("desktop false", Picked(g.Pick(needsBridge: true)));
            Assert.Equal("desktop true", Picked(g.Pick(needsBridge: false)));
        });
    }

    [Fact]
    public async Task WithoutABridgeOnlyTheFileHandOffWorks()
    {
        using var h = new Harness(null, ["claude-cli"]);
        var c = await h.MakeAsync();
        await h.Ui.RunAsync(() =>
        {
            c.Refresh();
            c.Refresh();
        });
        await h.IdleAsync();
        await h.Ui.RunAsync(() =>
        {
            Assert.Null(c.Status);
            Assert.Equal(["claude", "claude-cli", "claude", "claude-cli"], h.Handlers.Asked);
            // Claude Desktop is preferred and not installed: nothing, not even
            // the file, goes to Claude Code instead.
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("desktop false", Picked(c.Pick(needsBridge: false)));
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.Equal("code false", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("code true", Picked(c.Pick(needsBridge: false)));
            Assert.Equal(NotRegisteredProblem, c.Problem(AssistantTarget.Code));
        });
    }

    [Fact]
    public async Task ARefreshDuringACallIsSkipped()
    {
        using var holdDir = new TemporaryDirectory();
        var hold = Path.Combine(holdDir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(
            new FakeBridgeScript([FakeBridgeStep.Hold(hold, 120_000), .. FakeBridgeStep.Prints(StatusJson(desktop: true, code: true))]),
            ["claude"]);
        var c = await h.MakeAsync();
        try
        {
            await h.Ui.RunAsync(() =>
            {
                c.Refresh();
                c.Refresh();
                c.Refresh();
            });
        }
        finally
        {
            File.Delete(hold);
        }
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.IdleAsync();
        Assert.Equal([h.Call], h.Calls);
    }

    [Fact]
    public async Task CloseDropsLateRepliesAndObservers()
    {
        using var holdDir = new TemporaryDirectory();
        var hold = Path.Combine(holdDir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(
            new FakeBridgeScript([FakeBridgeStep.Hold(hold, 120_000), .. FakeBridgeStep.Prints(StatusJson(desktop: true, code: true))]),
            ["claude"]);
        var c = await h.MakeAsync();
        try
        {
            await h.Ui.RunAsync(() =>
            {
                c.Refresh();
                Assert.Equal(1, h.Changes);
            });
            // The bridge runs (held) when the controller closes.
            await Eventually.Holds(() => h.Calls.Count == 1, TimeSpan.FromSeconds(60), "the bridge did not start");
            await h.Ui.RunAsync(c.Close);
            // The status call ends with the controller (its run is killed).
            await h.IdleAsync();
        }
        finally
        {
            File.Delete(hold);
        }
        await h.Ui.RunAsync(() =>
        {
            Assert.Null(c.Status);
            Assert.Equal(1, h.Changes);
            c.Refresh();
            c.Apply(Status(desktop: true, code: true));
            Assert.Null(c.Status);
            Assert.Equal(1, h.Changes);
        });
        Assert.Equal(2, h.Handlers.Asked.Count);
        Assert.Single(h.Calls);
    }

    /// <summary>
    /// The In App target: its "handler" is Claude Code found with the bridge
    /// beside the application, its registration any client's; the panel
    /// exists while the Assistant is shown and In App chosen.
    /// </summary>
    [Fact]
    public async Task TheAppTargetNeedsClaudeCodeAndTheBridge()
    {
        using var claudeDir = new TemporaryDirectory();
        var claude = Path.Combine(claudeDir.Path, "claude.exe");
        File.WriteAllBytes(claude, []);
        using var h = new Harness(Printing(StatusJson(desktop: false, code: true)), ["claude"]);
        var locator = Locator(h, claudeDir.Path);
        var c = await h.MakeAsync(locator: locator);
        await h.Ui.RunAsync(() =>
        {
            h.Settings.AssistantTarget = AssistantTarget.App;
            c.Refresh();
            Assert.Equal(["claude", "claude-cli"], h.Handlers.Asked); // the panel has no link to look up
            Assert.Equal(default, c.Availability(AssistantTarget.App));
            Assert.Equal("Claude Code was not found on this computer", c.Problem(AssistantTarget.App));
            Assert.Equal("app false", Picked(c.Pick(needsBridge: true)));

            h.Settings.AssistantClaudePath = claude;
            c.Refresh();
        });
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() =>
        {
            Assert.Equal(new AssistantAvailability(Handler: true, Registered: true), c.Availability(AssistantTarget.App));
            Assert.Equal("app true", Picked(c.Pick(needsBridge: true)));
            Assert.Equal("", c.Problem(AssistantTarget.App));
            Assert.True(c.Shown && c.PanelShown);
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.False(c.PanelShown);
            h.Settings.AssistantTarget = AssistantTarget.App;
            h.Settings.AssistantMenu = false;
            Assert.False(c.PanelShown);
            h.Settings.AssistantMenu = true;

            // Registered nowhere: the panel is gone with the Assistant.
            c.Apply(Status(desktop: false, code: false));
            Assert.Equal(new AssistantAvailability(Handler: true, Registered: false), c.Availability(AssistantTarget.App));
            Assert.False(c.Shown || c.PanelShown);
        });

        // No bridge beside the application: never available.
        var bare = await h.MakeAsync(locator: locator, bare: true);
        await h.Ui.RunAsync(() =>
        {
            bare.RefreshHandlers();
            Assert.False(bare.Availability(AssistantTarget.App).Handler);
        });
    }

    /// <summary>
    /// The one-shot requests (the compose window's rewrite, the search in the
    /// user's own words) exist while the panel does and Claude Code was
    /// found; they need no bridge of their own, and a change of what was
    /// found is reported.
    /// </summary>
    [Fact]
    public async Task CanRunInAppNeedsThePanelAndClaudeCode()
    {
        using var claudeDir = new TemporaryDirectory();
        var claude = Path.Combine(claudeDir.Path, "claude.exe");
        File.WriteAllBytes(claude, []);
        using var h = new Harness(Printing(StatusJson(desktop: true, code: false)), []);
        var locator = Locator(h, claudeDir.Path);
        var c = await h.MakeAsync(locator: locator);
        await h.Ui.RunAsync(() =>
        {
            Assert.False(c.ClaudeFound || c.CanRunInApp);
            h.Settings.AssistantTarget = AssistantTarget.App;
            c.Refresh();
        });
        await h.WhenAsync(() => c.Status is not null, "the status");
        await h.Ui.RunAsync(() =>
        {
            Assert.True(c.PanelShown && !c.ClaudeFound && !c.CanRunInApp, "Claude Code not found");

            // Windows, as GTK: another claude chosen looks the handlers up
            // again at once, so the change is reported at the setting.
            var before = h.Changes;
            h.Settings.AssistantClaudePath = claude;
            Assert.True(c.ClaudeFound && c.CanRunInApp);
            Assert.Equal(before + 1, h.Changes);
            c.RefreshHandlers();
            Assert.Equal(before + 1, h.Changes); // nothing changed

            h.Settings.AssistantTarget = AssistantTarget.Desktop;
            Assert.False(c.CanRunInApp);
            h.Settings.AssistantTarget = AssistantTarget.App;
            h.Settings.AssistantMenu = false;
            Assert.False(c.CanRunInApp);
            h.Settings.AssistantMenu = true;
            Assert.True(c.CanRunInApp);
            c.Apply(Status(desktop: false, code: false));
            Assert.False(c.CanRunInApp, "the Assistant is off while nothing is registered");
        });

        // Claude Code found without the bridge beside the application: the
        // panel's target is not available, the one-shot requests would be,
        // but the Assistant is not shown without a registration anyway.
        var bare = await h.MakeAsync(locator: locator, bare: true);
        await h.Ui.RunAsync(() =>
        {
            bare.RefreshHandlers();
            Assert.True(bare.ClaudeFound && !bare.Availability(AssistantTarget.App).Handler && !bare.CanRunInApp);
        });
    }

    [Fact]
    public async Task ACancelledObserverIsNotCalled()
    {
        using var h = new Harness(null, []);
        var c = await h.MakeAsync(observe: false);
        await h.Ui.RunAsync(() =>
        {
            var a = 0;
            var b = 0;
            EventHandler first = (_, _) => a++;
            c.Changed += first;
            c.Changed += (_, _) => b++;
            h.Handlers.Installed = ["claude"];
            c.RefreshHandlers();
            c.Changed -= first;
            h.Handlers.Installed = ["claude", "claude-cli"];
            c.RefreshHandlers();
            Assert.Equal(1, a);
            Assert.Equal(2, b);
        });
    }

    /// <summary>
    /// assistant_test.go TestAssistantPanelTarget: the panel runs while
    /// Claude Code and the bridge were found and the bridge is registered in
    /// any client; it exists while the Assistant is shown and In App chosen.
    /// Its attachment item is there for the types the bridge reads; Claude
    /// Code in a terminal takes any file.
    /// </summary>
    [Fact]
    public async Task AssistantPanelTarget()
    {
        using var claudeDir = new TemporaryDirectory();
        var claude = Path.Combine(claudeDir.Path, "claude.exe");
        File.WriteAllBytes(claude, []);
        using var h = new Harness(null, [], bridge: Path.Combine(claudeDir.Path, "missing", "malachi-mcp.exe"));
        var c = await h.MakeAsync(locator: Locator(h, claudeDir.Path));
        await h.Ui.RunAsync(() =>
        {
            h.Settings.AssistantTarget = AssistantTarget.App;
            h.Settings.AssistantClaudePath = claude;
            c.Apply(Status(desktop: true, code: false)); // registered in Claude Desktop only
            Assert.Equal("app true", Picked(c.Pick(needsBridge: true)));
            Assert.True(c.PanelShown);
            Assert.Equal("", c.Problem(AssistantTarget.App));
            Assert.True(c.CanAskFile("text/plain; charset=utf-8"));
            Assert.False(c.CanAskFile("application/pdf"));

            File.Delete(claude);
            c.RefreshHandlers();
            Assert.False(c.Pick(needsBridge: true).Ok);
            Assert.Equal("Claude Code was not found on this computer", c.Problem(AssistantTarget.App));
            Assert.False(c.CanAskFile("text/plain"));

            File.WriteAllBytes(claude, []);
            c.RefreshHandlers();
            c.Apply(Status(desktop: false, code: false));
            Assert.False(c.Pick(needsBridge: true).Ok);
            Assert.False(c.PanelShown);

            c.Apply(Status(desktop: false, code: true));
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.False(c.PanelShown);
            h.Handlers.Installed = ["claude-cli"];
            c.RefreshHandlers();
            Assert.True(c.CanAskFile("application/pdf"));
        });
    }

    /// <summary>
    /// assistant_test.go TestAssistantNotifies: observers hear a new status,
    /// new handlers and the assistant keys, and nothing that changes nothing.
    /// </summary>
    [Fact]
    public async Task AssistantNotifies()
    {
        using var h = new Harness(null, []);
        var c = await h.MakeAsync(observe: false);
        await h.Ui.RunAsync(() =>
        {
            var calls = 0;
            EventHandler count = (_, _) => calls++;
            c.Changed += count;
            c.Apply(Status(desktop: true, code: false));
            c.Apply(Status(desktop: true, code: false));
            Assert.Equal(1, calls);
            c.Apply(Status(desktop: true, code: true));
            h.Settings.AssistantMenu = false;
            h.Settings.AssistantTarget = AssistantTarget.Code;
            Assert.Equal(4, calls);
            // A key that is set to what it is, and one the controller does not follow, change nothing.
            h.Settings.AssistantTarget = AssistantTarget.Code;
            h.Settings.AssistantModel = AssistantModel.Opus;
            Assert.Equal(4, calls);
            c.Changed -= count;
            c.Apply(Status(desktop: false, code: false));
            Assert.Equal(4, calls);
        });
    }

    /// <summary>
    /// Windows, as GTK's NewAssistant: another claude chosen looks the
    /// handlers up again, since whether the panel can run changes with it.
    /// </summary>
    [Fact]
    public async Task AnotherClaudeLooksTheHandlersUpAgain()
    {
        using var claudeDir = new TemporaryDirectory();
        var claude = Path.Combine(claudeDir.Path, "claude.exe");
        File.WriteAllBytes(claude, []);
        using var h = new Harness(null, ["claude"], bridge: Path.Combine(claudeDir.Path, "missing", "malachi-mcp.exe"));
        var c = await h.MakeAsync(locator: Locator(h, claudeDir.Path));
        await h.Ui.RunAsync(() =>
        {
            Assert.Empty(h.Handlers.Asked);
            h.Settings.AssistantClaudePath = claude;
            Assert.Equal(["claude", "claude-cli"], h.Handlers.Asked);
            Assert.True(c.ClaudeFound);
            Assert.True(c.Availability(AssistantTarget.App).Handler);
            Assert.Equal(1, h.Changes);
            h.Settings.AssistantClaudePath = "";
            Assert.False(c.ClaudeFound);
            Assert.Equal(2, h.Changes);
        });
    }

    // A locator that finds only what is inside dir (the setting's path).
    private static ClaudeCodeLocator Locator(Harness h, string dir)
    {
        var prefix = dir + @"\";
        return new ClaudeCodeLocator(
            h.Settings,
            CannedStreamJson.Environment(("USERPROFILE", dir), ("PATH", "")),
            usable: p => p.StartsWith(prefix, StringComparison.OrdinalIgnoreCase) && ClaudeCodeLocator.IsExecutableFile(p));
    }

    /// <summary>The handler lookup: the schemes asked, the answers scripted.</summary>
    private sealed class HandlerLookup(IEnumerable<string> installed)
    {
        public HashSet<string> Installed { get; set; } = [.. installed];

        public List<string> Asked { get; } = [];

        public bool Lookup(string scheme)
        {
            Asked.Add(scheme);
            return Installed.Contains(scheme);
        }
    }

    /// <summary>A stand-in bridge (or none), the handler lookup and the preferences, on the test's UI thread.</summary>
    private sealed class Harness : IDisposable
    {
        private readonly TemporaryDirectory dir = new();
        private readonly TemporaryDirectory packages = new();
        private readonly UiConditions conditions = new();
        private readonly string? bridge;

        public Harness(FakeBridgeScript? script, IEnumerable<string> installed, string? bridge = null)
        {
            this.bridge = script is null ? bridge : script.CreateIn(dir.Path);
            Handlers = new HandlerLookup(installed);
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        public SettingsStore Settings { get; } = new(new InMemorySettingsBackend(), null);

        public HandlerLookup Handlers { get; }

        /// <summary>The changes reported to the observer of <see cref="MakeAsync"/>.</summary>
        public int Changes { get; set; }

        /// <summary>The arguments of every invocation of the bridge so far, one line each.</summary>
        public IReadOnlyList<string> Calls => FakeBridgeScript.Calls(dir.Path);

        /// <summary>The line a status call leaves in <see cref="Calls"/>.</summary>
        public string Call => "status --json --command " + McpRegistrationController.CanonicalPath(bridge!);

        /// <summary>A controller on the UI thread over the harness's bridge (none when <paramref name="bare"/>), observed unless asked not to.</summary>
        public Task<AssistantController> MakeAsync(ClaudeCodeLocator? locator = null, bool observe = true, bool bare = false) => Ui.RunAsync(() =>
        {
            var c = new AssistantController(
                bare ? null : bridge,
                Settings,
                Handlers.Lookup,
                timeout: TimeSpan.FromSeconds(60),
                locator: locator,
                claudeDesktop: new ClaudeDesktopPackage { PackagesDirectory = packages.Path },
                pending: Pending);
            if (observe)
            {
                c.Changed += (_, _) =>
                {
                    Changes++;
                    conditions.Changed();
                };
            }
            return c;
        });

        public Task WhenAsync(Func<bool> condition, string what) => conditions.WhenAsync(Ui, condition, what);

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, timeout: TimeSpan.FromSeconds(60));

        public void Dispose()
        {
            Ui.Dispose();
            packages.Dispose();
            dir.Dispose();
        }
    }
}
