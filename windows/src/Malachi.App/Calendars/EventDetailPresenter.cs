// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Calendar/EventDetailPopover.swift
// (Presenter: init, show, wire, ask, askScope, run, popoverDidShow,
// popoverDidClose); GTK: the event detail follows (docs/calendar.md §6.4).
//
// One open event detail flyout: its EventDetailController, its questions
// and its hand-offs (docs/calendar.md §5.5, M6). The questions are
// ContentDialogs on the main window through the application's alerts
// (Cancel the close button and the default; the series question's choices
// in its content, AlertService.Calendar.cs), and the flyout stays open
// while one is up: its Closing is cancelled meanwhile, as macOS turns the
// popover application-defined, and the detail takes the keyboard again
// after the answer. The hand-offs close the flyout first: Edit and Find
// Another Time open the editor (AppState.OpenEditEvent, with the scope the
// series question chose and Next Free Slot for Find Another Time), Show
// Invitation in Mail reveals the message in Mail as the board's Show in Mail
// does (Integration.ShowInvitationInMail), Join Video Call and the notes'
// links go through the reader's "Open This Link?" with no listed links (the
// destination alone), Ask about This Event to the window's assistant panel
// with the question naming the event in its field
// (MainWindow.AskAssistantAboutEvent). A removed event closes the flyout.
//
// Windows: the flyout is a WinUI Flyout placed to the right of its anchor
// when there is room for it, else to its left, else over the anchor's middle
// (a wide agenda row, the page itself), constrained to the window. The page
// knows it is open (CalendarPage.DetailOpen) and its Escape closes it
// (CalendarPage.CloseDetailRequested), both through
// MainWindow.EventDetailShown.

using System;
using System.Threading.Tasks;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Malachi.Core.Model;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Windows.Foundation;

namespace Malachi.App.Calendars;

/// <summary>One open event detail flyout.</summary>
internal sealed class EventDetailPresenter
{
    // The room the flyout needs beside its anchor: the content's 380 and the
    // presenter's border and margin.
    private const double FlyoutWidth = 380 + 24;

    private readonly AppState state;
    private readonly MainWindow owner;
    private readonly EventDetailController controller;
    private readonly EventDetailView view = new();
    private readonly Flyout flyout;

    // How many of the controller's questions are up (the flyout stays open).
    private int asking;
    private bool closed;

    /// <summary>The flyout of <paramref name="detail"/> in <paramref name="window"/>, not shown yet.</summary>
    public EventDetailPresenter(AppState state, MainWindow window, EventDetailController detail)
    {
        this.state = state;
        owner = window;
        controller = detail;
        var presenterStyle = new Style(typeof(FlyoutPresenter));
        presenterStyle.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(0)));
        presenterStyle.Setters.Add(new Setter(FrameworkElement.MaxWidthProperty, 420d));
        flyout = new Flyout
        {
            Content = view,
            ShouldConstrainToRootBounds = true,
            FlyoutPresenterStyle = presenterStyle,
        };
        flyout.Opened += (_, _) => view.TakeKeyboard();
        flyout.Closing += (_, e) =>
        {
            if (asking > 0)
            {
                // A question of the controller is up: the detail stays.
                e.Cancel = true;
            }
        };
        flyout.Closed += (_, _) => End();
        owner.Closed += OnOwnerClosed;
        Wire();
        view.Attach(controller, () => controller.Source.Snapshot, () => state.Assistant.PanelShown);
    }

    /// <summary>Whether the flyout shows.</summary>
    public bool IsOpen => !closed && flyout.IsOpen;

    /// <summary>Starts the controller and shows the flyout beside <paramref name="anchor"/>.</summary>
    public void Show(FrameworkElement anchor)
    {
        controller.Start();
        view.Update();
        owner.EventDetailShown(true);
        flyout.ShowAt(anchor, Options(anchor));
    }

    /// <summary>Closes the flyout (a hand-off, Escape, another detail).</summary>
    public void Close()
    {
        if (closed)
        {
            return;
        }
        asking = 0;
        flyout.Hide();
        // A flyout that never opened raises no Closed.
        if (!flyout.IsOpen)
        {
            End();
        }
    }

    // Right of the anchor when there is room, else left, else over its middle.
    private static FlyoutShowOptions Options(FrameworkElement anchor)
    {
        var options = new FlyoutShowOptions { Placement = FlyoutPlacementMode.Right, ShowMode = FlyoutShowMode.Standard };
        if (anchor.XamlRoot is not { } root)
        {
            return options;
        }
        Rect bounds;
        try
        {
            bounds = anchor.TransformToVisual(null).TransformBounds(new Rect(0, 0, anchor.ActualWidth, anchor.ActualHeight));
        }
        catch (ArgumentException)
        {
            return options;
        }
        if (bounds.Right + FlyoutWidth <= root.Size.Width)
        {
            return options;
        }
        if (bounds.Left - FlyoutWidth >= 0)
        {
            options.Placement = FlyoutPlacementMode.Left;
            return options;
        }
        options.Placement = FlyoutPlacementMode.Auto;
        options.Position = new Point(anchor.ActualWidth / 2, Math.Min(anchor.ActualHeight / 2, 48));
        return options;
    }

    private void Wire()
    {
        var c = controller;
        c.Changed += (_, _) => view.Update();
        c.AskQuestion = q => AskAsync(() => state.Alerts.CalendarQuestionAsync(owner, q, confirmDefault: false));
        c.AskScope = q => AskAsync(() => state.Alerts.CalendarScopeAsync(owner, q));
        c.CloseRequested += (_, _) => Close();
        c.EditRequested += (_, edit) =>
        {
            Close();
            if (edit.Event.EventId is { } id)
            {
                state.OpenEditEvent(owner, id, edit.Event.RecurrenceId ?? c.RecurrenceId, edit.Scope, edit.FindFreeSlot);
            }
        };
        c.ShowInvitationRequested += (_, message) =>
        {
            Close();
            state.Integration?.ShowInvitationInMail(message);
        };
        c.OpenLinkRequested += (_, url) =>
        {
            Close();
            if (state.Integration?.Reader is { } reader && !state.IsStopping)
            {
                _ = reader.Services.Links.OpenAsync(new ActivatedLink(url, url), [], owner);
            }
        };
        c.AskRequested += (_, e) =>
        {
            Close();
            owner.AskAssistantAboutEvent(e);
        };
        view.CloseRequested += (_, _) => Close();
    }

    // A question of the controller, the flyout kept open while it is up and
    // the keyboard back on the detail after it.
    private async Task<T> AskAsync<T>(Func<Task<T>> ask)
    {
        asking++;
        try
        {
            return await ask();
        }
        finally
        {
            asking = Math.Max(0, asking - 1);
            if (!closed && flyout.IsOpen)
            {
                view.TakeKeyboard();
            }
        }
    }

    private void OnOwnerClosed(object sender, WindowEventArgs args) => End();

    // popoverDidClose: the controller stops, the page and the application
    // forget the flyout.
    private void End()
    {
        if (closed)
        {
            return;
        }
        closed = true;
        controller.Stop();
        owner.Closed -= OnOwnerClosed;
        // A detail that replaced this one and is open already keeps the page's flag.
        if (EventDetailFlyout.IsCurrent(this))
        {
            owner.EventDetailShown(false);
        }
        EventDetailFlyout.Closed(this);
    }
}
