// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of MCPRegistrationTests in macos/Tests/MalachiCoreTests/
// MCPRegistrationTests.swift (its BridgeRunnerTests are
// Platform/BridgeRunnerTests.cs), and of the report cases of
// ui/internal/mcpsetup/mcpsetup_test.go that Swift did not port
// (TestQueryParsesReport, TestRegistered, TestInstallNoClient,
// TestExitReasonIsFirstLineBounded, TestExitWithoutReason, TestBadReport,
// and TestSubcommandsPassJSONFlag with the arguments Windows adds), against
// Malachi.FakeBridge instead of the #!/bin/sh stand-in.
//
// Windows differences, as the controller's: a failed status whose repeats
// are used up with no state known, and a missing bridge, go into the
// group's description (U2, U3; Swift logs the one and toasts the other),
// every call carries --command with the bridge's canonical path and, with
// the Microsoft Store's Claude Desktop installed, --claude-desktop-config
// (both tested here), and a kill reads as -1 or an NTSTATUS. A sleep of the
// Swift scripts is a hold file the test deletes; the test waits for the
// tracked work (IdleAsync) or for a report (UiConditions), never for time,
// and the repeats of a failed status wait on a FakeTimeProvider the test
// advances. As in Swift, a controller repeats no failed status unless the
// test gives it the delays.
//
// Adapted to the repeats and Adopt (the Swift suite changed the same way):
// LoadShowsTheStatusAndEnablesTheRow (a status check of a known state keeps
// the row sensitive: Enabled is [true], not [true, false, true]) and
// AFailedStatusGoesIntoTheDescriptionAndTheRowStaysInsensitive (without
// repeats the description comes at once, as before).

using System;
using System.Collections.Generic;
using System.Diagnostics;
using System.IO;
using System.Linq;
using System.Text;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Malachi.Core.Controllers.Infrastructure;
using Malachi.Core.Platform;
using Malachi.Core.Tests.Fixtures;
using Malachi.Core.Tests.Platform;
using Malachi.FakeBridge;
using Microsoft.Extensions.Time.Testing;
using Xunit;

namespace Malachi.Core.Tests.Controllers;

[Collection(typeof(McpRegistrationProcesses))]
public sealed class McpRegistrationTests
{
    private const string BridgeCommand = @"C:\Users\u\AppData\Local\Programs\Malachi Mail\malachi-mcp.exe";

    // mcpsetup_test.go: the report, the no-client line and a first line of
    // 100 three-byte runes, whose 200-byte cap falls inside a rune.
    private const string GoReport = """
        {"command":"/opt/malachi/bin/malachi-mcp","clients":[
        {"id":"claude-desktop","name":"Claude Desktop","present":true,"registered":false,
         "path":"/home/u/.config/Claude/claude_desktop_config.json"},
        {"id":"claude-code","name":"Claude Code","present":true,"registered":true,
         "path":"/home/u/.claude.json","other":"/usr/local/bin/malachi-mcp"}]}
        """;

    private const string NoClientLine = "no Claude app found: neither Claude Desktop nor Claude Code is installed";

    private static readonly string LongReason = string.Concat(Enumerable.Repeat("\u20AC", 100));

    /// <summary>
    /// The JSON the bridge prints: Claude Desktop registered as asked, Claude
    /// Code present but never registered.
    /// </summary>
    private static string StatusJson(bool registered) =>
        "{\"command\": \"" + BridgeCommand.Replace(@"\", @"\\", StringComparison.Ordinal) + "\", \"clients\": [{\"id\": \"claude-desktop\", \"name\": \"Claude Desktop\", \"present\": true, \"registered\": "
        + (registered ? "true" : "false")
        + ", \"path\": \"C:\\\\Users\\\\u\\\\AppData\\\\Roaming\\\\Claude\\\\claude_desktop_config.json\"}, {\"id\": \"claude-code\", \"name\": \"Claude Code\", \"present\": true, \"registered\": false, \"path\": \"C:\\\\Users\\\\u\\\\.claude.json\"}]}";

    private static IReadOnlyList<FakeBridgeStep> Prints(bool registered) => FakeBridgeStep.Prints(StatusJson(registered));

    [Fact]
    public void StatusDecodesWhatTheBridgePrints()
    {
        var s = McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(true)));
        Assert.Equal(BridgeCommand, s.Command);
        Assert.Equal(["claude-desktop", "claude-code"], s.Clients.Select(c => c.Id));
        Assert.True(s.Clients[0].Registered && s.Clients[0].Present);
        Assert.EndsWith("claude_desktop_config.json", s.Clients[0].Path, StringComparison.Ordinal);
        Assert.Null(s.Clients[0].Other);
        Assert.True(s.IsRegistered);
        var off = McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(false)));
        Assert.False(off.IsRegistered);

        // Tolerant: unknown keys, an absent path, `other`, a null client list.
        var odd = """
            {"command": "/x/malachi-mcp", "version": "0.2", "clients": [{"id": "claude-code", "name": "Claude Code", "present": false, "registered": false, "other": "/old/malachi-mcp", "extra": 1}]}
            """;
        var o = McpStatus.Decode(Encoding.UTF8.GetBytes(odd));
        Assert.Single(o.Clients);
        Assert.Null(o.Clients[0].Path);
        Assert.Equal("/old/malachi-mcp", o.Clients[0].Other);
        Assert.False(o.IsRegistered);
        var none = McpStatus.Decode("""{"command": "/x", "clients": null}"""u8);
        Assert.Empty(none.Clients);
        Assert.False(none.IsRegistered);
    }

    [Fact]
    public async Task LoadShowsTheStatusAndEnablesTheRow()
    {
        using var h = new Harness(new(Prints(true)));
        var (c, rec) = await h.MakeControllerAsync();
        Assert.False(c.IsEnabled);
        Assert.False(c.IsRegistered);
        Assert.Null(c.Status);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
        Assert.Equal(BridgeCommand, c.Status?.Command);
        Assert.Equal([true], rec.Registered);
        Assert.Equal([true], rec.Enabled);
        Assert.Empty(rec.Toasts);
        Assert.Empty(rec.Descriptions);
        Assert.Equal([h.Call("status")], h.Calls);

        // Asked again (the page came up again): a fresh status, and the row
        // of a known state stays sensitive meanwhile.
        await h.Ui.RunAsync(() =>
        {
            c.Load();
            Assert.True(c.IsEnabled);
        });
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal([h.Call("status"), h.Call("status")], h.Calls);
        Assert.Equal([true], rec.Enabled);
        Assert.Equal([true, true], rec.Registered);
    }

    /// <summary>
    /// Swift <c>aFailedStatusIsOnlyLoggedAndTheRowStaysInsensitive</c>: on
    /// Windows the reason goes into the group's description, as in GTK (U3),
    /// and a status that answers later puts the page's own text back.
    /// </summary>
    [Fact]
    public async Task AFailedStatusGoesIntoTheDescriptionAndTheRowStaysInsensitive()
    {
        // Fails the first time, answers the second (the page came up again;
        // no automatic repeat here).
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        using var h = new Harness(new([
            FakeBridgeStep.IfExists(flag, Prints(true), [FakeBridgeStep.Touch(flag), .. FakeBridgeStep.Fails("read config: permission denied")]),
        ]));
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.False(c.IsEnabled, "the row stays insensitive");
        Assert.False(c.IsRegistered);
        Assert.Null(c.Status);
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied"], rec.Descriptions);
        Assert.Equal("The MCP bridge did not answer: read config: permission denied", c.Description);
        Assert.Empty(rec.Toasts);
        Assert.Empty(rec.Registered);
        Assert.Empty(rec.Enabled);

        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
        Assert.Equal([true], rec.Registered);
        Assert.Equal([true], rec.Enabled);
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied", null], rec.Descriptions);
        Assert.Null(c.Description);
        Assert.Equal([h.Call("status"), h.Call("status")], h.Calls);
    }

    [Fact]
    public async Task AFailedStatusIsRepeatedShortly()
    {
        // Fails the first time (a Claude app rewriting its file), answers the
        // repeat.
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        using var h = new Harness(new([
            FakeBridgeStep.IfExists(flag, Prints(true), [FakeBridgeStep.Touch(flag), .. FakeBridgeStep.Fails("parse config: unexpected end of JSON input")]),
        ]));
        var (c, rec) = await h.MakeControllerAsync(statusRetryDelays: [TimeSpan.FromMilliseconds(100)]);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(99));
        Assert.Equal([h.Call("status")], h.Calls);
        // Windows: nothing is said while the repeat waits.
        Assert.Empty(rec.Descriptions);
        Assert.False(c.IsEnabled);

        await h.AdvanceAsync(TimeSpan.FromMilliseconds(1));
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
        Assert.Equal([h.Call("status"), h.Call("status")], h.Calls);
        Assert.Empty(rec.Toasts);
        Assert.Equal([true], rec.Registered);
        Assert.Empty(rec.Descriptions);
    }

    [Fact]
    public async Task AFailedStatusOfAKnownStateKeepsItAndGivesUpAfterTheRepeats()
    {
        // Answers the first time, fails from then on.
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        using var h = new Harness(new([
            FakeBridgeStep.IfExists(flag, FakeBridgeStep.Fails("parse config: unexpected end of JSON input"), [FakeBridgeStep.Touch(flag), .. Prints(true)]),
        ]));
        var delay = TimeSpan.FromMilliseconds(50);
        var (c, rec) = await h.MakeControllerAsync(statusRetryDelays: [delay, delay]);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        // The check and its two repeats fail; the known state stays shown and
        // the row sensitive throughout.
        Assert.Equal(2, h.Calls.Count);
        await h.AdvanceAsync(delay);
        Assert.Equal(3, h.Calls.Count);
        Assert.True(c.IsEnabled);
        await h.AdvanceAsync(delay);
        Assert.Equal(4, h.Calls.Count);
        await h.AdvanceAsync(TimeSpan.FromMilliseconds(300));
        Assert.True(h.Calls.Count == 4, "no repeat after the last delay");
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
        Assert.True(c.Status?.IsRegistered);
        Assert.Equal([true], rec.Enabled);
        Assert.Equal([true], rec.Registered);
        Assert.Empty(rec.Toasts);
        // Windows: a state is known, so the group says nothing.
        Assert.Empty(rec.Descriptions);
    }

    /// <summary>
    /// Windows (GTK bindMCP): only when the repeats are used up and no state
    /// is known does the group say why; a later status that answers puts the
    /// page's own text back.
    /// </summary>
    [Fact]
    public async Task TheDescriptionWaitsForTheRepeats()
    {
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        using var h = new Harness(new([
            FakeBridgeStep.IfExists(flag, Prints(false), FakeBridgeStep.Fails("read config: permission denied")),
        ]));
        var delay = TimeSpan.FromSeconds(1);
        var (c, rec) = await h.MakeControllerAsync(statusRetryDelays: [delay]);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Empty(rec.Descriptions);
        await h.AdvanceAsync(delay);
        Assert.Equal([h.Call("status"), h.Call("status")], h.Calls);
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied"], rec.Descriptions);
        Assert.False(c.IsEnabled);
        Assert.Empty(rec.Toasts);

        // The page comes up again and the bridge answers.
        File.WriteAllBytes(flag, []);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied", null], rec.Descriptions);
        Assert.Equal([false], rec.Registered);
    }

    /// <summary>
    /// Windows: a call made while a repeat waits cancels it (its answer is
    /// newer), and so does closing the page.
    /// </summary>
    [Fact]
    public async Task ANewerCallCancelsTheRepeat()
    {
        using var dir = new TemporaryDirectory();
        var flag = Path.Combine(dir.Path, "flag");
        using var h = new Harness(new(
            [FakeBridgeStep.IfExists(flag, Prints(true), [FakeBridgeStep.Touch(flag), .. FakeBridgeStep.Fails("parse config: unexpected end of JSON input")])],
            install: Prints(true)));
        var delay = TimeSpan.FromSeconds(1);
        var (c, rec) = await h.MakeControllerAsync(statusRetryDelays: [delay, delay]);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        await h.AdvanceAsync(delay);
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls);
        Assert.True(c.IsRegistered);
        Assert.Equal([true], rec.Registered);

        // The next failure waits for its repeat; the page closes meanwhile.
        File.Delete(flag);
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(3, h.Calls.Count);
        await h.Ui.RunAsync(c.Close);
        await h.AdvanceAsync(delay);
        Assert.Equal(3, h.Calls.Count);
    }

    [Fact]
    public async Task AdoptShowsAStatusFromElsewhereAtOnce()
    {
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Hold(hold), .. Prints(true)]));
        var (c, rec) = await h.MakeControllerAsync();
        var known = McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(true)));
        var off = McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(false)));
        await h.Ui.RunAsync(() =>
        {
            // Before the page asked: the application's last status is shown
            // and the row is sensitive, without a call.
            c.Adopt(known);
            Assert.True(c.IsRegistered);
            Assert.True(c.IsEnabled);
            Assert.Equal([true], rec.Registered);
            Assert.Equal([true], rec.Enabled);
            // The same status again changes nothing.
            c.Adopt(known);
            Assert.Equal([true], rec.Registered);
            // While a call runs its answer is newer: nothing taken.
            c.SetRegistered(true);
            c.Adopt(off);
            Assert.True(c.IsRegistered);
        });
        File.Delete(hold);
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.Equal([h.Call("install")], h.Calls);
        await h.Ui.RunAsync(() =>
        {
            // Afterwards a newer status from elsewhere is taken again.
            c.Adopt(off);
            Assert.False(c.IsRegistered);
            Assert.False(rec.Registered[^1]);
        });
        Assert.Empty(rec.Descriptions);

        // Without a bridge or once closed: nothing.
        using var packages = new TemporaryDirectory();
        var missing = await h.Ui.RunAsync(() =>
        {
            var m = new McpRegistrationController(null, claudeDesktop: new ClaudeDesktopPackage { PackagesDirectory = packages.Path }, pending: h.Pending);
            m.Adopt(known);
            return m;
        });
        Assert.False(missing.IsRegistered);
        Assert.False(missing.IsEnabled);
        await h.Ui.RunAsync(() =>
        {
            c.Close();
            c.Adopt(known);
        });
        Assert.False(c.IsRegistered);
    }

    /// <summary>
    /// Windows: a status adopted after a failed check (with no state known,
    /// so the group said why) puts the page's own text back.
    /// </summary>
    [Fact]
    public async Task AdoptPutsThePagesOwnDescriptionBack()
    {
        using var h = new Harness(new(FakeBridgeStep.Fails("read config: permission denied")));
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied"], rec.Descriptions);
        await h.Ui.RunAsync(() => c.Adopt(McpStatus.Decode(Encoding.UTF8.GetBytes(StatusJson(true)))));
        Assert.Equal(["The MCP bridge did not answer: read config: permission denied", null], rec.Descriptions);
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
    }

    [Fact]
    public async Task SetRegistersThroughInstall()
    {
        using var h = new Harness(new(Prints(false), install: Prints(true)));
        var (c, rec) = await h.LoadedControllerAsync();
        Assert.False(c.IsRegistered);
        await h.Ui.RunAsync(() =>
        {
            c.SetRegistered(true);
            Assert.False(c.IsEnabled, "insensitive while the call runs");
        });
        await h.IdleAsync();
        Assert.True(c.IsEnabled);
        Assert.True(c.IsRegistered);
        Assert.True(c.Status?.IsRegistered);
        Assert.Equal([false, true], rec.Registered);
        Assert.Equal([true, false, true], rec.Enabled);
        Assert.Empty(rec.Toasts);
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls);
    }

    [Fact]
    public async Task SetUnregistersThroughUninstall()
    {
        using var h = new Harness(new(Prints(true), uninstall: Prints(false)));
        var (c, rec) = await h.LoadedControllerAsync();
        Assert.True(c.IsRegistered);
        await h.Ui.RunAsync(() => c.SetRegistered(false));
        await h.IdleAsync();
        Assert.False(c.IsRegistered);
        Assert.Equal([true, false], rec.Registered);
        Assert.Empty(rec.Toasts);
        Assert.Equal([h.Call("status"), h.Call("uninstall")], h.Calls);
    }

    [Fact]
    public async Task InstallWithoutAClaudeAppToastsAndTheSwitchStaysOff()
    {
        using var h = new Harness(new(
            Prints(false),
            install: FakeBridgeStep.Fails("no Claude app found on this computer (looked for Claude Desktop and Claude Code)")));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Equal(["No Claude app was found on this computer"], rec.Toasts);
        Assert.False(c.IsRegistered);
        Assert.Equal([false, false], rec.Registered); // rendered again so the switch reverts
        Assert.Equal([true, false, true], rec.Enabled);
    }

    /// <summary>
    /// The real bridge names itself before its reason (main.go prints
    /// "malachi-mcp: " and the error; measured on Windows with no Claude app):
    /// the reason still counts as "no Claude app found".
    /// </summary>
    [Fact]
    public async Task TheBridgesOwnNameBeforeTheReasonIsSkipped()
    {
        using var h = new Harness(new(
            Prints(false),
            install: FakeBridgeStep.Fails("malachi-mcp: no Claude app found (Claude Desktop or Claude Code)")));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Equal(["No Claude app was found on this computer"], rec.Toasts);
        Assert.False(c.IsRegistered);
    }

    [Fact]
    public async Task AFailedCallToastsTheBridgesReasonAndReverts()
    {
        using var h = new Harness(new(
            Prints(true),
            install: FakeBridgeStep.Fails(@"write C:\Users\u\.claude.json: permission denied"),
            uninstall: FakeBridgeStep.Fails(@"write C:\Users\u\.claude.json: permission denied")));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(false));
        await h.IdleAsync();
        Assert.Equal([@"The MCP bridge could not be unregistered: write C:\Users\u\.claude.json: permission denied"], rec.Toasts);
        Assert.True(c.IsRegistered, "the last confirmed state stays");
        Assert.Equal([true, true], rec.Registered);

        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Equal(2, rec.Toasts.Count);
        Assert.Equal(@"The MCP bridge could not be registered: write C:\Users\u\.claude.json: permission denied", rec.Toasts[1]);
        Assert.True(c.IsRegistered);
        Assert.Equal([h.Call("status"), h.Call("uninstall"), h.Call("install")], h.Calls);
        Assert.Empty(rec.Descriptions);
    }

    [Fact]
    public async Task ASilentFailureNamesTheExitStatus()
    {
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Exit(3)]));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Equal(["The MCP bridge could not be registered: malachi-mcp exited with status 3"], rec.Toasts);
        Assert.False(c.IsRegistered);
    }

    [Fact]
    public async Task UnparsableOutputToastsAndReverts()
    {
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Stdout("garbage\n")]));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Single(rec.Toasts);
        Assert.StartsWith("The MCP bridge could not be registered: ", rec.Toasts[0], StringComparison.Ordinal);
        Assert.False(c.IsRegistered);
        Assert.Equal([false, false], rec.Registered);
        Assert.False(c.Status?.IsRegistered); // the last good status stays
    }

    /// <summary>
    /// Swift loads first with the same 1 s limit; here the status is left out,
    /// because a .NET stand-in may take longer than that to start on a busy
    /// machine, and a status that timed out would leave nothing to test. The
    /// switch therefore goes back once, not after a status as well.
    /// </summary>
    [Fact]
    public async Task AHungBridgeTimesOut()
    {
        // The bridge's child keeps the pipes open, as the orphaned sleep of
        // the Swift script: the whole tree goes at the timeout.
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        try
        {
            using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.HoldingChild(hold), FakeBridgeStep.Hold(hold)]));
            var (c, rec) = await h.MakeControllerAsync(timeout: TimeSpan.FromSeconds(1));
            var clock = Stopwatch.StartNew();
            await h.Ui.RunAsync(() => c.SetRegistered(true));
            await h.IdleAsync();
            Assert.True(clock.Elapsed < TimeSpan.FromSeconds(5), $"the bridge was killed, not waited for: {clock.Elapsed}");
            Assert.Single(rec.Toasts);
            Assert.StartsWith("The MCP bridge could not be registered: ", rec.Toasts[0], StringComparison.Ordinal);
            Assert.Contains("did not finish within 1 s", rec.Toasts[0], StringComparison.Ordinal);
            Assert.False(c.IsRegistered);
            Assert.Equal([false], rec.Registered);
        }
        finally
        {
            File.Delete(hold);
        }
    }

    /// <summary>
    /// Swift <c>withoutABridgeTheRowStaysInsensitiveAndSaysSoOnce</c>, with the
    /// description of GTK instead of the toast (U2).
    /// </summary>
    [Fact]
    public async Task WithoutABridgeTheRowStaysInsensitiveAndSaysSoOnce()
    {
        using var ui = new TestUIContext();
        var pending = new PendingWork();
        using var packages = new TemporaryDirectory();
        var (c, rec) = await ui.RunAsync(() =>
        {
            var c = new McpRegistrationController(null, claudeDesktop: new ClaudeDesktopPackage { PackagesDirectory = packages.Path }, pending: pending);
            var rec = new Recorder();
            rec.Attach(c);
            return (c, rec);
        });
        await ui.RunAsync(() =>
        {
            c.Load();
            c.Load();
        });
        await Quiescence.IdleAsync(ui, pending);
        Assert.False(c.IsEnabled);
        Assert.Null(c.Bridge);
        Assert.Equal([false], rec.Enabled);
        Assert.Equal(["The MCP bridge (malachi-mcp) was not found"], rec.Descriptions);
        Assert.Empty(rec.Toasts);
        // A flip that somehow got through goes back, without saying it again.
        await ui.RunAsync(() => c.SetRegistered(true));
        await Quiescence.IdleAsync(ui, pending);
        Assert.Equal([false], rec.Registered);
        Assert.Single(rec.Descriptions);
        Assert.False(c.IsRegistered);
    }

    [Fact]
    public async Task AStaleReplyDoesNotOverwriteANewerOne()
    {
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Hold(hold), .. Prints(true)], uninstall: Prints(false)));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() =>
        {
            c.SetRegistered(true); // slow
            c.SetRegistered(false); // fast, answers first
        });
        await rec.Conditions.WhenAsync(h.Ui, () => c.IsEnabled);
        Assert.False(c.IsRegistered);
        var renders = rec.Registered.Count;
        var toggles = rec.Enabled.Count;

        // The slow reply arrives and is dropped.
        File.Delete(hold);
        await h.IdleAsync();
        Assert.False(c.IsRegistered);
        Assert.Equal(renders, rec.Registered.Count);
        Assert.Equal(toggles, rec.Enabled.Count);
        Assert.Empty(rec.Toasts);
        Assert.Equal(3, h.Calls.Count);
    }

    [Fact]
    public async Task AStatusAskedDuringACallIsSkipped()
    {
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Hold(hold), .. Prints(true)]));
        var (c, _) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.Ui.RunAsync(c.Load); // the page came up again meanwhile
        File.Delete(hold);
        await h.IdleAsync();
        Assert.True(c.IsRegistered, "the install's answer is the status");
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls);
    }

    [Fact]
    public async Task CloseDropsLateReplies()
    {
        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Hold(hold), .. Prints(true)]));
        var (c, rec) = await h.LoadedControllerAsync();
        var (renders, toggles) = await h.Ui.RunAsync(() =>
        {
            c.SetRegistered(true);
            Assert.False(c.IsEnabled);
            c.Close();
            Assert.True(c.IsClosed);
            return (rec.Registered.Count, rec.Enabled.Count);
        });
        File.Delete(hold);
        await h.IdleAsync();
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls); // the request itself went out
        Assert.Equal(renders, rec.Registered.Count);
        Assert.Equal(toggles, rec.Enabled.Count);
        Assert.Empty(rec.Toasts);
        // Nothing starts after close either.
        await h.Ui.RunAsync(() =>
        {
            c.Load();
            c.SetRegistered(false);
        });
        await h.IdleAsync();
        Assert.Equal(2, h.Calls.Count);
    }

    [Fact]
    public async Task ChangeReturnsTheStatusAfterTheCallbacks()
    {
        using var h = new Harness(new(Prints(false), install: Prints(true)));
        var (c, rec) = await h.LoadedControllerAsync();
        var s = await h.Ui.InvokeAsync(() => c.ChangeAsync(true));
        Assert.True(s?.IsRegistered);
        Assert.True(c.IsRegistered);
        Assert.True(c.IsEnabled);
        Assert.True(rec.Registered.SequenceEqual([false, true]), "the switch followed before the caller went on");
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls);
        await h.IdleAsync();
    }

    [Fact]
    public async Task ChangeReturnsNilWhenTheCallFails()
    {
        using var h = new Harness(new(Prints(true), uninstall: FakeBridgeStep.Fails("write: permission denied")));
        var (c, rec) = await h.LoadedControllerAsync();
        Assert.Null(await h.Ui.InvokeAsync(() => c.ChangeAsync(false)));
        Assert.Equal(["The MCP bridge could not be unregistered: write: permission denied"], rec.Toasts);
        Assert.True(c.IsRegistered);
        Assert.Equal([true, true], rec.Registered);
        await h.IdleAsync();
    }

    [Fact]
    public async Task ChangeReturnsNilWithoutABridgeOvertakenOrClosed()
    {
        using (var ui = new TestUIContext())
        {
            var pending = new PendingWork();
            using var packages = new TemporaryDirectory();
            var (missing, missingRec) = await ui.RunAsync(() =>
            {
                var m = new McpRegistrationController(null, claudeDesktop: new ClaudeDesktopPackage { PackagesDirectory = packages.Path }, pending: pending);
                var r = new Recorder();
                r.Attach(m);
                return (m, r);
            });
            Assert.Null(await ui.InvokeAsync(() => missing.ChangeAsync(true)));
            // Windows: the description says so (U2), where Swift toasts.
            Assert.Equal(["The MCP bridge (malachi-mcp) was not found"], missingRec.Descriptions);
            Assert.Empty(missingRec.Toasts);
            Assert.Equal([false], missingRec.Registered);
        }

        using var dir = new TemporaryDirectory();
        var hold = Path.Combine(dir.Path, "hold");
        File.WriteAllBytes(hold, []);
        using var h = new Harness(new(Prints(false), install: [FakeBridgeStep.Hold(hold), .. Prints(true)], uninstall: Prints(false)));
        var (c, _) = await h.LoadedControllerAsync();
        // Overtaken by a newer call: null, and the newer one's answer counts.
        var (slow, fast) = await h.Ui.RunAsync(() => (c.ChangeAsync(true), c.ChangeAsync(false)));
        Assert.False((await fast)?.IsRegistered);
        File.Delete(hold);
        Assert.Null(await slow);
        await h.IdleAsync();
        Assert.False(c.IsRegistered);
        Assert.Equal(3, h.Calls.Count);

        // Closed while the call runs (an install runs to its end, held here
        // until the test lets it go): null at once.
        File.WriteAllBytes(hold, []);
        var closing = await h.Ui.RunAsync(() => c.ChangeAsync(true));
        await h.Ui.RunAsync(c.Close);
        Assert.Null(await closing);
        Assert.Null(await h.Ui.InvokeAsync(() => c.ChangeAsync(false)));
        File.Delete(hold);
        await h.IdleAsync();
        Assert.Equal(4, h.Calls.Count);
    }

    [Fact]
    public void TheReasonIsTheFirstLineOfStderrBounded()
    {
        Assert.Equal("first line", McpRegistrationController.FirstLine("  first line \r\nsecond\n"u8));
        Assert.Equal("", McpRegistrationController.FirstLine([]));
        Assert.Equal(200, McpRegistrationController.FirstLine(Encoding.UTF8.GetBytes(new string('x', 300))).Length);
        // Cut on a character boundary: 199 ASCII bytes, then a two-byte
        // character that would be split.
        const string eAcute = "\u00E9";
        Assert.Equal(2, Encoding.UTF8.GetByteCount(eAcute));
        var edge = new string('a', 199) + eAcute + "tail";
        Assert.Equal(new string('a', 199), McpRegistrationController.FirstLine(Encoding.UTF8.GetBytes(edge)));
        var fits = new string('a', 198) + eAcute;
        Assert.Equal(fits, McpRegistrationController.FirstLine(Encoding.UTF8.GetBytes(fits)));
        // Go's order (mcpsetup reason): a character that ends right at the
        // limit stays, and leading spaces do not count.
        var atLimit = new string('a', 197) + "€";
        Assert.Equal(atLimit, McpRegistrationController.FirstLine(Encoding.UTF8.GetBytes(atLimit + "€")));
        Assert.Equal(new string('x', 200), McpRegistrationController.FirstLine(Encoding.UTF8.GetBytes("   " + new string('x', 300))));
        // Swift: status 3 and signal 9; Windows has no signals.
        Assert.Equal("malachi-mcp exited with status 3", McpRegistrationController.ExitDescription(3));
        Assert.Equal("malachi-mcp was killed", McpRegistrationController.ExitDescription(-1));
        Assert.Equal("malachi-mcp died with status 0xC0000005", McpRegistrationController.ExitDescription(unchecked((int)0xC0000005)));
    }

    /// <summary>mcpsetup_test.go TestQueryParsesReport, through the controller.</summary>
    [Fact]
    public async Task QueryParsesReport()
    {
        using var h = new Harness(new(FakeBridgeStep.Prints(GoReport)));
        var (c, _) = await h.LoadedControllerAsync();
        var st = c.Status!;
        Assert.Equal("/opt/malachi/bin/malachi-mcp", st.Command);
        Assert.Equal(2, st.Clients.Count);
        var want = new McpClient
        {
            Id = "claude-code",
            Name = "Claude Code",
            Present = true,
            Registered = true,
            Path = "/home/u/.claude.json",
            Other = "/usr/local/bin/malachi-mcp",
        };
        Assert.Equal(want, st.Clients[1]);
        Assert.True(st.Clients[0].Other is null && !st.Clients[0].Registered);
        Assert.True(st.IsRegistered, "one registered client");
        Assert.True(c.IsRegistered);
    }

    /// <summary>mcpsetup_test.go TestRegistered.</summary>
    [Fact]
    public void Registered()
    {
        static McpClient Client(bool present = false, bool registered = false) =>
            new() { Id = "x", Name = "X", Present = present, Registered = registered };
        (string Name, McpStatus Status, bool Want)[] cases =
        [
            ("empty", new() { Command = "" }, false),
            ("present but not registered", new() { Command = "", Clients = [Client(present: true), Client(present: true)] }, false),
            ("one registered", new() { Command = "", Clients = [Client(present: true), Client(present: true, registered: true)] }, true),
            ("all registered", new() { Command = "", Clients = [Client(registered: true), Client(registered: true)] }, true),
        ];
        foreach (var (name, status, want) in cases)
        {
            Assert.True(status.IsRegistered == want, name);
        }
    }

    /// <summary>mcpsetup_test.go TestInstallNoClient: the bridge's own line, exit 1.</summary>
    [Fact]
    public async Task InstallNoClient()
    {
        using var h = new Harness(new(Prints(false), install: FakeBridgeStep.Fails(NoClientLine)));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        Assert.Equal(["No Claude app was found on this computer"], rec.Toasts);
        Assert.Equal([h.Call("status"), h.Call("install")], h.Calls);
    }

    /// <summary>
    /// mcpsetup_test.go TestExitReasonIsFirstLineBounded: the reason is the
    /// first line, trimmed, at most 200 bytes and never cut inside a rune.
    /// </summary>
    [Fact]
    public async Task ExitReasonIsFirstLineBounded()
    {
        using var h = new Harness(new(
            Prints(true),
            uninstall: [FakeBridgeStep.Stderr("  " + LongReason + "  \nsecond line with details\n"), FakeBridgeStep.Exit(2)]));
        var (c, rec) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(false));
        await h.IdleAsync();
        const string prefix = "The MCP bridge could not be unregistered: ";
        var toast = Assert.Single(rec.Toasts);
        Assert.StartsWith(prefix, toast, StringComparison.Ordinal);
        var reason = toast[prefix.Length..];
        Assert.Equal(198, Encoding.UTF8.GetByteCount(reason)); // 66 whole runes
        Assert.StartsWith(reason, LongReason, StringComparison.Ordinal);
        Assert.DoesNotContain("second line", toast, StringComparison.Ordinal);
    }

    /// <summary>mcpsetup_test.go TestExitWithoutReason: the exit status stands in for the reason.</summary>
    [Fact]
    public async Task ExitWithoutReason()
    {
        using var h = new Harness(new([FakeBridgeStep.Exit(3)]));
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(["The MCP bridge did not answer: malachi-mcp exited with status 3"], rec.Descriptions);
        Assert.False(c.IsEnabled);
    }

    /// <summary>mcpsetup_test.go TestBadReport: a report that is not JSON is no status.</summary>
    [Fact]
    public async Task BadReport()
    {
        using var h = new Harness(new([FakeBridgeStep.Stdout("not json\n")]));
        var (c, rec) = await h.MakeControllerAsync();
        await h.Ui.RunAsync(c.Load);
        await h.IdleAsync();
        Assert.Equal(["The MCP bridge did not answer: unexpected output from malachi-mcp status"], rec.Descriptions);
        Assert.Null(c.Status);
    }

    /// <summary>
    /// mcpsetup_test.go TestSubcommandsPassJSONFlag, with what Windows adds
    /// (docs/mcp.md): every subcommand gets --json and --command with the
    /// bridge's path, and nothing about Claude Desktop while its package is
    /// not installed.
    /// </summary>
    [Fact]
    public async Task SubcommandsPassJsonFlag()
    {
        using var h = new Harness(new(Prints(false), install: Prints(true), uninstall: Prints(false)));
        var (c, _) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(false));
        await h.IdleAsync();
        var bridge = McpRegistrationController.CanonicalPath(h.Bridge);
        Assert.Equal(bridge, c.Bridge);
        Assert.Equal(
            [$"status --json --command {bridge}", $"install --json --command {bridge}", $"uninstall --json --command {bridge}"],
            h.Calls);
        Assert.Equal(
            ["install", "--json", "--command", bridge],
            McpRegistrationController.Arguments(McpRegistrationController.Command.Install, bridge, null));
    }

    /// <summary>
    /// The Microsoft Store's Claude Desktop keeps its configuration in its
    /// package (docs/mcp.md): once the package directory exists, every call
    /// names that file with --claude-desktop-config, after --command.
    /// </summary>
    [Fact]
    public async Task TheMsixClaudeDesktopIsPassedItsConfiguration()
    {
        using var packages = new TemporaryDirectory();
        var desktop = new ClaudeDesktopPackage { PackagesDirectory = packages.Path };
        Assert.Equal(ClaudeDesktopPackage.ClaudePackageFamily, desktop.PackageFamily);
        Assert.Equal("Claude_pzs8sxrjxfjjc", desktop.PackageFamily);
        Assert.False(desktop.IsInstalled);
        Directory.CreateDirectory(Path.Combine(packages.Path, "Claude_pzs8sxrjxfjjc"));
        Assert.True(desktop.IsInstalled);
        var config = Path.Combine(packages.Path, "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json");
        Assert.Equal(config, desktop.ConfigPath);

        using var h = new Harness(new(Prints(false), install: Prints(true), uninstall: Prints(false)), desktop);
        var (c, _) = await h.LoadedControllerAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(true));
        await h.IdleAsync();
        await h.Ui.RunAsync(() => c.SetRegistered(false));
        await h.IdleAsync();
        var bridge = McpRegistrationController.CanonicalPath(h.Bridge);
        Assert.Equal(
            [
                $"status --json --command {bridge} --claude-desktop-config {config}",
                $"install --json --command {bridge} --claude-desktop-config {config}",
                $"uninstall --json --command {bridge} --claude-desktop-config {config}",
            ],
            h.Calls);
        Assert.Equal(
            ["status", "--json", "--command", bridge, "--claude-desktop-config", config],
            McpRegistrationController.Arguments(McpRegistrationController.Command.Status, bridge, config));

        // Another family (the package is looked for by its family) is not installed.
        using var other = new Harness(new(Prints(false)), desktop with { PackageFamily = "Claude_other" });
        var (o, _) = await other.LoadedControllerAsync();
        Assert.Equal([$"status --json --command {McpRegistrationController.CanonicalPath(other.Bridge)}"], other.Calls);
        await other.Ui.RunAsync(o.Close);
    }

    [Fact]
    public void TheCurrentUsersPackageIsUnderLocalAppData()
    {
        var desktop = ClaudeDesktopPackage.ForCurrentUser();
        Assert.Equal("Packages", Path.GetFileName(desktop.PackagesDirectory));
        Assert.EndsWith(
            Path.Combine("Packages", "Claude_pzs8sxrjxfjjc", "LocalCache", "Roaming", "Claude", "claude_desktop_config.json"),
            desktop.ConfigPath,
            StringComparison.Ordinal);
    }

    /// <summary>
    /// The bridge is registered and run as one spelling of its path, however
    /// the app folder was named: absolute, cleaned, short names expanded and,
    /// on Windows, each component as the file system spells it.
    /// </summary>
    [Fact]
    public async Task TheCommandIsTheBridgesCanonicalPath()
    {
        using var root = new TemporaryDirectory();
        var app = Path.Combine(root.Path, "Malachi Mail App");
        Directory.CreateDirectory(app);
        var script = new FakeBridgeScript(Prints(false));
        var bridge = script.CreateIn(app);
        var canonicalRoot = McpRegistrationController.CanonicalPath(root.Path);
        Assert.Equal(root.Path, canonicalRoot, ignoreCase: true);
        var want = Path.Combine(canonicalRoot, "Malachi Mail App", FakeBridgeScript.BridgeFileName);
        Assert.Equal(want, McpRegistrationController.CanonicalPath(bridge));
        Assert.Equal(want, McpRegistrationController.CanonicalPath(Path.Combine(app, ".", "..", "Malachi Mail App", FakeBridgeScript.BridgeFileName)));
        string spelt;
        if (OperatingSystem.IsWindows())
        {
            spelt = Path.Combine(root.Path.ToLowerInvariant(), "malachi mail app", FakeBridgeScript.BridgeFileName.ToUpperInvariant());
            Assert.Equal(want, McpRegistrationController.CanonicalPath(spelt));
            Assert.Equal(want, McpRegistrationController.CanonicalPath(ShortPath(bridge)));
        }
        else
        {
            spelt = Path.Combine(app, ".", FakeBridgeScript.BridgeFileName);
        }

        using var ui = new TestUIContext();
        var pending = new PendingWork();
        using var packages = new TemporaryDirectory();
        var c = await ui.RunAsync(() => new McpRegistrationController(
            spelt, claudeDesktop: new ClaudeDesktopPackage { PackagesDirectory = packages.Path }, pending: pending));
        Assert.Equal(want, c.Bridge);
        await ui.RunAsync(c.Load);
        await Quiescence.IdleAsync(ui, pending);
        Assert.True(c.IsEnabled);
        Assert.Equal([$"status --json --command {want}"], FakeBridgeScript.Calls(app));
    }

    // The 8.3 name of an existing path as cmd.exe reports it; the path
    // itself where the volume makes no short names.
    private static string ShortPath(string path)
    {
        var start = new ProcessStartInfo("cmd.exe", "/d /c for %I in (\"" + path + "\") do @echo %~sI")
        {
            UseShellExecute = false,
            RedirectStandardOutput = true,
            CreateNoWindow = true,
        };
        using var cmd = Process.Start(start)!;
        var output = cmd.StandardOutput.ReadToEnd().Trim();
        cmd.WaitForExit();
        return output.Length > 0 ? output : path;
    }

    /// <summary>Collects what the controller emits (the Swift suite's Recorder).</summary>
    private sealed class Recorder
    {
        public List<bool> Registered { get; } = [];

        public List<bool> Enabled { get; } = [];

        public List<string> Toasts { get; } = [];

        public List<string?> Descriptions { get; } = [];

        public UiConditions Conditions { get; } = new();

        public void Attach(McpRegistrationController c)
        {
            c.RegisteredChanged += (_, on) => Note(() => Registered.Add(on));
            c.EnabledChanged += (_, on) => Note(() => Enabled.Add(on));
            c.ToastRequested += (_, text) => Note(() => Toasts.Add(text));
            c.DescriptionChanged += (_, text) => Note(() => Descriptions.Add(text));
            c.PropertyChanged += (_, _) => Conditions.Changed();
        }

        private void Note(Action record)
        {
            record();
            Conditions.Changed();
        }
    }

    /// <summary>
    /// A stand-in bridge in a fresh directory (the Swift suite's FakeBridge),
    /// the UI thread, and an empty directory of packages unless given one.
    /// </summary>
    private sealed class Harness : IDisposable
    {
        private readonly TemporaryDirectory dir = new();
        private readonly TemporaryDirectory packages = new();
        private readonly ClaudeDesktopPackage desktop;

        public Harness(FakeBridgeScript script, ClaudeDesktopPackage? desktop = null)
        {
            Bridge = script.CreateIn(dir.Path);
            this.desktop = desktop ?? new ClaudeDesktopPackage { PackagesDirectory = packages.Path };
        }

        public TestUIContext Ui { get; } = new();

        public PendingWork Pending { get; } = new();

        /// <summary>The clock the repeats of a failed status wait on.</summary>
        public FakeTimeProvider Time { get; } = new();

        /// <summary>The stand-in's path.</summary>
        public string Bridge { get; }

        /// <summary>The arguments of every invocation so far, one line each.</summary>
        public IReadOnlyList<string> Calls => FakeBridgeScript.Calls(dir.Path);

        /// <summary>The line a subcommand leaves in <see cref="Calls"/> without Claude Desktop's package.</summary>
        public string Call(string subcommand) => $"{subcommand} --json --command {McpRegistrationController.CanonicalPath(Bridge)}";

        /// <summary>A controller over the stand-in; a failed status is not repeated unless the test gives <paramref name="statusRetryDelays"/>.</summary>
        public Task<(McpRegistrationController, Recorder)> MakeControllerAsync(TimeSpan? timeout = null, IReadOnlyList<TimeSpan>? statusRetryDelays = null) => Ui.RunAsync(() =>
        {
            var c = new McpRegistrationController(
                Bridge, timeout: timeout ?? TimeSpan.FromSeconds(15), statusRetryDelays: statusRetryDelays ?? [], claudeDesktop: desktop, time: Time, pending: Pending);
            var rec = new Recorder();
            rec.Attach(c);
            return (c, rec);
        });

        /// <summary>A controller whose first status has answered.</summary>
        public async Task<(McpRegistrationController, Recorder)> LoadedControllerAsync()
        {
            var (c, rec) = await MakeControllerAsync();
            await Ui.RunAsync(c.Load);
            await IdleAsync();
            Assert.True(c.IsEnabled);
            return (c, rec);
        }

        public Task IdleAsync() => Quiescence.IdleAsync(Ui, Pending, timeout: TimeSpan.FromSeconds(30));

        /// <summary>Moves the clock on once everything settled, then waits for what that started.</summary>
        public async Task AdvanceAsync(TimeSpan by)
        {
            await IdleAsync();
            Time.Advance(by);
            await IdleAsync();
        }

        public void Dispose()
        {
            Ui.Dispose();
            packages.Dispose();
            dir.Dispose();
        }
    }
}
