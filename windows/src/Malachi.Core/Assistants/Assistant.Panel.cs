// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiCore/Assistant/AssistantPanel.swift (targets,
// Model, models, parseModel, modelName, prefixTools, Options, args,
// mcpConfig, uiLanguage, userMessage, attachmentReadable, candidatePaths,
// childEnv, childEnvironment, PanelStrings, panelTexts, contextLabel,
// conversationContext, conversationLabel, stoppedText, activityLabel) and of
// stripBridgePrefix with its constants (AssistantEvents.swift); GTK:
// ui/internal/assistant/claude.go (toolPrefix, bridgeServer, PrefixTools,
// Options, Args, mcpConfig, UserMessage, AttachmentReadable, CandidatePaths,
// ChildEnv) and the panel's half of assistant.go (Targets, Model, Models,
// ParseModel, ModelName, ActivityLabel, ContextLabel, ConversationLabel,
// StoppedText, PanelStrings, PanelTexts).
//
// The In App target: the panel of the main window runs the user's own
// Claude Code (claude -p with stream-json in and out, one process per
// conversation) restricted to the malachi-mcp tools, and shows its answers.
// This file holds the pure half of it that is not about the stream's events
// (Assistant.Events.cs) or the answers' formatting (Assistant.Markdown.cs):
// the models, the command line over the daemon's prompt, the stdin line,
// where claude may be, the child's environment and the panel's texts. Every
// prompt is the daemon's (docs/api.md §4.18 assistant.prompt,
// IAssistantPrompter): the system prompt with the user's profile, the
// message of a turn, the tools and the bridge's arguments; nothing of it is
// built here. The command line, as an argument array, never a shell string:
//
//     claude -p --verbose --output-format stream-json --include-partial-messages
//            --input-format stream-json --tools "" --disallowedTools LSP
//            --disable-slash-commands --setting-sources "" --strict-mcp-config
//            --mcp-config {"mcpServers":{"malachi":{…}}} --allowedTools <tools>
//            --permission-mode dontAsk --no-session-persistence
//            --model <model> --system-prompt <systemPrompt>
//
// in an empty private working directory and with ChildEnv: each stdin line
// is a turn (UserMessage), each stdout line an event; stdin stays open while
// the conversation lives. Nothing of the user's own Claude Code setup is
// loaded, no built-in tool exists, the bridge is the only MCP server, only
// the prompt's tools run and the session is not written to disk. The
// compose window's rewrite and writing bar and the search in the user's own
// words are one-shot requests over the same protocol: the same command line
// without the bridge (no --mcp-config, no --allowedTools: no tool at all),
// with --json-schema when the daemon's prompt has a schema. Authentication
// is entirely Claude Code's: the command line never carries a key and the
// environment passes nothing of the kind. The texts at the end go through
// L10n.
//
// The model is the enum AssistantModel (Settings), where Swift and Go keep a
// string: a value outside it is the unknown model and behaves as Sonnet.
//
// Windows differences:
// - CandidatePaths looks only for claude.exe, Anthropic's native build:
//   npm's claude.cmd would run through cmd.exe, whose parsing of a command
//   line cannot carry the JSON arguments of Args safely, so there are no
//   nvm, Homebrew or npm places (and no newestFirst). The native installer's
//   %USERPROFILE%\.local\bin comes first, then every usable directory of
//   PATH.
// - Paths are Windows paths, cleaned by CleanWindowsPath in pure string code
//   (Core does not depend on the OS it runs on, so no Path.GetFullPath):
//   drive-absolute only, the way Go's filepath.Clean writes them, with an
//   upper-case drive letter; UNC, relative and odd paths are refused.
//   Duplicates are compared without case, as the file system does.
// - ChildEnv keeps the variables a Windows process needs to start and to
//   find its profile, temporary directory and system tools (keys compared
//   without case, written as spelt in ChildEnvKeys), and a PATH of the
//   directory of claude.exe and the system's own directories under
//   SystemRoot. Measured: Claude Code 2.1.72 on the development machine
//   starts and answers system/init with such an environment.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.Core.Api;
using Malachi.Core.I18n;
using Malachi.Core.Model;
using Malachi.Core.Settings;

namespace Malachi.Core.Assistants;

public static partial class Assistant
{
    /// <summary>
    /// The prefix of the bridge's tools in Claude Code (Go's toolPrefix): the
    /// MCP server is called <see cref="BridgeServer"/> in the
    /// <c>--mcp-config</c> of <see cref="Args"/>.
    /// </summary>
    internal const string BridgeToolPrefix = "mcp__malachi__";

    /// <summary>The name of the bridge's MCP server in <c>--mcp-config</c> and in the init event's <c>mcp_servers</c>.</summary>
    internal const string BridgeServer = "malachi";

    /// <summary>The most of a subject <see cref="ConversationLabel"/> shows, in bytes.</summary>
    internal const int MaxSubject = 200;

    /// <summary>
    /// The most of a reason <see cref="StoppedText"/> (and the search's failure
    /// text) shows, in bytes: room for Claude Code's longer words, whose end
    /// says what helps (its message when it could not refresh its sign-in is
    /// 215 bytes).
    /// </summary>
    internal const int MaxReason = 400;

    /// <summary>The length of a drive root, <c>X:\</c>.</summary>
    internal const int WindowsRootLength = 3;

    /// <summary>The name of Claude Code's native executable.</summary>
    private const string ClaudeExe = "claude.exe";

    /// <summary>The system directory when the parent's SystemRoot is missing or unusable.</summary>
    private const string DefaultSystemRoot = @"C:\Windows";

    /// <summary>The targets, in the order of the menu and the settings (assistant.Targets).</summary>
    public static IReadOnlyList<AssistantTarget> Targets { get; } = [AssistantTarget.Desktop, AssistantTarget.Code, AssistantTarget.App];

    // Models

    /// <summary>The models, in the order of the settings (assistant.Models).</summary>
    public static IReadOnlyList<AssistantModel> Models { get; } = [AssistantModel.Sonnet, AssistantModel.Haiku, AssistantModel.Opus];

    /// <summary>
    /// A stored nick of the gschema enum io.github.schotek.Malachi.AssistantModel
    /// (assistant.ParseModel), compared byte for byte; an unknown or empty
    /// one is Sonnet, the default.
    /// </summary>
    public static AssistantModel ParseModel(string nick) => nick switch
    {
        "haiku" => AssistantModel.Haiku,
        "opus" => AssistantModel.Opus,
        _ => AssistantModel.Sonnet,
    };

    /// <summary>
    /// The nick of a model, which is Claude Code's <c>--model</c> alias (the
    /// Swift raw value, read as <see cref="ParseModel"/> reads it): a value
    /// outside the enum is <c>sonnet</c>.
    /// </summary>
    public static string ModelNick(AssistantModel m) => m switch
    {
        AssistantModel.Haiku => "haiku",
        AssistantModel.Opus => "opus",
        _ => "sonnet",
    };

    /// <summary>
    /// The product name of a model, for the settings and the panel's
    /// subtitle (assistant.ModelName); an unknown model is named as Sonnet.
    /// </summary>
    public static string ModelName(AssistantModel m)
    {
        switch (m)
        {
            case AssistantModel.Haiku:
                // TRANSLATORS: A Claude model name, normally left untranslated.
                return L10n.T("Haiku");
            case AssistantModel.Opus:
                // TRANSLATORS: A Claude model name, normally left untranslated.
                return L10n.T("Opus");
            default:
                // TRANSLATORS: A Claude model name, normally left untranslated.
                return L10n.T("Sonnet");
        }
    }

    // The command line

    /// <summary>
    /// The bare tool names of the daemon's prompt (<see cref="PromptResult.Tools"/>)
    /// as Claude Code's <c>--allowedTools</c> names them (assistant.PrefixTools).
    /// </summary>
    public static IReadOnlyList<string> PrefixTools(IReadOnlyList<string> bare)
    {
        ArgumentNullException.ThrowIfNull(bare);
        return [.. bare.Select(t => BridgeToolPrefix + t)];
    }

    /// <summary>
    /// The arguments of <c>claude</c> (without the executable itself) for
    /// one conversation of the panel, or for a one-shot request without the
    /// bridge (assistant.Args); see the comment at the top of this file.
    /// </summary>
    public static IReadOnlyList<string> Args(AssistantOptions o)
    {
        ArgumentNullException.ThrowIfNull(o);
        List<string> args =
        [
            "-p", "--verbose",
            "--output-format", "stream-json",
            "--include-partial-messages",
            "--input-format", "stream-json",
            "--tools", "",
            "--disallowedTools", "LSP",
            "--disable-slash-commands",
            "--setting-sources", "",
            "--strict-mcp-config",
        ];
        if (o.Bridge.Length > 0)
        {
            args.AddRange([
                "--mcp-config", McpConfig(o.Bridge, o.Socket, o.Prompt.BridgeArgs),
                "--allowedTools", string.Join(",", PrefixTools(o.Prompt.Tools)),
            ]);
        }
        args.AddRange([
            "--permission-mode", "dontAsk",
            "--no-session-persistence",
            "--model", ModelNick(o.Model),
            "--system-prompt", o.Prompt.SystemPrompt,
        ]);
        if (o.Prompt.JsonSchemaText is { Length: > 0 } schema)
        {
            args.AddRange(["--json-schema", schema]);
        }
        return args;
    }

    /// <summary>
    /// The JSON of <c>--mcp-config</c>, as encoding/json writes it
    /// (assistant.mcpConfig): the bridge as the stdio server "malachi", with
    /// <c>--socket</c> when <paramref name="socket"/> is set, then
    /// <paramref name="extra"/> (the daemon's bridge arguments,
    /// <c>--in-app</c> first: the prompts carry the user's profile, so the
    /// bridge keeps it out of its instructions).
    /// </summary>
    internal static string McpConfig(string bridge, string socket, IReadOnlyList<string>? extra = null)
    {
        var output = new List<byte>(bridge.Length + socket.Length + 80);
        output.AddRange(Utf8("{\"mcpServers\":{\"" + BridgeServer + "\":{\"type\":\"stdio\",\"command\":"));
        AppendJsonString(bridge, output);
        output.AddRange(Utf8(",\"args\":["));
        var args = new List<string>();
        if (socket.Length > 0)
        {
            args.Add("--socket");
            args.Add(socket);
        }
        args.AddRange(extra ?? []);
        for (var i = 0; i < args.Count; i++)
        {
            if (i > 0)
            {
                output.Add((byte)',');
            }
            AppendJsonString(args[i], output);
        }
        output.AddRange(Utf8("]}}}"));
        return FromUtf8(output.ToArray());
    }

    /// <summary>
    /// The UI language for the daemon's prompts as a BCP 47 tag ("cs",
    /// "en"), the catalogue's (Swift <c>uiLanguage</c>, GTK
    /// <c>uiLanguageTag</c>); the model answers in it.
    /// </summary>
    public static string UiLanguage => L10n.Catalogue.Language;

    /// <summary>
    /// One turn as Claude Code's stream-json input (assistant.UserMessage), a
    /// JSON object on one line as encoding/json writes it, without the
    /// newline the caller writes after it. Newlines, the line and paragraph
    /// separators and the other control characters of <paramref name="text"/>
    /// are escaped, so the line never breaks.
    /// </summary>
    public static byte[] UserMessage(string text)
    {
        ArgumentNullException.ThrowIfNull(text);
        var output = new List<byte>(text.Length + 80);
        output.AddRange(Utf8("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":[{\"type\":\"text\",\"text\":"));
        AppendJsonString(text, output);
        output.AddRange(Utf8("}]}}"));
        return [.. output];
    }

    // Attachments

    /// <summary>The content types <see cref="AttachmentReadable"/> accepts.</summary>
    private static readonly HashSet<string> ReadableTypes = new(StringComparer.Ordinal)
    {
        "text/plain", "text/csv", "text/markdown", "text/calendar", "application/json",
        "image/png", "image/jpeg", "image/gif", "image/webp",
    };

    /// <summary>
    /// Whether get_attachment returns an attachment of this content type as
    /// it is (text or an image, as the bridge's tools_read.go decides;
    /// assistant.AttachmentReadable): compared without case (ASCII only) and
    /// parameters. For the In App target the attachment item is there only
    /// for these; documents, which the bridge returns as extracted text, are
    /// deliberately not offered.
    /// </summary>
    public static bool AttachmentReadable(string contentType)
    {
        ArgumentNullException.ThrowIfNull(contentType);
        var b = Utf8(contentType);
        var semi = Array.IndexOf(b, (byte)';');
        var (lo, hi) = TrimSpace(b, 0, semi < 0 ? b.Length : semi);
        // Byte for byte: a canonically equivalent spelling is no match.
        return ReadableTypes.Contains(FromUtf8(AsciiLower(b.AsSpan(lo, hi - lo))));
    }

    // Where claude is

    /// <summary>
    /// Where to look for Claude Code, in this order (assistant.CandidatePaths):
    /// the native installer's <c>&lt;home&gt;\.local\bin\claude.exe</c> when
    /// <paramref name="home"/> (USERPROFILE) is a drive-absolute path, then
    /// <c>claude.exe</c> in every directory of <paramref name="pathEnv"/> (a
    /// PATH, ";" separated; a surrounding pair of double quotes is stripped;
    /// empty, relative, UNC and otherwise unusable entries are skipped). The
    /// paths are clean (<see cref="CleanWindowsPath"/>) and duplicates are
    /// dropped without case, the first spelling kept. The caller takes the
    /// first that is a regular file, after the path the settings name
    /// (<c>assistant-claude-path</c>).
    /// </summary>
    /// <remarks>
    /// Windows: only <c>claude.exe</c>, so no nvm, Homebrew or npm places:
    /// npm's <c>claude.cmd</c> would run through cmd.exe, whose parsing of a
    /// command line cannot carry the JSON arguments of <see cref="Args"/>
    /// safely.
    /// </remarks>
    public static IReadOnlyList<string> CandidatePaths(string home, string pathEnv)
    {
        ArgumentNullException.ThrowIfNull(home);
        ArgumentNullException.ThrowIfNull(pathEnv);
        var output = new List<string>();
        var seen = new HashSet<string>(StringComparer.OrdinalIgnoreCase);
        void Add(string dir)
        {
            // A directory that is not drive-absolute itself ("C:", which is
            // relative to the drive's current directory) is refused before
            // the name is joined to it.
            if (CleanWindowsPath(dir) is null)
            {
                return;
            }
            var p = CleanWindowsPath(dir + @"\" + ClaudeExe);
            if (p is not null && seen.Add(p))
            {
                output.Add(p);
            }
        }
        if (CleanWindowsPath(home) is not null)
        {
            Add(home + @"\.local\bin");
        }
        foreach (var entry in pathEnv.Split(';'))
        {
            Add(entry.Length >= 2 && entry[0] == '"' && entry[^1] == '"' ? entry[1..^1] : entry);
        }
        return output;
    }

    /// <summary>
    /// <paramref name="path"/> as a clean drive-absolute Windows path, what
    /// Go's <c>filepath.Clean</c> makes of one, in pure string code (Core
    /// does not depend on the OS it runs on): <c>X:\</c> or <c>X:/</c> first,
    /// '/' turned into '\', repeated separators collapsed, "." segments
    /// dropped, ".." resolved and never above the drive root, no trailing
    /// separator but the root's <c>X:\</c>, the drive letter upper-cased.
    /// Null for a UNC, device or relative path (<c>C:x</c> included), and for
    /// one that holds <c>&lt; &gt; " | ? *</c>, a control character, a ':'
    /// beyond the drive or a lone surrogate (which UTF-8, and so a link or a
    /// command line, cannot carry).
    /// </summary>
    internal static string? CleanWindowsPath(string path)
    {
        if (path.Length < WindowsRootLength || !char.IsAsciiLetter(path[0]) || path[1] != ':' || path[2] is not ('\\' or '/'))
        {
            return null;
        }
        for (var i = 0; i < path.Length; i++)
        {
            var c = path[i];
            if (IsControl(c) || c is '<' or '>' or '"' or '|' or '?' or '*' || (c == ':' && i != 1))
            {
                return null;
            }
            if (char.IsHighSurrogate(c) && i + 1 < path.Length && char.IsLowSurrogate(path[i + 1]))
            {
                i++;
                continue;
            }
            if (char.IsSurrogate(c))
            {
                return null;
            }
        }
        var parts = new List<string>();
        foreach (var segment in path[WindowsRootLength..].Split('\\', '/'))
        {
            if (segment.Length == 0 || segment == ".")
            {
                continue;
            }
            if (segment == "..")
            {
                if (parts.Count > 0)
                {
                    parts.RemoveAt(parts.Count - 1);
                }
                continue;
            }
            parts.Add(segment);
        }
        return char.ToUpperInvariant(path[0]) + @":\" + string.Join('\\', parts);
    }

    /// <summary>
    /// The directory of a clean drive-absolute path (Go's <c>filepath.Dir</c>
    /// of one): everything before the last '\', <c>X:\</c> for a file at the
    /// root.
    /// </summary>
    internal static string WindowsDirectory(string clean)
    {
        var slash = clean.LastIndexOf('\\');
        return slash < WindowsRootLength ? clean[..WindowsRootLength] : clean[..slash];
    }

    /// <summary><paramref name="rest"/> under the clean directory <paramref name="dir"/>, which ends with '\' only as a drive root.</summary>
    private static string UnderWindowsDirectory(string dir, string rest) =>
        dir.EndsWith('\\') ? dir + rest : dir + @"\" + rest;

    // The child's environment

    /// <summary>
    /// The variables of the application's environment the child keeps,
    /// spelt as Windows spells them; a parent's key matches without case.
    /// </summary>
    private static readonly string[] ChildEnvKeys =
    [
        "SystemRoot", "windir", "SystemDrive", "ComSpec", "PATHEXT", "OS",
        "NUMBER_OF_PROCESSORS", "PROCESSOR_ARCHITECTURE",
        "USERPROFILE", "HOMEDRIVE", "HOMEPATH", "USERNAME", "USERDOMAIN", "COMPUTERNAME",
        "APPDATA", "LOCALAPPDATA", "ProgramData",
        "ProgramFiles", "ProgramFiles(x86)", "ProgramW6432",
        "CommonProgramFiles", "CommonProgramFiles(x86)", "CommonProgramW6432",
        "ALLUSERSPROFILE", "PUBLIC", "TEMP", "TMP",
        "LANG", "LC_ALL", "LC_CTYPE",
    ];

    /// <summary>
    /// Makes Claude Code connect its MCP servers before the init event and
    /// the first turn: Claude Code 2.1.178 starts them in the background by
    /// default, init reports the bridge "pending" with no tool, and the
    /// model, offered no tool, writes its calls out as text.
    /// </summary>
    private const string McpBlocking = "MCP_CONNECTION_NONBLOCKING=false";

    /// <summary><see cref="ChildEnvKeys"/> by any spelling of their names.</summary>
    private static readonly Dictionary<string, string> ChildEnvKeySpelling =
        ChildEnvKeys.ToDictionary(k => k, k => k, StringComparer.OrdinalIgnoreCase);

    /// <summary>
    /// The environment of the panel's Claude Code, as "KEY=value" entries
    /// sorted as Go sorts them (by code points, the order of the UTF-8 bytes;
    /// assistant.ChildEnv).
    /// </summary>
    /// <remarks>
    /// The variables of <see cref="ChildEnvKeys"/> from
    /// <paramref name="parent"/> when set there (keys compared without case,
    /// written as spelt in that list, the last entry of a key wins), and a
    /// PATH of the directory of <paramref name="claudePath"/> followed by
    /// <c>R\System32;R;R\System32\Wbem;R\System32\WindowsPowerShell\v1.0</c>,
    /// where R is the parent's SystemRoot when that is a clean drive-absolute
    /// path (<see cref="CleanWindowsPath"/> leaves it as it is) without a ';',
    /// and <c>C:\Windows</c> otherwise. The directory of claude is left out
    /// unless <paramref name="claudePath"/> is such a path too and its
    /// directory holds no ';'. And <c>MCP_CONNECTION_NONBLOCKING=false</c>
    /// (<see cref="McpBlocking"/>). Nothing else: no CLAUDE* or ANTHROPIC*
    /// variable of a surrounding session, no MALACHI_* (the socket goes on
    /// the command line), not the parent's PATH.
    /// </remarks>
    public static IReadOnlyList<string> ChildEnv(IEnumerable<string> parent, string claudePath)
    {
        ArgumentNullException.ThrowIfNull(parent);
        ArgumentNullException.ThrowIfNull(claudePath);
        var kept = new Dictionary<string, string>(StringComparer.Ordinal);
        foreach (var entry in parent)
        {
            var eq = entry.IndexOf('=', StringComparison.Ordinal);
            if (eq > 0 && ChildEnvKeySpelling.TryGetValue(entry[..eq], out var key))
            {
                kept[key] = entry[(eq + 1)..];
            }
        }
        var root = kept.TryGetValue("SystemRoot", out var systemRoot) && IsCleanWindowsDirectory(systemRoot)
            ? systemRoot
            : DefaultSystemRoot;
        var path = UnderWindowsDirectory(root, "System32") + ";" + root + ";" + UnderWindowsDirectory(root, @"System32\Wbem")
            + ";" + UnderWindowsDirectory(root, @"System32\WindowsPowerShell\v1.0");
        if (string.Equals(CleanWindowsPath(claudePath), claudePath, StringComparison.Ordinal))
        {
            var dir = WindowsDirectory(claudePath);
            if (!dir.Contains(';', StringComparison.Ordinal))
            {
                path = dir + ";" + path;
            }
        }
        return [.. kept.Select(kv => kv.Key + "=" + kv.Value).Append("PATH=" + path).Append(McpBlocking).Order(CodePoints.Comparer)];
    }

    /// <summary>Whether <paramref name="dir"/> is clean and drive-absolute and can stand in a PATH (no ';').</summary>
    private static bool IsCleanWindowsDirectory(string dir) =>
        string.Equals(CleanWindowsPath(dir), dir, StringComparison.Ordinal) && !dir.Contains(';', StringComparison.Ordinal);

    /// <summary>
    /// <see cref="ChildEnv"/> as a dictionary for <c>ProcessStartInfo.Environment</c>,
    /// its keys compared without case as Windows compares them.
    /// </summary>
    public static Dictionary<string, string> ChildEnvironment(IReadOnlyDictionary<string, string> parent, string claudePath)
    {
        ArgumentNullException.ThrowIfNull(parent);
        var output = new Dictionary<string, string>(StringComparer.OrdinalIgnoreCase);
        foreach (var entry in ChildEnv(parent.Select(kv => kv.Key + "=" + kv.Value), claudePath))
        {
            var eq = entry.IndexOf('=', StringComparison.Ordinal);
            output[entry[..eq]] = entry[(eq + 1)..];
        }
        return output;
    }

    // Texts

    /// <summary>The panel's fixed texts, translated (assistant.PanelTexts).</summary>
    public static PanelStrings PanelTexts() => new()
    {
        // TRANSLATORS: Placeholder of the assistant panel's question field.
        Placeholder = L10n.T("Ask about your mail…"),
        ReplyPlaceholder = L10n.T("What should the reply say?"),
        AskPlaceholder = L10n.T("What do you want to know?"),
        Send = L10n.T("_Send"),
        Stop = L10n.T("Stop"),
        NewConversation = L10n.T("New Conversation"),
        // TRANSLATORS: The context of the assistant panel.
        SelectedMessage = L10n.T("Selected message"),
        // TRANSLATORS: The context of the assistant panel.
        AllMail = L10n.T("All mail"),
        DraftReady = L10n.T("A draft is ready"),
        OpenDraft = L10n.T("Open Draft"),
        NotSignedIn = L10n.T("Claude Code is not signed in"),
        ToolsMissing = L10n.T("The Malachi Mail tools are not available to the assistant"),
        Stopped = L10n.T("The conversation was stopped"),
        TryAgain = L10n.T("Try Again"),
        DraftGone = L10n.T("The draft is no longer there"),
        Footer = L10n.T("Mail you ask about is sent to Claude under your account"),
        ConsentHeading = L10n.T("Send Mail to Claude?"),
        ConsentBody = L10n.T("The assistant reads the messages you ask about and sends their content to Anthropic under your Claude account. Messages may contain instructions from their senders: the assistant is told not to follow them, and it cannot send, move or delete anything."),
        Allow = L10n.T("Allow"),
        Cancel = L10n.T("_Cancel"),
        Show = L10n.T("Show Assistant"),
        Hide = L10n.T("Hide Assistant"),
        Model = L10n.T("Model"),
        Choose = L10n.T("Choose…"),
        SignedIn = L10n.T("Signed in"),
        NotSignedInShort = L10n.T("Not signed in"),
        NotFound = L10n.T("Claude Code was not found on this computer"),
        // TRANSLATORS: A bar in the assistant panel: the conversation is about other mail than the message selected in the list.
        AnotherSelected = L10n.T("Another message is selected"),
        // TRANSLATORS: A button of the bar "Another message is selected": the assistant may talk about that message too.
        AddToConversation = L10n.T("Add to Conversation"),
    };

    /// <summary>
    /// The panel's context chip for a context of <paramref name="n"/>
    /// messages (assistant.ContextLabel): 0 (no selection, or the user
    /// removed it) "All mail", 1 "Selected message", more a conversation with
    /// its count.
    /// </summary>
    public static string ContextLabel(int n)
    {
        if (n <= 0)
        {
            // TRANSLATORS: The context of the assistant panel.
            return L10n.T("All mail");
        }
        if (n == 1)
        {
            // TRANSLATORS: The context of the assistant panel.
            return L10n.T("Selected message");
        }
        return ConversationContext(n);
    }

    /// <summary>The context chip for a conversation of <paramref name="n"/> messages (the plural half of <see cref="ContextLabel"/>).</summary>
    public static string ConversationContext(int n) =>
        // TRANSLATORS: The context of the assistant panel.
        L10n.N("Selected conversation (%d message)", "Selected conversation (%d messages)", n);

    /// <summary>
    /// The panel's context chip once a conversation keeps its context (its
    /// first question pinned what the chip showed, and later selections
    /// change nothing; assistant.ConversationLabel): for one message or one
    /// conversation (<paramref name="messages"/> ≤ 1) "Conversation about: "
    /// and its subject, the mail text as one line (<c>OneLine</c>), or
    /// <c>ContextLabel(1)</c> when no subject is left; for several messages,
    /// the selections added to the conversation counted once each, their
    /// count. The caller keeps "All mail" (<c>ContextLabel(0)</c>) for a
    /// conversation about no messages.
    /// </summary>
    public static string ConversationLabel(string subject, int messages)
    {
        ArgumentNullException.ThrowIfNull(subject);
        if (messages > 1)
        {
            // TRANSLATORS: The context of the assistant panel: the conversation is about several messages.
            return L10n.N("Conversation about %d message", "Conversation about %d messages", messages);
        }
        var s = OneLine(subject, MaxSubject);
        if (s.Length > 0)
        {
            // TRANSLATORS: %s is the subject of the message the conversation is about.
            return L10n.T("Conversation about: %s", s);
        }
        return ContextLabel(1);
    }

    /// <summary>
    /// The transcript's error line when a turn ended badly
    /// (assistant.StoppedText). <paramref name="reason"/> is technical (the
    /// result's text or subtype, or Claude Code's stderr) and shown as data:
    /// its first non-empty line without control characters, at most 400
    /// bytes (cut at a character boundary); "unknown" when nothing is left.
    /// </summary>
    public static string StoppedText(string reason)
    {
        ArgumentNullException.ThrowIfNull(reason);
        // TRANSLATORS: %s is a technical reason.
        return L10n.T("The assistant stopped: %s", FirstLine(reason, MaxReason));
    }

    /// <summary>
    /// The transcript's line while the panel's Claude Code runs a tool, by
    /// the tool's name (without the <c>mcp__malachi__</c> prefix; a prefixed
    /// name is read the same; assistant.ActivityLabel).
    /// </summary>
    public static string ActivityLabel(string tool)
    {
        ArgumentNullException.ThrowIfNull(tool);
        return StripBridgePrefix(tool) switch
        {
            "read_message" => L10n.T("Reading a message…"),
            "list_messages" => L10n.T("Listing messages…"),
            "search_messages" => L10n.T("Searching mail…"),
            "list_accounts" => L10n.T("Listing accounts…"),
            "list_folders" => L10n.T("Listing folders…"),
            "get_attachment" => L10n.T("Reading an attachment…"),
            "create_draft" => L10n.T("Saving a draft…"),
            "get_user_profile" => L10n.T("Reading your profile…"),
            "propose_profile_note" => L10n.T("Suggesting a note for your profile…"),
            _ => L10n.T("Using a tool…"),
        };
    }

    /// <summary><paramref name="name"/> without <see cref="BridgeToolPrefix"/> (strings.TrimPrefix), byte for byte.</summary>
    internal static string StripBridgePrefix(string name) =>
        name.StartsWith(BridgeToolPrefix, StringComparison.Ordinal) ? name[BridgeToolPrefix.Length..] : name;
}
