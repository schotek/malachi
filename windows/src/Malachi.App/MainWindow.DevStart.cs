// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MainWindow/MainWindowController+DevStart.swift
// (applyDevelopmentStart, developmentStep, developmentSelectRow,
// developmentSelectFolder, developmentSelectLongestConversation,
// developmentShot, developmentResize) and of GTK's
// ui/internal/window/devstart.go (startDevelopmentScript, runDevStep): the
// steps of MALACHI_START that act on the window; Core's DevelopmentStart
// parses them. A DEVELOPMENT AID, not a feature (docs/screenshots.md §6):
// nothing here runs without the variable, nothing of it is translated,
// and it is for an isolated instance (MALACHI_DATA_DIR, MALACHI_SOCKET,
// MALACHI_SETTINGS_KEY, the samples) only, never over the real store. What
// it prints goes to stderr: "MALACHI_START step <n> <name>", then what the
// step found ("MALACHI_START row: selected '…'").
//
//   MALACHI_START="group=on;chat=off;row=DevConf;shot=C:\shots\inbox.png;quit"
//   MALACHI_START_SIZE=1440x900   (the window's visible frame, optional)
//   MALACHI_START_INTERVAL=4      (seconds between steps, optional, 3)
//
// Timing (macOS asyncAfter, GTK devBusy): the size is applied half a second
// after the window shows and the first step a second after it; the next
// step comes the interval after the previous one finished its work, which
// may wait: folder= for the folders and then for the list to show the
// folder, row= for a row that matches, board:<style>:select for the cases
// and every case's conversation, shot= for the window to be in front.
// Where a step finds nothing it says so and the script goes on.

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Boards;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Malachi.Core.Presentation;
using Microsoft.UI.Windowing;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using Windows.Graphics;

namespace Malachi.App;

/// <summary>The MALACHI_START development aid of the main window.</summary>
public sealed partial class MainWindow
{
    // How long a step waits for what it needs (the daemon's answer) before it gives up.
    private static readonly TimeSpan DevelopmentWait = TimeSpan.FromSeconds(60);

    /// <summary>The list the steps act on (macOS <c>developmentList</c>); null before the Integration.</summary>
    internal ListController? DevelopmentList => boardIntegration?.List;

    /// <summary>The mailbox the steps act on (macOS <c>developmentMailbox</c>); null before the Integration.</summary>
    internal MailboxController? DevelopmentMailbox => boardIntegration?.Mailbox;

    /// <summary>
    /// Runs <c>MALACHI_START</c>, when set, from the window's first show
    /// (called once by the app after its first activation). Without the
    /// variable nothing happens.
    /// </summary>
    internal void ApplyDevelopmentStart()
    {
        var script = Environment.GetEnvironmentVariable(DevelopmentStart.Variable);
        if (string.IsNullOrWhiteSpace(script))
        {
            return;
        }
        IReadOnlyList<DevelopmentStep> steps;
        try
        {
            steps = DevelopmentStart.Parse(script);
        }
        catch (FormatException e)
        {
            DevelopmentPrint("MALACHI_START: " + e.Message);
            return;
        }
        var interval = DevelopmentStart.ParseInterval(Environment.GetEnvironmentVariable(DevelopmentStart.IntervalVariable));
        var size = DevelopmentStart.ParseSize(Environment.GetEnvironmentVariable(DevelopmentStart.SizeVariable));
        _ = RunDevelopmentStepsAsync(steps, interval, size);
    }

    // The script, step by step on the UI thread (Task.Delay comes back to it
    // through the dispatcher's synchronization context).
    private async Task RunDevelopmentStepsAsync(IReadOnlyList<DevelopmentStep> steps, TimeSpan interval, (int Width, int Height)? size)
    {
        await Task.Delay(500);
        if (size is { } s)
        {
            DevelopmentResize(s.Width, s.Height);
        }
        await Task.Delay(500);
        for (var i = 0; i < steps.Count; i++)
        {
            var step = steps[i];
            DevelopmentPrint($"MALACHI_START step {i + 1} {step.Name}");
            try
            {
                await RunDevelopmentStepAsync(step);
            }
            catch (Exception e) when (e is not OutOfMemoryException)
            {
                DevelopmentPrint($"MALACHI_START step {i + 1} {step.Name}: {e}");
            }
            if (step.Kind == DevelopmentStepKind.Quit)
            {
                return;
            }
            await Task.Delay(interval);
        }
    }

    private async Task RunDevelopmentStepAsync(DevelopmentStep step)
    {
        switch (step.Kind)
        {
            case DevelopmentStepKind.Mail:
                SetMode(Board.Mode.Mail);
                break;
            case DevelopmentStepKind.Board:
                await DevelopmentBoardAsync(step);
                break;
            case DevelopmentStepKind.Calendar:
                SetMode(Board.Mode.Calendar);
                if (step.View is { } view && CalendarController is { } calendar && calendar.View != view)
                {
                    calendar.SetView(view);
                }
                break;
            case DevelopmentStepKind.Size:
                DevelopmentResize(step.Width, step.Height);
                break;
            case DevelopmentStepKind.Quit:
                DevelopmentQuit();
                break;
            case DevelopmentStepKind.Group:
                state.Settings.GroupByConversation = step.On;
                break;
            case DevelopmentStepKind.Chat:
                state.Settings.ConversationChatLayout = step.On;
                break;
            case DevelopmentStepKind.Folder:
                await DevelopmentSelectFolderAsync(step.Text);
                break;
            case DevelopmentStepKind.Row:
                await DevelopmentSelectRowAsync(step.Text);
                break;
            case DevelopmentStepKind.ComposeOpen:
                state.Hooks.ComposeNew?.Invoke();
                break;
            case DevelopmentStepKind.CrmSave:
                if (crm is { } c)
                {
                    c.Save();
                }
                else
                {
                    DevelopmentPrint("MALACHI_START crm-save: no CRM controller");
                }
                break;
            case DevelopmentStepKind.Shot:
                await DevelopmentShotAsync(step.Text);
                break;
        }
    }

    // `folder=<text>`: the first folder, accounts in the sidebar's order,
    // whose server name contains the text, as a click in the sidebar; then
    // the list is waited for, so that a `row=` next does not match a row of
    // the folder before.
    private async Task DevelopmentSelectFolderAsync(string text)
    {
        if (DevelopmentMailbox is not { } mailbox)
        {
            DevelopmentPrint("MALACHI_START folder: no mailbox");
            return;
        }
        Folder? folder = null;
        await DevelopmentWaitAsync(() => (folder = DevelopmentStart.FindFolder(mailbox.Model.MailSidebarAccounts, mailbox.Model.Folders, text)) is not null);
        if (folder is null)
        {
            var count = mailbox.Model.Folders.Values.Sum(f => f.Count);
            DevelopmentPrint($"MALACHI_START folder: no folder with '{text}' among {count}");
            return;
        }
        mailbox.SelectFolder(new FolderKey(folder.AccountId, folder.Id), fav: false);
        var shown = false;
        if (DevelopmentList is { } list)
        {
            shown = await DevelopmentWaitAsync(() => list.Rows.Count > 0 && list.Rows.All(r => r.Message.FolderId == folder.Id), TimeSpan.FromSeconds(20));
        }
        DevelopmentPrint($"MALACHI_START folder: selected '{folder.Name}' (listed {shown})");
    }

    // `row=<text>`: the first row of the list whose subject contains the
    // text, case-insensitively, as a click (the mark-as-read timer runs);
    // the row is scrolled into view.
    private async Task DevelopmentSelectRowAsync(string text)
    {
        if (DevelopmentList is not { } list)
        {
            DevelopmentPrint("MALACHI_START row: no list");
            return;
        }
        var index = -1;
        await DevelopmentWaitAsync(() => (index = DevelopmentStart.FindRow(list.Rows, text)) >= 0);
        if (index < 0)
        {
            DevelopmentPrint($"MALACHI_START row: no row with '{text}' among {list.Rows.Count}");
            return;
        }
        var row = list.Rows[index];
        list.Select(row.Key);
        ListPane.ScrollTo(row.Key);
        DevelopmentPrint($"MALACHI_START row: selected '{row.Message.Subject}'");
    }

    // `board`, `board:<style>`, `board:<style>:select`: the Board in that
    // style; with `:select` the case whose conversation has the most text
    // (the quotation thread of the sample set), once every listed case's
    // conversation has been loaded.
    private async Task DevelopmentBoardAsync(DevelopmentStep step)
    {
        SetMode(Board.Mode.Board);
        if (BoardView.Controller is not { } controller)
        {
            DevelopmentPrint("MALACHI_START board: no board");
            return;
        }
        if (step.Style is { } style && controller.State.Style != style)
        {
            controller.SetStyle(style);
        }
        if (!step.Select)
        {
            return;
        }
        await DevelopmentWaitAsync(() => controller.Phase != Board.Phase.Loading && controller.Source.Snapshot.Cases.Count > 0);
        var view = controller.View;
        List<BoardCaseId> ids = [.. view.Sections.SelectMany(s => s.Rows).Concat(view.Columns.SelectMany(c => c.Rows)).Select(r => r.Id).Distinct()];
        if (ids.Count == 0)
        {
            DevelopmentPrint($"MALACHI_START board: no cases (phase {controller.Phase})");
            return;
        }
        foreach (var id in ids)
        {
            controller.Source.LoadMessages(id);
        }
        var loaded = await DevelopmentWaitAsync(() => ids.All(id => controller.Source.Snapshot.FindCase(id) is { } c && (c.Messages is not null || c.MessagesFailed)));
        List<Board.Case> cases = [.. ids.Select(controller.Source.Snapshot.FindCase).OfType<Board.Case>()];
        var best = DevelopmentStart.RichestCase(cases);
        controller.Select(best);
        var subject = best is { } b ? controller.Source.Snapshot.FindCase(b)?.Subject : null;
        DevelopmentPrint($"MALACHI_START board: {cases.Count} cases, conversations loaded {loaded}, selected '{subject}'");
    }

    // `shot=<path>`: the compose window when one is open, else this window,
    // brought to the front and written as a PNG (WindowCapture).
    private async Task DevelopmentShotAsync(string path)
    {
        var target = state.Windows.Windows.FirstOrDefault(w => w.Kind == WindowKind.Compose && w.Window.AppWindow.IsVisible)?.Window ?? (Window)this;
        if (ReferenceEquals(target, this) && mode == Board.Mode.Mail && Reader is Reader.MessageView pane)
        {
            // The reading pane's HTML body: a cold WebView2 shows it seconds later.
            var shown = await DevelopmentWaitAsync(() => pane.DevelopmentBodyShown, TimeSpan.FromSeconds(20));
            if (!shown)
            {
                DevelopmentPrint("MALACHI_START shot: the reading pane's body is still loading");
            }
        }
        var how = WindowCapture.BringToFront(target);
        if (ReferenceEquals(target, this) && Root.XamlRoot is { } root && FocusManager.GetFocusedElement(root) is Control focused)
        {
            // The focus ring XAML draws around the focused control when the
            // last input it saw was a key is not part of the picture: the
            // control stops drawing the system focus visual (a pointer click
            // would not show one either), and is focused as a pointer would.
            focused.UseSystemFocusVisuals = false;
            focused.Focus(FocusState.Pointer);
        }
        // The activation's redraw, and a dialog's or a web view's last frame.
        await Task.Delay(800);
        try
        {
            var line = await WindowCapture.SavePngAsync(target, path, fromScreen: how != WindowCapture.Behind);
            DevelopmentPrint($"MALACHI_START shot: {line} of '{target.Title}' -> {path}; front {how}; {WindowCapture.Rectangles(target)}");
        }
        catch (Exception e) when (e is not OutOfMemoryException)
        {
            DevelopmentPrint($"MALACHI_START shot: {e.Message}");
        }
    }

    // `size=WxH` and MALACHI_START_SIZE: the window's visible frame (the
    // extended frame bounds, without the invisible resize borders) in
    // effective pixels, centred in the work area; the window's own rectangle
    // is that much larger.
    private void DevelopmentResize(int width, int height)
    {
        var scale = Scale();
        var size = new SizeInt32((int)Math.Round(width * scale), (int)Math.Round(height * scale));
        var area = DisplayArea.GetFromWindowId(AppWindow.Id, DisplayAreaFallback.Primary).WorkArea;
        AppWindow.MoveAndResize(new RectInt32(
            area.X + Math.Max(0, (area.Width - size.Width) / 2), area.Y + Math.Max(0, (area.Height - size.Height) / 2), size.Width, size.Height));
        // The borders Windows adds around the visible frame.
        var frame = WindowCapture.FrameSize(this);
        var dw = size.Width - frame.Width;
        var dh = size.Height - frame.Height;
        if (dw != 0 || dh != 0)
        {
            AppWindow.MoveAndResize(new RectInt32(
                area.X + Math.Max(0, (area.Width - size.Width - dw) / 2), area.Y + Math.Max(0, (area.Height - size.Height - dh) / 2),
                size.Width + dw, size.Height + dh));
            frame = WindowCapture.FrameSize(this);
        }
        DevelopmentPrint($"MALACHI_START resize {width}x{height}: frame {frame.Width}x{frame.Height} px, window {AppWindow.Size.Width}x{AppWindow.Size.Height} px at {AppWindow.Position.X},{AppWindow.Position.Y} (scale {scale})");
    }

    // `quit`: a dialog left open (crm-save) is closed first, then the
    // application quits as from the menu.
    private void DevelopmentQuit()
    {
        if (Root.XamlRoot is { } root)
        {
            foreach (var popup in VisualTreeHelper.GetOpenPopupsForXamlRoot(root))
            {
                if (popup.Child is ContentDialog dialog)
                {
                    dialog.Hide();
                }
            }
        }
        state.Quit();
    }

    // Polls `done` ten times a second until it is true or `limit` passes.
    private static async Task<bool> DevelopmentWaitAsync(Func<bool> done, TimeSpan? limit = null)
    {
        var deadline = DateTime.UtcNow + (limit ?? DevelopmentWait);
        while (!done())
        {
            if (DateTime.UtcNow >= deadline)
            {
                return false;
            }
            await Task.Delay(100);
        }
        return true;
    }

    // Development output, never shown in the UI (Windows-only strings).
    private static void DevelopmentPrint(string line) => Console.Error.WriteLine(line);
}
