// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The two runs of the network canary (docs/windows-port.md §12), made once
// for all its assertions and side by side, so the whole takes well under two
// minutes:
//
// - protected: the app's viewer, editor, previewer and conversation card
//   (MessageWebView, ComposeWebView, PreviewWebView, CardWebView in the
//   app's WebViewEnvironment) over the
//   hostile document, its active twin (hover, press, link, form, target,
//   middle click, mailto, download, the security audit's masked links), a
//   meta refresh, the previewer's SVG, PDF (its link clicked, its open
//   action), picture and text, and every HTML part of backend/testdata/mime
//   raw in the viewer, the editor and the card; the card also measures a
//   document of a known height, at two zooms;
// - control: the same hostile document in a WebView2 without any
//   protection (only its reach beyond the machine cut off: a dead proxy and
//   no name but 127.0.0.1 resolved), which must reach the canaries: the
//   proof that the harness sees what it looks for;
// - recovery: the app's views with harmless documents whose renderers are
//   crashed twice each (DevTools Page.crash, also while a document loads)
//   and hung once (a host script that never ends): each document is shown
//   again once and then given up (RendererRecovery), never reloaded in a
//   loop. A run of its own because a hang takes Chromium's hang monitor
//   about 15 s to report.

using System;
using System.Collections.Generic;
using System.IO;
using System.Linq;
using System.Reflection;
using System.Threading.Tasks;
using Malachi.App.Canary.Host;
using Microsoft.Win32;
using Xunit;

namespace Malachi.App.Canary;

/// <summary>Runs the canary once for the tests of <see cref="NetworkCanaryTests"/>.</summary>
public sealed class CanaryFixture : IAsyncLifetime
{
    private const string WebView2ClientKey = @"Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}";

    private string? root;

    /// <summary>Why the canary cannot run here; null when it ran.</summary>
    public string? SkipReason { get; private set; }

    /// <summary>The run's id (in the DNS canary names).</summary>
    public string RunId { get; } = Guid.NewGuid().ToString("N")[..8];

    /// <summary>The corpus rendered raw.</summary>
    public IReadOnlyList<(string File, string Html)> Corpus { get; private set; } = [];

    internal CanaryRun? Protected { get; private set; }

    internal CanaryRun? Control { get; private set; }

    internal CanaryRun? Recovery { get; private set; }

    /// <summary>The file the editor gets dropped on it.</summary>
    internal string DroppedFile => Path.Combine(root ?? Path.GetTempPath(), "dropped file.txt");

    /// <summary>The repository root baked in at build time.</summary>
    internal static string RepositoryRoot => Metadata("MalachiRoot");

    // A GitHub-hosted runner may well have a desktop session
    // (Environment.UserInteractive cannot tell), but it is a Windows Server
    // image whose desktop, graphics and WebView2 runtime change with the
    // image, not the desktop the canary's expectations were measured on
    // (docs/windows-port.md §12: the runtime's own background requests, the
    // NetLog's event types, the windows WebView2 draws in). A red canary
    // there would say more about the runner than about the app, and a green
    // one would not replace the run on a desktop, so CI skips it by name;
    // .github/workflows/windows.yml runs it on request (workflow_dispatch
    // with canary).
    private static bool OnCiRunner => Environment.GetEnvironmentVariable("GITHUB_ACTIONS") == "true";

    public async ValueTask InitializeAsync()
    {
        if (!OperatingSystem.IsWindows())
        {
            SkipReason = "the network canary needs Windows";
            return;
        }
        if (OnCiRunner && Environment.GetEnvironmentVariable("MALACHI_CANARY") != "1")
        {
            SkipReason = "the network canary is not run on CI runners (MALACHI_CANARY=1 runs it there): it needs a "
                + "desktop session with the WebView2 runtime; make test-windows on a Windows desktop runs it";
            return;
        }
        if (!Environment.UserInteractive)
        {
            SkipReason = "the network canary needs a desktop session (this process is not interactive)";
            return;
        }
        if (!WebView2Installed())
        {
            SkipReason = "the network canary needs the WebView2 runtime, which is not installed";
            return;
        }
        var host = Metadata("MalachiCanaryHost");
        if (!File.Exists(host))
        {
            throw new FileNotFoundException("the canary host was not built", host);
        }
        Corpus = MimeCorpus.HtmlParts(Path.Combine(RepositoryRoot, "backend", "testdata", "mime"));
        root = Path.Combine(Path.GetTempPath(), "malachi-canary-" + RunId);
        Directory.CreateDirectory(root);
        await File.WriteAllTextAsync(DroppedFile, "dropped");
        Protected = new CanaryRun("protected", Path.Combine(root, "p"), HostileDocuments.AllVectors);
        Control = new CanaryRun("control", Path.Combine(root, "c"), HostileDocuments.AllVectors);
        Recovery = new CanaryRun("recovery", Path.Combine(root, "r"), []);
        await Task.WhenAll(
            Protected.RunAsync(host, Protected.Config(HostConfig.Modes.Protected, ProtectedSteps(Protected))),
            Control.RunAsync(host, Control.Config(HostConfig.Modes.Control, ControlSteps(Control))),
            Recovery.RunAsync(host, Recovery.Config(HostConfig.Modes.Protected, RecoverySteps())));
    }

    public async ValueTask DisposeAsync()
    {
        Protected?.Dispose();
        Control?.Dispose();
        Recovery?.Dispose();
        // MALACHI_CANARY_KEEP=1 keeps the runs' files (configuration,
        // results, NetLogs) for a look after a failure.
        if (root is null || Environment.GetEnvironmentVariable("MALACHI_CANARY_KEEP") == "1")
        {
            return;
        }
        // The browser processes may still hold their user data for a moment.
        for (var attempt = 0; attempt < 10 && Directory.Exists(root); attempt++)
        {
            try
            {
                Directory.Delete(root, recursive: true);
            }
            catch (Exception e) when (e is IOException or UnauthorizedAccessException)
            {
                await Task.Delay(500);
            }
        }
    }

    private List<HostStep> ProtectedSteps(CanaryRun run)
    {
        // How long a click may take to reach the reader as a link (the
        // recovery run's Expect).
        const int LinkLimit = 20_000;
        var passive = HostileDocuments.Passive(run.Canary, RunId);
        var active = HostileDocuments.Active(run.Canary);
        var refresh = HostileDocuments.Refresh(run.Canary);
        var steps = new List<HostStep>();
        void Add(string view, string op, string? phase = null, string? html = null, string? target = null, int ms = 0) =>
            steps.Add(new HostStep { View = view, Op = op, Phase = phase, Html = html, Target = target, Ms = ms });
        void Show(string name, string type, byte[] data, int ms, string phase) =>
            steps.Add(new HostStep { View = "preview", Op = "show", Phase = phase, Name = name, ContentType = type, Data = Convert.ToBase64String(data), Ms = ms });

        // A click that must reach the reader as a link: the step waits for
        // the link, which a busy machine may bring later than any fixed
        // pause (and a late one would count for the next phase), then as
        // long as before for whatever must not follow.
        void ClickForLink(string op, string phase, string target, int ms)
        {
            Add("viewer", op, phase, target: target);
            steps.Add(new HostStep { View = "viewer", Op = "await", Phase = phase, Target = HostEvent.Kinds.Link, Ms = LinkLimit, InPhase = true });
            Add("viewer", "wait", phase, ms: ms);
        }

        // The viewer: everything passive, then every activation.
        Add("viewer", "load", "viewer-passive", passive, ms: 2500);
        Add("viewer", "load", "viewer-hover", active, ms: 200);
        Add("viewer", "hover", target: "hover", ms: 1200);
        Add("viewer", "press", target: "hover", ms: 600);
        // Every activation is cancelled, so the page stays for the next one.
        ClickForLink("click", "viewer-nav", "pinglink", 1200);
        Add("viewer", "click", "viewer-form", target: "sub", ms: 1200);
        Add("viewer", "click", "viewer-blank", target: "blank", ms: 800);
        ClickForLink("middle", "viewer-middle", "middle", 800);
        ClickForLink("click", "viewer-mailto", "mailto", 600);
        Add("viewer", "click", "viewer-download", target: "dl", ms: 800);
        Add("viewer", "click", "viewer-unc", target: "unc", ms: 800);
        // The security audit's masked links, one by one: each must reach
        // the reader as the link it is, and nothing else.
        foreach (var (id, _) in HostileDocuments.MaskedLinks)
        {
            ClickForLink("click", "viewer-masked-" + id, "masked-" + id, 500);
        }
        Add("viewer", "load", "viewer-refresh", refresh, ms: 2000);

        // The editor: the same content pasted, with page script on.
        Add("editor", "load", "editor-passive", passive, ms: 2500);
        Add("editor", "load", "editor-active", active, ms: 200);
        Add("editor", "hover", target: "hover", ms: 800);
        Add("editor", "click", target: "pinglink", ms: 800);
        Add("editor", "click", target: "sub", ms: 800);
        Add("editor", "click", target: "unc", ms: 600);
        Add("editor", "load", "editor-refresh", refresh, ms: 2000);

        // The editor's bridge under its CSP: typing, a format command, a
        // flush, a file dropped on the page.
        Add("editor", "load", "editor-bridge", "<p>start</p>", ms: 300);
        Add("editor", "focus", ms: 200);
        Add("editor", "type", html: "hello ", ms: 100);
        steps.Add(new HostStep { View = "editor", Op = "exec", Name = "bold", Ms = 100 });
        Add("editor", "type", html: "world", ms: 100);
        Add("editor", "flush");
        steps.Add(new HostStep { View = "editor", Op = "drop", X = 200, Y = 100, Name = DroppedFile, Ms = 500 });

        // The card: the hostile document passive and active (its links
        // reach the conversation view as the viewer's reach the reader), and
        // a document of a known height, measured by the host, at 100 % and
        // at 150 %.
        Add("card", "load", "card-passive", passive, ms: 2500);
        Add("card", "load", "card-hover", active, ms: 200);
        Add("card", "hover", target: "hover", ms: 1200);
        Add("card", "click", "card-nav", target: "pinglink");
        steps.Add(new HostStep { View = "card", Op = "await", Phase = "card-nav", Target = HostEvent.Kinds.Link, Ms = LinkLimit, InPhase = true });
        Add("card", "wait", "card-nav", ms: 1200);
        Add("card", "click", "card-form", target: "sub", ms: 1200);
        Add("card", "click", "card-blank", target: "blank", ms: 800);
        Add("card", "click", "card-unc", target: "unc", ms: 800);
        Add("card", "load", "card-refresh", refresh, ms: 2000);
        Add("card", "load", "card-size", "<div style='height:500px'>tall</div>");
        steps.Add(new HostStep { View = "card", Op = "await", Phase = "card-size", Target = HostEvent.Kinds.Size, Ms = LinkLimit, InPhase = true });
        Add("card", "wait", "card-size", ms: 300);
        steps.Add(new HostStep { View = "card", Op = "zoom", Phase = "card-zoom", X = 150 });
        steps.Add(new HostStep { View = "card", Op = "await", Phase = "card-zoom", Target = HostEvent.Kinds.Size, Ms = LinkLimit, InPhase = true });
        Add("card", "wait", "card-zoom", ms: 300);
        steps.Add(new HostStep { View = "card", Op = "zoom", Phase = "card-zoom-back", X = 100, Ms = 300 });

        // The viewer's text zoom, set while a message is on display.
        Add("viewer", "load", "viewer-zoom", "<p>zoom</p>", ms: 200);
        steps.Add(new HostStep { View = "viewer", Op = "zoom", X = 150, Ms = 200 });
        Add("viewer", "probe", html: "String(getComputedStyle(Object.getOwnPropertyDescriptor(Document.prototype, 'documentElement').get.call(document)).zoom)");
        steps.Add(new HostStep { View = "viewer", Op = "zoom", X = 100 });

        // The previewer: HTML and SVG as source, a PDF with a link, a
        // picture, text.
        Show("page.html", "text/html", System.Text.Encoding.UTF8.GetBytes(passive), 1000, "preview-html");
        Show("drawing.svg", "image/svg+xml", HostileDocuments.Svg(run.Canary), 800, "preview-svg");
        Show("canary.pdf", "application/pdf", HostileDocuments.Pdf(run.Canary), 1500, "preview-pdf");
        steps.Add(new HostStep { View = "preview", Op = "click", X = 400, Y = 300, Ms = 1500 });
        // The window titles while a PDF (with a title of its own) and a
        // picture are shown.
        Add("preview", "titles");
        Show("picture.png", "image/png", Convert.FromBase64String(
            "iVBORw0KGgoAAAANSUhEUgAAAAcAAAAFCAIAAAAG+GGPAAAAEUlEQVR42mNQaHiAiRhoJAoALlM0gX31oMMAAAAASUVORK5CYII="), 400, "preview-png");
        Add("preview", "titles");
        Show("notes.txt", "text/plain", System.Text.Encoding.UTF8.GetBytes(active), 400, "preview-text");

        // The corpus, raw, in the viewer and the editor.
        var i = 0;
        foreach (var (file, html) in Corpus)
        {
            var phase = "corpus-" + (i++).ToString(System.Globalization.CultureInfo.InvariantCulture) + "-" + file;
            Add("viewer", "load", phase, html, ms: 250);
            Add("editor", "load", html: html, ms: 250);
            Add("card", "load", html: html, ms: 250);
        }
        return steps;
    }

    private List<HostStep> ControlSteps(CanaryRun run)
    {
        var active = HostileDocuments.Active(run.Canary);
        return
        [
            new() { View = "control", Op = "load", Phase = "control-passive", Html = HostileDocuments.Passive(run.Canary, RunId), Ms = 2500 },
            new() { View = "control", Op = "load", Phase = "control-hover", Html = active, Ms = 200 },
            new() { View = "control", Op = "hover", Target = "hover", Ms = 1200 },
            new() { View = "control", Op = "click", Phase = "control-nav", Target = "pinglink", Ms = 1200 },
            // The link navigated: the active page again for the form.
            new() { View = "control", Op = "load", Phase = "control-form", Html = active, Ms = 200 },
            new() { View = "control", Op = "click", Target = "sub", Ms = 1200 },
            new() { View = "control", Op = "load", Phase = "control-unc", Html = active, Ms = 200 },
            new() { View = "control", Op = "click", Target = "unc", Ms = 800 },
            new() { View = "control", Op = "load", Phase = "control-refresh", Html = HostileDocuments.Refresh(run.Canary), Ms = 2000 },
        ];
    }

    // Each view's renderer crashed twice under one document (the second
    // time after the view showed it again), the viewer's also while a
    // document loads, then hung once; between them a document of its own
    // shows the view still works. Every step waits for what it expects (a
    // new renderer may take seconds to start on a busy machine), then a
    // moment for what must not follow. Phases name what the tests look at.
    private static List<HostStep> RecoverySteps()
    {
        const int Expect = 20_000;
        const int Settle = 1000;
        const string Loaded = "True";
        var steps = new List<HostStep>();
        void Add(string view, string op, string phase, string? html = null, int ms = 0, string? target = null) =>
            steps.Add(new HostStep { View = view, Op = op, Phase = phase, Html = html, Ms = ms, Target = target });
        void CrashTwice(string view)
        {
            Add(view, "crash", view + "-crash-1");
            Add(view, "await", view + "-crash-1", Loaded, Expect, HostEvent.Kinds.Completed);
            Add(view, "wait", view + "-crash-1", ms: Settle);
            Add(view, "crash", view + "-crash-2");
            Add(view, "await", view + "-crash-2", null, Expect, HostEvent.Kinds.Unavailable);
            Add(view, "wait", view + "-crash-2", ms: Settle);
        }

        Add("viewer", "load", "viewer-crash", "<p>crash</p>", 300);
        CrashTwice("viewer");
        Add("viewer", "load", "viewer-after", "<p>after</p>", 300);
        // A body large enough to be still loading when its renderer dies, or
        // when its load is stopped (a failed navigation, no process report).
        var large = "<p>" + string.Concat(Enumerable.Repeat("lorem ipsum dolor sit amet ", 100_000)) + "</p>";
        Add("viewer", "loadcrash", "viewer-loadcrash", large);
        Add("viewer", "await", "viewer-loadcrash", Loaded, Expect, HostEvent.Kinds.Completed);
        Add("viewer", "wait", "viewer-loadcrash", ms: Settle);
        Add("viewer", "loadstop", "viewer-loadstop", large + "<p>stopped</p>");
        Add("viewer", "await", "viewer-loadstop", null, Expect, HostEvent.Kinds.Unavailable);
        Add("viewer", "wait", "viewer-loadstop", ms: Settle);

        Add("editor", "load", "editor-crash", "<p>text</p>", 300);
        CrashTwice("editor");

        Add("card", "load", "card-crash", "<p>crash</p>", 300);
        CrashTwice("card");

        steps.Add(new HostStep
        {
            View = "preview",
            Op = "show",
            Phase = "preview-crash",
            Name = "picture.png",
            ContentType = "image/png",
            Ms = 300,
            Data = "iVBORw0KGgoAAAANSUhEUgAAAAcAAAAFCAIAAAAG+GGPAAAAEUlEQVR42mNQaHiAiRhoJAoALlM0gX31oMMAAAAASUVORK5CYII=",
        });
        CrashTwice("preview");

        // Reported by Chromium's hang monitor about 15 s after the input the
        // renderer left unanswered, then RendererRecovery.AnswerTimeout.
        Add("viewer", "load", "viewer-hang", "<p>hang</p>", 300);
        Add("viewer", "hang", "viewer-hang-1");
        Add("viewer", "await", "viewer-hang-1", "again", 40_000, HostEvent.Kinds.Ready);
        Add("viewer", "await", "viewer-hang-1", Loaded, Expect, HostEvent.Kinds.Completed);
        Add("viewer", "wait", "viewer-hang-1", ms: Settle);
        Add("viewer", "load", "viewer-after-hang", "<p>after the hang</p>", 300);
        return steps;
    }

    private static bool WebView2Installed()
    {
        foreach (var (hive, key) in new[]
        {
            (Registry.LocalMachine, @"SOFTWARE\WOW6432Node\" + WebView2ClientKey),
            (Registry.LocalMachine, @"SOFTWARE\" + WebView2ClientKey),
            (Registry.CurrentUser, @"Software\" + WebView2ClientKey),
        })
        {
            using var k = hive.OpenSubKey(key);
            if (k?.GetValue("pv") is string version && version.Length > 0 && version != "0.0.0.0")
            {
                return true;
            }
        }
        return false;
    }

    private static string Metadata(string key) =>
        typeof(CanaryFixture).Assembly.GetCustomAttributes<AssemblyMetadataAttribute>().First(a => a.Key == key).Value
        ?? throw new InvalidOperationException("no " + key);
}
