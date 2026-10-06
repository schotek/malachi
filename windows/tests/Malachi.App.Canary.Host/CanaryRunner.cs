// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The canary host's run (docs/windows-port.md §12): the app's viewer, editor,
// previewer, conversation card and board card (the same CardWebView in the
// board detail's conversation; or, in the control run, a WebView2
// without any protection)
// in one window beyond the edge of the screen, the steps of the
// configuration played on them, and everything they did recorded. Pointer
// actions go through the DevTools protocol (Input.dispatchMouseEvent), which
// needs no real pointer and no foreground window, and so do a renderer's
// crash (Page.crash) and its hang (a host script that never ends); keys are
// not tested here (CDP keys bypass AreBrowserAcceleratorKeysEnabled,
// INPUT-SPIKES.md). A view that replaces its control after a failed process
// is observed again from the new one on (CoreWebViewInitialized). The
// browser's processes keep the normal priority class whatever Chromium sets
// (ProcessPriority). The counting of connections and lookups is the
// test's, from its listeners and the NetLog this run makes the browser
// write.

using System;
using System.Collections.Generic;
using System.Globalization;
using System.IO;
using System.Linq;
using System.Runtime.InteropServices;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Malachi.App.WebViews;
using Malachi.Core.Api;
using Malachi.Core.Html;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Files;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.Web.WebView2.Core;
using Windows.Graphics;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.UI.WindowsAndMessaging;

namespace Malachi.App.Canary.Host;

/// <summary>Plays one <see cref="HostConfig"/>.</summary>
internal sealed class CanaryRunner
{
    private const int ViewHeight = 700;
    private const int MaxUri = 240;

    // How long the host waits for the browser to end once the views closed
    // (its NetLog is whole only then): a second or two on an idle machine,
    // 20 to 50 s while eight to ten CPU-burning processes kept every core
    // busy (measured; the 15 s before failed that way), and more than 60 s
    // once while eight of them ran beside the solution being rebuilt in a
    // loop (the 60 s before failed that way). App.Watchdog leaves room for
    // it.
    private static readonly TimeSpan BrowserExitTimeout = TimeSpan.FromSeconds(120);

    // A 7×5 PNG, the picture every malachi-cid: and registered cid: request
    // gets.
    private static readonly byte[] Png = Convert.FromBase64String(
        "iVBORw0KGgoAAAANSUhEUgAAAAcAAAAFCAIAAAAG+GGPAAAAEUlEQVR42mNQaHiAiRhoJAoALlM0gX31oMMAAAAASUVORK5CYII=");

    private readonly HostConfig config;
    private readonly List<HostEvent> events = [];
    private readonly Dictionary<string, CoreWebView2> cores = [];
    private readonly HashSet<nint> knownWindows = [];
    private readonly System.Diagnostics.Stopwatch clock = System.Diagnostics.Stopwatch.StartNew();
    private string phase = "init";
    private Window? window;
    private MessageWebView? viewer;
    private ComposeWebView? editor;
    private PreviewWebView? preview;
    private CardWebView? card;
    private CardWebView? boardCard;
    private WebView2? control;

    // The views of a protected run: the viewer, the editor, the previewer,
    // a card of the conversation view and a card of the board detail's
    // conversation (BoardMessageCardView's CardWebView).
    private const int ViewCount = 5;

    public CanaryRunner(HostConfig config)
    {
        this.config = config;
    }

    private int ViewWidth => config.ViewWidth;

    public async Task<HostResults> RunAsync()
    {
        var completed = false;
        string? version = null;
        var exited = false;
        try
        {
            // Before the browser starts: it and its processes inherit the job.
            if (ProcessPriority.PinNormal() is { } unpinned)
            {
                Add(HostEvent.Kinds.Log, "", null, detail: "priorities not pinned: " + unpinned);
            }
            Directory.CreateDirectory(config.Downloads);
            window = new Window { Title = "Malachi Mail canary" };
            var grid = new Grid();
            window.Content = grid;
            if (config.Mode == HostConfig.Modes.Control)
            {
                control = new WebView2 { Width = ViewWidth, Height = ViewHeight, HorizontalAlignment = HorizontalAlignment.Left };
                grid.Children.Add(control);
            }
            else
            {
                WebViewEnvironment.NetLogPath = config.NetLog;
                WebViewEnvironment.LoggerFactory = new HostLoggerFactory((category, message) =>
                    Add(HostEvent.Kinds.Log, category[(category.LastIndexOf('.') + 1)..], null, detail: message));
                WebViewEnvironment.Start(config.UserDataFolder);
                var registry = new CidRegistry();
                registry.RegisterFetcher("canary@x", _ => Task.FromResult(new InlineImage(Png, "image/png")));
                viewer = new MessageWebView { Parts = (_, _) => Task.FromResult(("image/png", Png)) };
                editor = new ComposeWebView(registry);
                preview = new PreviewWebView { FileTypes = new ShellFileTypes(), TypePolicy = new FileTypePolicy() };
                card = new CardWebView { Parts = (_, _) => Task.FromResult(("image/png", Png)) };
                boardCard = new CardWebView { Parts = (_, _) => Task.FromResult(("image/png", Png)) };
                var column = 0;
                foreach (var view in new FrameworkElement[] { viewer, editor, preview, card, boardCard })
                {
                    grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(ViewWidth) });
                    view.Width = ViewWidth;
                    view.Height = ViewHeight;
                    view.VerticalAlignment = VerticalAlignment.Top;
                    Grid.SetColumn(view, column++);
                    grid.Children.Add(view);
                }
                viewer.LinkActivated += (_, link) => Add(HostEvent.Kinds.Link, "viewer", link.Resolved, detail: link.Raw);
                viewer.HoveredLinkChanged += (_, _) => Add(HostEvent.Kinds.Hover, "viewer", null, detail: viewer.HoveredLink);
                card.OnLink = link => Add(HostEvent.Kinds.Link, "card", link.Resolved, detail: link.Raw);
                card.OnHover = text => Add(HostEvent.Kinds.Hover, "card", null, detail: text);
                card.OnSize = (css, viewport, _) => Add(HostEvent.Kinds.Size, "card", null,
                    detail: css.ToString("0.##", CultureInfo.InvariantCulture) + (viewport ? " viewport" : " -"));
                boardCard.OnLink = link => Add(HostEvent.Kinds.Link, "board-card", link.Resolved, detail: link.Raw);
                boardCard.OnHover = text => Add(HostEvent.Kinds.Hover, "board-card", null, detail: text);
                boardCard.OnSize = (css, viewport, _) => Add(HostEvent.Kinds.Size, "board-card", null,
                    detail: css.ToString("0.##", CultureInfo.InvariantCulture) + (viewport ? " viewport" : " -"));
                editor.Channel.Ready += (_, _) => Add(HostEvent.Kinds.Bridge, "editor", null, detail: "ready");
                editor.Channel.KeyPressed += (_, key) => Add(HostEvent.Kinds.Bridge, "editor", null, detail: "key " + key);
                editor.FilesDropped += (_, paths) => Add(HostEvent.Kinds.Dropped, "editor", null, detail: string.Join("|", paths));
                editor.Crashed += (_, _) =>
                {
                    Add(HostEvent.Kinds.Bridge, "editor", null, detail: "crashed");
                    // What the compose window does (editor.OnCrashed): the
                    // last text again.
                    editor.Load(editor.Html);
                };
                foreach (var (name, view) in new (string, HardenedWebView)[] { ("viewer", viewer), ("editor", editor), ("preview", preview), ("card", card), ("board-card", boardCard) })
                {
                    view.Unavailable += (_, _) => Add(HostEvent.Kinds.Unavailable, name, null);
                    view.CoreWebViewInitialized += (_, _) => Initialized(name, view);
                }
            }
            Place(window);
            await ReadyAsync();
            version = cores.Values.First().Environment.BrowserVersionString;
            RememberWindows();
            foreach (var step in config.Steps)
            {
                if (step.Phase is { } p)
                {
                    phase = p;
                }
                await StepAsync(step);
            }
            phase = "settle";
            await Task.Delay(1500);
            NewWindows();
            foreach (var file in Directory.EnumerateFileSystemEntries(config.Downloads))
            {
                Add(HostEvent.Kinds.DownloadedFile, "", Path.GetFileName(file));
            }
            completed = true;
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            Add(HostEvent.Kinds.Error, "", null, detail: e.ToString());
        }
        finally
        {
            exited = await CloseAsync();
        }
        lock (events)
        {
            return new HostResults { Completed = completed, BrowserVersion = version, BrowserExited = exited, Events = [.. events] };
        }
    }

    // Beyond the right edge of the virtual screen, shown without taking the
    // foreground (a canary run must not steal the user's focus), unless the
    // run is to be looked at.
    private void Place(Window w)
    {
        var width = config.Mode == HostConfig.Modes.Control ? ViewWidth + 40 : (ViewCount * ViewWidth) + 40;
        var height = ViewHeight + 60;
        if (config.Visible)
        {
            w.AppWindow.MoveAndResize(new RectInt32(20, 20, width, height));
            w.Activate();
            return;
        }
        var right = PInvoke.GetSystemMetrics(SYSTEM_METRICS_INDEX.SM_XVIRTUALSCREEN)
            + PInvoke.GetSystemMetrics(SYSTEM_METRICS_INDEX.SM_CXVIRTUALSCREEN);
        w.AppWindow.IsShownInSwitchers = false;
        w.AppWindow.MoveAndResize(new RectInt32(right + 200, 0, width, height));
        w.AppWindow.Show(false);
    }

    private async Task ReadyAsync()
    {
        if (control is { } c)
        {
            var arguments = "--proxy-server=127.0.0.1:1 --host-resolver-rules=\"MAP * ~NOTFOUND, EXCLUDE 127.0.0.1\""
                + " --log-net-log=\"" + config.NetLog + "\" --net-log-capture-mode=Everything";
            var environment = await CoreWebView2Environment.CreateWithOptionsAsync(
                "", config.UserDataFolder, new CoreWebView2EnvironmentOptions { AdditionalBrowserArguments = arguments });
            await c.EnsureCoreWebView2Async(environment);
            c.CoreWebView2.Settings.IsScriptEnabled = false;
            cores["control"] = c.CoreWebView2;
            Add(HostEvent.Kinds.Ready, "control", null);
            Observe("control", c.CoreWebView2);
            return;
        }
        var deadline = DateTime.UtcNow.AddSeconds(45);
        while (DateTime.UtcNow < deadline)
        {
            bool unavailable;
            lock (events)
            {
                unavailable = events.Any(e => e.Kind == HostEvent.Kinds.Unavailable);
            }
            if (cores.Count == ViewCount || unavailable)
            {
                return;
            }
            await Task.Delay(50);
        }
        throw new TimeoutException("the views did not become ready within 45 s");
    }

    // A view has a control: the first, or a new one after a failed process
    // (then its events are observed from the new one on).
    private void Initialized(string name, HardenedWebView view)
    {
        if (view.CoreWebView is not { } core)
        {
            return;
        }
        var again = cores.ContainsKey(name);
        cores[name] = core;
        Add(HostEvent.Kinds.Ready, name, null, detail: again ? "again" : null);
        Observe(name, core);
    }

    // A second handler after the view's own: it sees what the view decided.
    private void Observe(string name, CoreWebView2 core)
    {
        core.Profile.DefaultDownloadFolderPath = config.Downloads;
        core.NavigationStarting += (_, e) => Add(HostEvent.Kinds.Navigation, name, e.Uri, e.Cancel,
            detail: e.IsUserInitiated ? HostEvent.UserInitiated : HostEvent.NotUserInitiated);
        core.NavigationCompleted += (s, e) => Add(HostEvent.Kinds.Completed, name, s.Source, detail: e.IsSuccess + " " + e.WebErrorStatus);
        core.SourceChanged += (s, _) => Add(HostEvent.Kinds.Source, name, s.Source);
        core.FrameNavigationStarting += (_, e) => Add(HostEvent.Kinds.Frame, name, e.Uri, e.Cancel);
        core.NewWindowRequested += (_, e) => Add(HostEvent.Kinds.NewWindow, name, e.Uri, e.Handled && e.NewWindow is null);
        core.DownloadStarting += (_, e) => Add(HostEvent.Kinds.Download, name, e.DownloadOperation?.Uri, e.Cancel);
        core.LaunchingExternalUriScheme += (_, e) => Add(HostEvent.Kinds.ExternalScheme, name, e.Uri, e.Cancel);
        core.WebResourceRequested += (_, e) =>
        {
            string status;
            try
            {
                status = e.Response is { } r ? r.StatusCode.ToString(CultureInfo.InvariantCulture) : "deferred";
            }
            catch (Exception x) when (x is not OutOfMemoryException)
            {
                status = "deferred";
            }
            Add(HostEvent.Kinds.Request, name, e.Request.Uri, detail: status);
        };
        core.ProcessFailed += (_, e) => Add(HostEvent.Kinds.ProcessFailed, name, null, detail: e.ProcessFailedKind.ToString());
        if (control is null)
        {
            // The app's views filter WebResourceRequested themselves; the
            // control needs a filter for the observer to see anything.
            return;
        }
        core.AddWebResourceRequestedFilter("*", CoreWebView2WebResourceContext.All, CoreWebView2WebResourceRequestSourceKinds.All);
    }

    private async Task StepAsync(HostStep step)
    {
        var core = step.View == "wait" ? null : cores.GetValueOrDefault(step.View);
        switch (step.Op)
        {
            case "load":
                await LoadAsync(step, core!);
                break;
            case "show":
                await ShowAsync(step, core!);
                break;
            case "hover":
                await MouseAsync(core!, "mouseMoved", await PointAsync(step, core!), "none", 0);
                break;
            case "press":
                var pressed = await PointAsync(step, core!);
                await MouseAsync(core!, "mouseMoved", pressed, "none", 0);
                await MouseAsync(core!, "mousePressed", pressed, "left", 1);
                await Task.Delay(400);
                await MouseAsync(core!, "mouseMoved", (pressed.X + 400, pressed.Y + 300), "left", 0);
                await MouseAsync(core!, "mouseReleased", (pressed.X + 400, pressed.Y + 300), "left", 1);
                break;
            case "click":
            case "middle":
                var point = await PointAsync(step, core!);
                var button = step.Op == "middle" ? "middle" : "left";
                await MouseAsync(core!, "mouseMoved", point, "none", 0);
                await MouseAsync(core!, "mousePressed", point, button, 1);
                await MouseAsync(core!, "mouseReleased", point, button, 1);
                break;
            case "wait":
                break;
            case "focus":
                editor!.FocusStart();
                break;
            case "type":
                await core!.CallDevToolsProtocolMethodAsync("Input.insertText", JsonSerializer.Serialize(new TextInput(step.Html ?? ""), DevToolsJson.Default.TextInput));
                break;
            case "exec":
                editor!.Exec(step.Name ?? "", step.Html);
                break;
            case "flush":
                var flushed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
                editor!.Flush(flushed.SetResult);
                await Task.WhenAny(flushed.Task, Task.Delay(5000));
                Add(HostEvent.Kinds.Flushed, "editor", null, detail: flushed.Task.IsCompleted ? editor.Html : "timeout");
                break;
            case "drop":
                var target = await PointAsync(step, core!);
                foreach (var type in new[] { "dragEnter", "dragOver", "drop" })
                {
                    await core!.CallDevToolsProtocolMethodAsync("Input.dispatchDragEvent", JsonSerializer.Serialize(
                        new DragInput(type, target.X, target.Y, new DragData([], [step.Name ?? ""], 1)), DevToolsJson.Default.DragInput));
                }
                break;
            case "zoom":
                if (step.View == "card")
                {
                    card!.Zoom = (int)step.X;
                }
                else if (step.View == "board-card")
                {
                    boardCard!.Zoom = (int)step.X;
                }
                else
                {
                    viewer!.Zoom = (int)step.X;
                }
                break;
            case "titles":
                _ = BrowserWindows();
                break;
            case "probe":
                Add(HostEvent.Kinds.Probe, step.View, null, detail: await core!.ExecuteScriptAsync(step.Html ?? "null"));
                break;
            case "crash":
                Crash(core!);
                break;
            case "await":
                await AwaitAsync(step);
                return;
            case "hang":
                // A renderer busy for ever, and input for it to not answer.
                _ = HangAsync(core!);
                for (var i = 0; i < 4; i++)
                {
                    _ = MouseAsync(core!, "mouseMoved", (50 + i, 50), "none", 0);
                    await Task.Delay(250);
                }
                break;
            case "loadcrash":
                await LoadAndThenAsync(step, core!, () => Crash(core!));
                break;
            case "loadstop":
                // A document whose navigation fails with no process to blame.
                await LoadAndThenAsync(step, core!, core!.Stop);
                break;
            default:
                throw new InvalidOperationException("unknown step " + step.Op);
        }
        if (step.Ms > 0)
        {
            await Task.Delay(step.Ms);
        }
    }

    private async Task LoadAsync(HostStep step, CoreWebView2 core)
    {
        using var loaded = new LoadWatch(core);
        StartLoad(step, core);
        await loaded.WaitAsync();
    }

    private void StartLoad(HostStep step, CoreWebView2 core)
    {
        var html = step.Html ?? "";
        switch (step.View)
        {
            case "viewer":
                viewer!.Load(html);
                break;
            case "editor":
                editor!.Load(html);
                break;
            case "card":
                card!.Load(html, reload: false);
                break;
            case "board-card":
                boardCard!.Load(html, reload: false);
                break;
            default:
                core.NavigateToString(html);
                break;
        }
    }

    // A load, and then an action (the renderer's crash, a stop) as soon as
    // the document committed, while it still loads (a large one parses for
    // about a second). An action at a fixed time after the load could hit
    // no renderer at all, the navigation between two.
    private async Task LoadAndThenAsync(HostStep step, CoreWebView2 core, Action then)
    {
        var committed = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        void OnSource(CoreWebView2 sender, CoreWebView2SourceChangedEventArgs args) => committed.TrySetResult();
        core.SourceChanged += OnSource;
        try
        {
            StartLoad(step, core);
            if (await Task.WhenAny(committed.Task, Task.Delay(5000)) != committed.Task)
            {
                throw new TimeoutException("the document did not commit");
            }
        }
        finally
        {
            core.SourceChanged -= OnSource;
        }
        then();
    }

    // The DevTools protocol's Page.crash ends the page's renderer at once
    // (a stand-in for a body that kills it); it never answers.
    private void Crash(CoreWebView2 core) => _ = CrashAsync(core);

    // Until the view has recorded an event of the step's kind (Target) whose
    // detail contains Html, since the step began (with InPhase, in the
    // step's phase); at most Ms.
    private async Task AwaitAsync(HostStep step)
    {
        int start;
        lock (events)
        {
            start = events.Count;
        }
        var inPhase = phase;
        var deadline = clock.ElapsedMilliseconds + step.Ms;
        while (clock.ElapsedMilliseconds < deadline)
        {
            lock (events)
            {
                var candidates = step.InPhase ? events.Where(e => e.Phase == inPhase) : events.Skip(start);
                if (candidates.Any(e => e.Kind == step.Target && e.View == step.View
                    && (step.Html is null || (e.Detail?.Contains(step.Html, StringComparison.Ordinal) ?? false))))
                {
                    return;
                }
            }
            await Task.Delay(100);
        }
    }

    private async Task HangAsync(CoreWebView2 core)
    {
        try
        {
            await core.ExecuteScriptAsync("for (;;) {}");
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            Add(HostEvent.Kinds.Crash, "", null, detail: "hang " + e.GetType().Name);
        }
    }

    private async Task CrashAsync(CoreWebView2 core)
    {
        try
        {
            await core.CallDevToolsProtocolMethodAsync("Page.crash", "{}");
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            // The renderer went away before it could answer.
            Add(HostEvent.Kinds.Crash, "", null, detail: e.GetType().Name);
        }
    }

    private async Task ShowAsync(HostStep step, CoreWebView2 core)
    {
        var data = Convert.FromBase64String(step.Data ?? "");
        var attachment = new Attachment
        {
            PartId = "2",
            Filename = step.Name ?? "",
            ContentType = step.ContentType ?? "application/octet-stream",
            Size = data.LongLength,
            Inline = false,
        };
        using var loaded = new LoadWatch(core);
        preview!.Show(attachment, step.ContentType, data);
        Add(HostEvent.Kinds.Request, "preview", "shown:" + preview.Shown, detail: step.Name);
        await loaded.WaitAsync();
    }

    // The centre of the step's target (read through the prototype, as the
    // page may shadow document methods), or its point.
    private static async Task<(double X, double Y)> PointAsync(HostStep step, CoreWebView2 core)
    {
        if (step.Target is not { } target)
        {
            return (step.X, step.Y);
        }
        var json = await core.ExecuteScriptAsync(
            "(() => { const e = Document.prototype.getElementById.call(document, " + JsonSerializer.Serialize(target, DevToolsJson.Default.String)
            + "); if (!e) return null; const r = Element.prototype.getBoundingClientRect.call(e);"
            + " return [r.left + r.width / 2, r.top + r.height / 2]; })()");
        using var document = JsonDocument.Parse(json);
        if (document.RootElement.ValueKind != JsonValueKind.Array)
        {
            throw new InvalidOperationException("no element " + target);
        }
        return (document.RootElement[0].GetDouble(), document.RootElement[1].GetDouble());
    }

    private static async Task MouseAsync(CoreWebView2 core, string type, (double X, double Y) point, string button, int clicks)
    {
        var json = "{\"type\":\"" + type + "\",\"x\":" + point.X.ToString("0.##", CultureInfo.InvariantCulture)
            + ",\"y\":" + point.Y.ToString("0.##", CultureInfo.InvariantCulture)
            + ",\"button\":\"" + button + "\",\"clickCount\":" + clicks.ToString(CultureInfo.InvariantCulture) + "}";
        await core.CallDevToolsProtocolMethodAsync("Input.dispatchMouseEvent", json);
    }

    // Also appended to the progress file, which says where a run that never
    // wrote its results stopped. The views may log from other threads.
    private void Add(string kind, string view, string? uri, bool? stopped = null, string? detail = null)
    {
        if (uri is { Length: > MaxUri })
        {
            uri = uri[..MaxUri] + "…";
        }
        lock (events)
        {
            var e = new HostEvent { Kind = kind, View = view, Phase = phase, Uri = uri, Stopped = stopped, Detail = detail, Ms = clock.ElapsedMilliseconds };
            events.Add(e);
            try
            {
                File.AppendAllText(config.Results + ".progress",
                    e.Ms.ToString(CultureInfo.InvariantCulture) + " " + kind + " " + view + " " + e.Phase + " " + uri + " " + stopped + " " + detail + "\n");
            }
            catch (IOException)
            {
                // Progress is a convenience.
            }
        }
    }

    // The visible top-level windows of this process and the browser's.
    private HashSet<nint> Windows()
    {
        var pids = BrowserProcessIds();
        pids.Add((uint)Environment.ProcessId);
        var found = new HashSet<nint>();
        PInvoke.EnumWindows(
            (hwnd, _) =>
            {
                if (PInvoke.IsWindowVisible(hwnd) && pids.Contains(ProcessOf(hwnd)))
                {
                    found.Add(Handle(hwnd));
                }
                return true;
            },
            default);
        return found;
    }

    private static unsafe uint ProcessOf(HWND hwnd)
    {
        uint pid = 0;
        _ = PInvoke.GetWindowThreadProcessId(hwnd, &pid);
        return pid;
    }

    private static unsafe nint Handle(HWND hwnd) => (nint)hwnd.Value;

    // "class | title | pid" of a window.
    private static unsafe string Describe(nint handle)
    {
        var hwnd = new HWND((void*)handle);
        Span<char> buffer = stackalloc char[256];
        var classLength = PInvoke.GetClassName(hwnd, buffer);
        var className = buffer[..Math.Max(0, classLength)].ToString();
        var titleLength = PInvoke.GetWindowText(hwnd, buffer);
        var title = buffer[..Math.Max(0, titleLength)].ToString();
        return className + " | " + title + " | " + ProcessOf(hwnd).ToString(CultureInfo.InvariantCulture);
    }

    private void RememberWindows() => knownWindows.UnionWith(Windows());

    // Every visible window of the browser process is reported with its
    // title (a WebView2 in WinUI is drawn by a top-level Chrome_WidgetWin_1
    // over the control, titled with the document's title, which other
    // processes can read). A window that is new since the start is a leak
    // unless it is one of those, one per view.
    private void NewWindows()
    {
        var drawing = 0;
        foreach (var (hwnd, description, isBrowser) in BrowserWindows())
        {
            var parts = description.Split(" | ");
            if (knownWindows.Contains(hwnd))
            {
                continue;
            }
            if (isBrowser && parts[0] == "Chrome_WidgetWin_1" && ++drawing <= cores.Count)
            {
                continue;
            }
            Add(HostEvent.Kinds.Window, "", null, detail: description);
        }
    }

    // The visible windows of this process and the browser's, each browser
    // window's title recorded (what other processes can read).
    private List<(nint Handle, string Description, bool IsBrowser)> BrowserWindows()
    {
        var browsers = BrowserProcessIds();
        var found = new List<(nint, string, bool)>();
        foreach (var hwnd in Windows())
        {
            var description = Describe(hwnd);
            var isBrowser = browsers.Contains(uint.Parse(description.Split(" | ")[^1], CultureInfo.InvariantCulture));
            if (isBrowser)
            {
                Add(HostEvent.Kinds.Title, "", null, detail: description);
            }
            found.Add((hwnd, description, isBrowser));
        }
        return found;
    }

    // The browser processes of the views' live controls (a control closed
    // after a failed process answers no more).
    private HashSet<uint> BrowserProcessIds()
    {
        var pids = new HashSet<uint>();
        foreach (var core in cores.Values)
        {
            try
            {
                pids.Add(core.BrowserProcessId);
            }
            catch (COMException)
            {
            }
        }
        return pids;
    }

    // Closes the views and waits for the browser process to end, so that it
    // finishes its NetLog.
    private async Task<bool> CloseAsync()
    {
        CoreWebView2Environment? environment = null;
        foreach (var core in cores.Values)
        {
            try
            {
                environment = core.Environment;
                break;
            }
            catch (COMException)
            {
            }
        }
        var exited = new TaskCompletionSource(TaskCreationOptions.RunContinuationsAsynchronously);
        if (environment is not null)
        {
            environment.BrowserProcessExited += (_, _) => exited.TrySetResult();
        }
        try
        {
            viewer?.Close();
            editor?.Close();
            preview?.Close();
            card?.Close();
            boardCard?.Close();
            control?.Close();
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            Add(HostEvent.Kinds.Error, "", null, detail: "close: " + e.Message);
        }
        if (environment is null)
        {
            window?.Close();
            return false;
        }
        var waited = System.Diagnostics.Stopwatch.StartNew();
        var done = await Task.WhenAny(exited.Task, Task.Delay(BrowserExitTimeout)) == exited.Task;
        Add(HostEvent.Kinds.Log, "", null, detail: (done ? "the browser exited after " : "the browser still ran after ")
            + waited.ElapsedMilliseconds.ToString(CultureInfo.InvariantCulture) + " ms");
        window?.Close();
        return done;
    }
}
