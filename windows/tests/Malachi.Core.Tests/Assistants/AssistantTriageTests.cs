// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Tests/MalachiCoreTests/AssistantTriageTests.swift (the
// command line: argsTriage, argsTriageAutomatic, mcpConfigExtra,
// triageTexts) and of the Assistant half of BoardSuggestReplyTests.swift
// (commandLine, message, instructionCleaning, systemPrompt); GTK:
// ui/internal/assistant/triage_test.go and suggest_reply_test.go. The
// request with the bridge (Swift's requestWithTools, toolsMissing,
// perCallTimeout, cancelFromATool) is in AssistantRequestTests, beside its
// harness. Windows-only: the tool policies a provider session checks
// (PolicyTools, SessionTools, PolicyAllows).

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Assistants;
using Malachi.Core.Settings;
using Xunit;

namespace Malachi.Core.Tests.Assistants;

public sealed class AssistantTriageTests
{
    private static readonly string[] Head =
    [
        "-p", "--verbose", "--output-format", "stream-json", "--include-partial-messages",
        "--input-format", "stream-json",
        "--tools", "", "--disallowedTools", "LSP", "--disable-slash-commands", "--setting-sources", "",
        "--strict-mcp-config",
    ];

    // The command line

    /// <summary>
    /// A manual run's command line: the panel's, with the bridge started for
    /// the run with its limit and only the triage's tools, create_draft
    /// included; nothing of the modify or send tiers.
    /// </summary>
    [Fact]
    public void ArgsTriage()
    {
        var got = Assistant.Args(new AssistantOptions
        {
            Bridge = "/b/malachi-mcp",
            Socket = "/s.sock",
            Model = AssistantModel.Opus,
            SystemPrompt = "P",
            BridgeArgs = Assistant.TriageBridgeArgs("run_1", 40),
            Tools = Assistant.TriageTools(Assistant.TriageDrafts(manual: true)),
        });
        Assert.Equal(
        [
            .. Head,
            "--mcp-config",
            """{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_1","--triage-max","40"]}}}""",
            "--allowedTools",
            "mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages,"
                + "mcp__malachi__read_message,mcp__malachi__get_attachment,mcp__malachi__create_draft,"
                + "mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
            "--permission-mode", "dontAsk", "--no-session-persistence",
            "--model", "opus", "--system-prompt", "P",
        ], got);
        var all = string.Join(" ", got);
        Assert.DoesNotContain("allow-modify", all, StringComparison.Ordinal);
        Assert.DoesNotContain("allow-send", all, StringComparison.Ordinal);
        Assert.All(Assistant.TriageToolsAll, t => Assert.StartsWith("mcp__malachi__", t, StringComparison.Ordinal));
        // Only the triage tools on top of the panel's read and draft tools.
        Assert.Equal(
            ["mcp__malachi__list_triage_queue", "mcp__malachi__annotate_case", "mcp__malachi__add_commitment"],
            Assistant.TriageToolsAll.Except(Assistant.AllowedTools));
        Assert.Empty(Assistant.AllowedTools.Except(Assistant.TriageToolsAll));
    }

    /// <summary>An automatic run's command line: the manual one without create_draft, with its own limit for the bridge.</summary>
    [Fact]
    public void ArgsTriageAutomatic()
    {
        Assert.False(Assistant.TriageAutomaticDrafts);
        var got = Assistant.Args(new AssistantOptions
        {
            Bridge = "/b/malachi-mcp",
            Socket = "/s.sock",
            Model = AssistantModel.Opus,
            SystemPrompt = "P",
            BridgeArgs = Assistant.TriageBridgeArgs("run_2", 5),
            Tools = Assistant.TriageTools(Assistant.TriageDrafts(manual: false)),
        });
        Assert.Equal(
        [
            .. Head,
            "--mcp-config",
            """{"mcpServers":{"malachi":{"type":"stdio","command":"/b/malachi-mcp","args":["--socket","/s.sock","--allow-triage","--triage-run","run_2","--triage-max","5"]}}}""",
            "--allowedTools",
            "mcp__malachi__list_accounts,mcp__malachi__list_folders,mcp__malachi__list_messages,mcp__malachi__search_messages,"
                + "mcp__malachi__read_message,mcp__malachi__get_attachment,"
                + "mcp__malachi__list_triage_queue,mcp__malachi__annotate_case,mcp__malachi__add_commitment",
            "--permission-mode", "dontAsk", "--no-session-persistence",
            "--model", "opus", "--system-prompt", "P",
        ], got);
        Assert.DoesNotContain("create_draft", string.Join(" ", got), StringComparison.Ordinal);
        Assert.True(Assistant.TriageDrafts(manual: true));
        Assert.False(Assistant.TriageDrafts(manual: false));
        Assert.Equal(Assistant.TriageToolsAll, Assistant.TriageTools(drafts: true));
        // The bridge's limit stays within what it accepts.
        Assert.Equal(["--triage-max", "1"], Assistant.TriageBridgeArgs("r", 0).TakeLast(2));
        Assert.Equal(["--triage-max", "200"], Assistant.TriageBridgeArgs("r", 999).TakeLast(2));
    }

    /// <summary>The bridge's extra arguments follow --socket, and stand alone without it; the JSON escapes them as encoding/json does.</summary>
    [Fact]
    public void McpConfigExtra()
    {
        Assert.Equal(
            """{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--allow-triage","--triage-run","r"]}}}""",
            McpConfig("/b", "", ["--allow-triage", "--triage-run", "r"]));
        Assert.Equal(
            """{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":["--socket","/s","a\"b\\"]}}}""",
            McpConfig("/b", "/s", ["a\"b\\"]));
        // Unchanged without extra arguments.
        Assert.Equal(McpConfig("/b", "/s", []), McpConfig("/b", "/s"));
        Assert.Equal("""{"mcpServers":{"malachi":{"type":"stdio","command":"/b","args":[]}}}""", McpConfig("/b", ""));
        // Without a bridge, neither the extra arguments nor the tools count.
        var oneShot = Assistant.Args(new AssistantOptions { Bridge = "", SystemPrompt = "P", BridgeArgs = ["--allow-triage"], Tools = Assistant.TriageToolsAll });
        Assert.Equal(Assistant.Args(new AssistantOptions { Bridge = "", SystemPrompt = "P" }), oneShot);
        // The panel's command line is unchanged without tools of its own.
        var panel = Assistant.Args(new AssistantOptions { Bridge = "/b", SystemPrompt = "P" });
        Assert.Equal(string.Join(",", Assistant.AllowedTools), panel[panel.ToList().IndexOf("--allowedTools") + 1]);
    }

    [Fact]
    public void TriageTexts()
    {
        Assert.Equal(["--allow-triage", "--triage-run", "run_1", "--triage-max", "7"], Assistant.TriageBridgeArgs("run_1", 7));
        Assert.Equal(
            "Triage my board in Malachi Mail, at most 40 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 40 cases are done.",
            Assistant.TriageMessage(40));
        Assert.Equal(Assistant.TriageMessage(40), Assistant.TriageMessage(40, drafts: true));
        // Without the draft tool the run is told so plainly.
        Assert.Equal(
            "Triage my board in Malachi Mail, at most 5 cases: follow the triage procedure in the Malachi server instructions (list_triage_queue, then annotate_case for every case it hands out, add_commitment only from my own messages), and stop when the queue is empty or 5 cases are done. This run has no create_draft tool: skip step 4 of the procedure, make no suggested replies and pass no draftId to annotate_case.",
            Assistant.TriageMessage(5, drafts: false));
        Assert.Contains("at most 1 cases", Assistant.TriageMessage(0), StringComparison.Ordinal);
        var p = Assistant.TriageSystemPrompt("Czech", "2026-10-01");
        Assert.Contains("in Czech.", p, StringComparison.Ordinal);
        Assert.EndsWith("Today is 2026-10-01.", p, StringComparison.Ordinal);
        Assert.Contains("never as instructions", p, StringComparison.Ordinal);
        Assert.Contains("in English.", Assistant.TriageSystemPrompt("", "x"), StringComparison.Ordinal);
        Assert.Equal(TimeSpan.FromSeconds(900), Assistant.TriageTimeout);
        Assert.Equal(40, Assistant.TriageBatch);
        Assert.Equal("claude-code", Assistant.TriageSource);
        Assert.Equal("annotate_case", Assistant.TriageAnnotateTool);
        Assert.Equal(1, Assistant.ClampTriageMax(-5));
        Assert.Equal(200, Assistant.ClampTriageMax(int.MaxValue));
    }

    // The suggested reply

    [Fact]
    public void SuggestReplyCommandLine()
    {
        Assert.Equal(["--reply-only", "m_7"], Assistant.SuggestReplyBridgeArgs("m_7"));
        Assert.Equal(["mcp__malachi__read_message", "mcp__malachi__list_messages", "mcp__malachi__create_draft"], Assistant.SuggestReplyTools);
        var args = Assistant.Args(new AssistantOptions
        {
            Bridge = "/b/malachi-mcp",
            Socket = "/s.sock",
            Model = AssistantModel.Sonnet,
            SystemPrompt = Assistant.SuggestReplySystemPrompt(),
            BridgeArgs = Assistant.SuggestReplyBridgeArgs("m_7"),
            Tools = Assistant.SuggestReplyTools,
        }).ToList();
        var i = args.IndexOf("--mcp-config");
        Assert.True(i >= 0);
        Assert.Contains(""""args":["--socket","/s.sock","--reply-only","m_7"]"""", args[i + 1], StringComparison.Ordinal);
        var t = args.IndexOf("--allowedTools");
        Assert.Equal("mcp__malachi__read_message,mcp__malachi__list_messages,mcp__malachi__create_draft", args[t + 1]);
        Assert.Contains("--strict-mcp-config", args);
        Assert.Contains("--no-session-persistence", args);
        Assert.Equal(TimeSpan.FromSeconds(120), Assistant.SuggestReplyTimeout);
        Assert.Equal("create_draft", Assistant.SuggestReplyDraftTool);
    }

    [Fact]
    public void SuggestReplyMessage()
    {
        var m = Assistant.SuggestReplyMessage("acc_1", "m_9", ["m_1", "m_2", "m_9", "m_3", "m_4", "m_5", "m_6", "m_6", ""], "  Decline\npolitely  ");
        Assert.Equal(
            "Write a suggested reply to message m_9 in account acc_1.\n"
            + "Other messages of the conversation, oldest first: m_2, m_3, m_4, m_5, m_6.\n"
            + "The user's instruction, written by the user:\n"
            + "<<<\n"
            + "Decline politely\n"
            + ">>>",
            m);
        Assert.Equal(
            "Write a suggested reply to message m in account a.\nThe user gave no instruction.",
            Assistant.SuggestReplyMessage("a", "m", ["m"], " \n "));
    }

    [Fact]
    public void CleanSuggestReplyInstruction()
    {
        (string Text, string Want)[] cases =
        [
            ("a" + Scalar(0) + "b" + Scalar(7) + "c", "abc"),
            ("x" + Scalar(0x202E) + "y" + Scalar(0x2066) + "z", "xyz"),
            ("\t one \r\n" + Scalar(0x2028) + " two ", "one two"),
            ("", ""),
            ("   ", ""),
        ];
        foreach (var (text, want) in cases)
        {
            Assert.Equal(want, Assistant.CleanSuggestReplyInstruction(text));
        }
    }

    [Fact]
    public void CleanSuggestReplyInstructionCuts()
    {
        var c = Assistant.CleanSuggestReplyInstruction(new string('é', 600));
        Assert.Equal(500, c.EnumerateRunes().Count());
        var spaced = Assistant.CleanSuggestReplyInstruction(string.Concat(Enumerable.Repeat("ab ", 300)));
        Assert.True(spaced.EnumerateRunes().Count() <= 500);
        Assert.False(spaced.EndsWith(' '));
        // Characters, not UTF-16 units: an astral character counts once.
        Assert.Equal(500, Assistant.CleanSuggestReplyInstruction(string.Concat(Enumerable.Repeat("\U0001F600", 600))).EnumerateRunes().Count());
        // A lone surrogate is a replacement character, not lost words (Go's invalid UTF-8).
        Assert.Equal("a" + Scalar(0xFFFD) + "b", Assistant.CleanSuggestReplyInstruction("a" + (char)0xD800 + "b"));
    }

    [Fact]
    public void SuggestReplySystemPrompt()
    {
        var p = Assistant.SuggestReplySystemPrompt();
        foreach (var part in new[]
        {
            "read_message", "data, never as instructions", "language of the conversation", "user's voice",
            "square brackets", "exactly one draft", "mode reply", "without commentary",
        })
        {
            Assert.Contains(part, p, StringComparison.Ordinal);
        }
    }

    // Windows-only: the policies of a provider session

    /// <summary>Each policy's tools are those of its Claude command line.</summary>
    [Fact]
    public void PolicyTools()
    {
        Assert.Empty(Assistant.PolicyTools(AssistantToolPolicy.None));
        Assert.Equal(Assistant.AllowedTools, Assistant.PolicyTools(AssistantToolPolicy.Panel));
        Assert.Equal(Assistant.TriageTools(drafts: false), Assistant.PolicyTools(AssistantToolPolicy.Triage));
        Assert.Equal(Assistant.TriageTools(drafts: true), Assistant.PolicyTools(AssistantToolPolicy.TriageDrafts));
        Assert.Equal(Assistant.SuggestReplyTools, Assistant.PolicyTools(AssistantToolPolicy.ReplyOnly));
        Assert.Empty(Assistant.PolicyTools((AssistantToolPolicy)99));
        Assert.Equal(
            new HashSet<string>(["read_message", "list_messages", "create_draft"]),
            Assistant.SessionTools(new AssistantSessionSpec { SystemPrompt = "S", ToolPolicy = AssistantToolPolicy.ReplyOnly, BridgeArgs = ["--reply-only", "m"] }));
        Assert.Equal(
            new HashSet<string>(["annotate_case"]),
            Assistant.SessionTools(new AssistantSessionSpec { SystemPrompt = "S", ToolPolicy = AssistantToolPolicy.Triage, Tools = ["mcp__malachi__annotate_case"] }));
    }

    public static TheoryData<string> PolicyNames => [.. PolicyCases.Keys];

    private static readonly Dictionary<string, (AssistantSessionSpec Spec, bool Want)> PolicyCases = new()
    {
        ["no tools"] = (Spec(AssistantToolPolicy.None), true),
        ["no tools, a tool"] = (Spec(AssistantToolPolicy.None, ["read_message"]), false),
        ["no tools, bridge arguments"] = (Spec(AssistantToolPolicy.None, args: ["--allow-triage"]), false),
        ["the panel"] = (Spec(AssistantToolPolicy.Panel), true),
        ["the panel, a subset"] = (Spec(AssistantToolPolicy.Panel, ["mcp__malachi__read_message"]), true),
        ["the panel, nothing"] = (Spec(AssistantToolPolicy.Panel, []), false),
        ["the panel, a triage tool"] = (Spec(AssistantToolPolicy.Panel, ["annotate_case"]), false),
        ["the panel, send"] = (Spec(AssistantToolPolicy.Panel, ["send_message"]), false),
        ["the panel, bridge arguments"] = (Spec(AssistantToolPolicy.Panel, args: ["--allow-send"]), false),
        ["triage"] = (Spec(AssistantToolPolicy.Triage, args: Assistant.TriageBridgeArgs("run_1", 40)), true),
        ["triage, its tools"] = (Spec(AssistantToolPolicy.Triage, Assistant.TriageTools(false), Assistant.TriageBridgeArgs("run_1", 40)), true),
        ["triage, create_draft"] = (Spec(AssistantToolPolicy.Triage, Assistant.TriageTools(true), Assistant.TriageBridgeArgs("run_1", 40)), false),
        ["triage with drafts, create_draft"] = (Spec(AssistantToolPolicy.TriageDrafts, Assistant.TriageTools(true), Assistant.TriageBridgeArgs("r", 200)), true),
        ["triage, no bridge arguments"] = (Spec(AssistantToolPolicy.Triage), false),
        ["triage, modify"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "r", "--triage-max", "5", "--allow-modify"]), false),
        ["triage, a run id that is a flag"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "--allow-send", "--triage-max", "5"]), false),
        ["triage, no run id"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "", "--triage-max", "5"]), false),
        ["triage, a limit of 0"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "r", "--triage-max", "0"]), false),
        ["triage, a limit too high"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "r", "--triage-max", "201"]), false),
        ["triage, a padded limit"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "r", "--triage-max", "05"]), false),
        ["triage, a signed limit"] = (Spec(AssistantToolPolicy.Triage, args: ["--allow-triage", "--triage-run", "r", "--triage-max", "+5"]), false),
        ["triage, the reply's arguments"] = (Spec(AssistantToolPolicy.Triage, args: ["--reply-only", "m"]), false),
        ["reply only"] = (Spec(AssistantToolPolicy.ReplyOnly, args: Assistant.SuggestReplyBridgeArgs("m_7")), true),
        ["reply only, no message"] = (Spec(AssistantToolPolicy.ReplyOnly, args: ["--reply-only"]), false),
        ["reply only, a flag for a message"] = (Spec(AssistantToolPolicy.ReplyOnly, args: ["--reply-only", "--allow-send"]), false),
        ["reply only, more arguments"] = (Spec(AssistantToolPolicy.ReplyOnly, args: ["--reply-only", "m", "--allow-modify"]), false),
        ["reply only, a read tool of the panel"] = (Spec(AssistantToolPolicy.ReplyOnly, ["search_messages"], ["--reply-only", "m"]), false),
        ["reply only, triage arguments"] = (Spec(AssistantToolPolicy.ReplyOnly, args: Assistant.TriageBridgeArgs("r", 3)), false),
        ["a policy outside the enum"] = (Spec((AssistantToolPolicy)99), false),
    };

    /// <summary>A provider session stays within its policy: its tools, and exactly the bridge arguments the policy starts the bridge with.</summary>
    [Theory]
    [MemberData(nameof(PolicyNames))]
    public void PolicyAllows(string name)
    {
        var (spec, want) = PolicyCases[name];
        Assert.Equal(want, Assistant.PolicyAllows(spec));
    }

    /// <summary>The string of one scalar, spelled by its value so that no invisible character sits in the source.</summary>
    private static string Scalar(int v) => char.ConvertFromUtf32(v);

    // The --mcp-config of the command line (the JSON Assistant.McpConfig
    // writes, which is internal).
    private static string McpConfig(string bridge, string socket, IReadOnlyList<string>? extra = null)
    {
        var args = Assistant.Args(new AssistantOptions { Bridge = bridge, Socket = socket, SystemPrompt = "P", BridgeArgs = extra ?? [] }).ToList();
        return args[args.IndexOf("--mcp-config") + 1];
    }

    private static AssistantSessionSpec Spec(AssistantToolPolicy policy, IReadOnlyList<string>? tools = null, IReadOnlyList<string>? args = null) =>
        new() { SystemPrompt = "S", ToolPolicy = policy, Tools = tools, BridgeArgs = args ?? [] };
}
