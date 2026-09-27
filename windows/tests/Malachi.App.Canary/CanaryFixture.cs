// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The two runs of the network canary (docs/windows-port.md §12), made once
// for all its assertions and side by side, so the whole takes well under two
// minutes:
//
// - protected: the app's viewer, editor and previewer (MessageWebView,
//   ComposeWebView, PreviewWebView in the app's WebViewEnvironment) over the
//   hostile document, its active twin (hover, press, link, form, target,
//   middle click, mailto, download), a meta refresh, the previewer's SVG,
//   PDF (its link clicked, its open action), picture and text, and every
//   HTML part of backend/testdata/mime raw in the viewer and the editor;
// - control: the same hostile document in a WebView2 without any
//   protection (only its reach beyond the machine cut off: a dead proxy and
//   no name but 127.0.0.1 resolved), which must reach the canaries: the
//   proof that the harness sees what it looks for.

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

    /// <summary>The file the editor gets dropped on it.</summary>
    internal string DroppedFile => Path.Combine(root ?? Path.GetTempPath(), "dropped file.txt");

    /// <summary>The repository root baked in at build time.</summary>
    internal static string RepositoryRoot => Metadata("MalachiRoot");

    public async ValueTask InitializeAsync()
    {
        if (!OperatingSystem.IsWindows())
        {
            SkipReason = "the network canary needs Windows";
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
        await Task.WhenAll(
            Protected.RunAsync(host, Protected.Config(HostConfig.Modes.Protected, ProtectedSteps(Protected))),
            Control.RunAsync(host, Control.Config(HostConfig.Modes.Control, ControlSteps(Control))));
    }

    public async ValueTask DisposeAsync()
    {
        Protected?.Dispose();
        Control?.Dispose();
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
        var passive = HostileDocuments.Passive(run.Canary, RunId);
        var active = HostileDocuments.Active(run.Canary);
        var refresh = HostileDocuments.Refresh(run.Canary);
        var steps = new List<HostStep>();
        void Add(string view, string op, string? phase = null, string? html = null, string? target = null, int ms = 0) =>
            steps.Add(new HostStep { View = view, Op = op, Phase = phase, Html = html, Target = target, Ms = ms });
        void Show(string name, string type, byte[] data, int ms, string phase) =>
            steps.Add(new HostStep { View = "preview", Op = "show", Phase = phase, Name = name, ContentType = type, Data = Convert.ToBase64String(data), Ms = ms });

        // The viewer: everything passive, then every activation.
        Add("viewer", "load", "viewer-passive", passive, ms: 2500);
        Add("viewer", "load", "viewer-hover", active, ms: 200);
        Add("viewer", "hover", target: "hover", ms: 1200);
        Add("viewer", "press", target: "hover", ms: 600);
        // Every activation is cancelled, so the page stays for the next one.
        Add("viewer", "click", "viewer-nav", target: "pinglink", ms: 1200);
        Add("viewer", "click", "viewer-form", target: "sub", ms: 1200);
        Add("viewer", "click", "viewer-blank", target: "blank", ms: 800);
        Add("viewer", "middle", "viewer-middle", target: "middle", ms: 800);
        Add("viewer", "click", "viewer-mailto", target: "mailto", ms: 600);
        Add("viewer", "click", "viewer-download", target: "dl", ms: 800);
        Add("viewer", "load", "viewer-refresh", refresh, ms: 2000);

        // The editor: the same content pasted, with page script on.
        Add("editor", "load", "editor-passive", passive, ms: 2500);
        Add("editor", "load", "editor-active", active, ms: 200);
        Add("editor", "hover", target: "hover", ms: 800);
        Add("editor", "click", target: "pinglink", ms: 800);
        Add("editor", "click", target: "sub", ms: 800);
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
        Show("picture.png", "image/png", Convert.FromBase64String(
            "iVBORw0KGgoAAAANSUhEUgAAAAcAAAAFCAIAAAAG+GGPAAAAEUlEQVR42mNQaHiAiRhoJAoALlM0gX31oMMAAAAASUVORK5CYII="), 400, "preview-png");
        Show("notes.txt", "text/plain", System.Text.Encoding.UTF8.GetBytes(active), 400, "preview-text");

        // The corpus, raw, in the viewer and the editor.
        var i = 0;
        foreach (var (file, html) in Corpus)
        {
            var phase = "corpus-" + (i++).ToString(System.Globalization.CultureInfo.InvariantCulture) + "-" + file;
            Add("viewer", "load", phase, html, ms: 250);
            Add("editor", "load", html: html, ms: 250);
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
            new() { View = "control", Op = "load", Phase = "control-refresh", Html = HostileDocuments.Refresh(run.Canary), Ms = 2000 },
        ];
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
