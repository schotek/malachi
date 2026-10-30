// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/MessageView/InvitationCardView.swift
// (show, render, fill, renderTargets, renderChangeMenu, the actions, the
// tile's show, the proposal popover's changed and proposeClicked); GTK: none
// yet (docs/calendar.md §5.7). The view of an InvitationController: the host
// (InvitationCardPresenter) calls Show with the controller, and Render again
// after each of its Changed; every click goes back to the controller.
// Everything shown is the controller's cleaned strings set as
// TextBlock.Text (a ComboBox item's and a menu item's text as well), never
// parsed.
//
// Windows differences: "Add to:" is a ComboBox; Decline is a SplitButton
// (its arrow's menu has Decline Without Reply, as macOS's NSComboButton);
// Change Response is a DropDownButton whose menu checks the current answer;
// Propose New Time… is a Flyout with a date and a time picker for each end
// (macOS's NSDatePicker does both in one), in the card's zone; the tile's
// band and the conflict strip take the theme's attention, caution, critical
// and success colours (visual states) for AppKit's system colours. A
// UserControl has no automation peer of its own: the card is a group with
// its AutomationId and the event's title as its name (GroupPeer), as macOS's
// card is an accessibility group.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Resources;
using Malachi.Core.Api;
using Malachi.Core.Calendars;
using Malachi.Core.Controllers;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Automation.Peers;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Reader;

/// <summary>The invitation card above a message's header.</summary>
public sealed partial class InvitationCardView : UserControl
{
    private InvitationController? controller;

    // The targets the ComboBox shows, in its order; set while it is filled,
    // so that its SelectionChanged is not taken for the user's.
    private List<CalendarId> targetIds = [];
    private bool filling;

    /// <summary>A collapsed card; <see cref="Show"/> gives it a controller.</summary>
    public InvitationCardView()
    {
        InitializeComponent();
        JoinButton.Content = Cal.JoinVideoCall;
        AddToLabel.Text = Cal.AddTo;
        AutomationProperties.SetName(TargetBox, Cal.AddTo);
        AcceptButton.Content = Cal.ResponseAction(new EventResponse(EventResponse.Accepted));
        TentativeButton.Content = Cal.ResponseAction(new EventResponse(EventResponse.Tentative));
        DeclineButton.Content = Cal.ResponseAction(new EventResponse(EventResponse.Declined));
        AutomationProperties.SetName(DeclineButton, Cal.ResponseAction(new EventResponse(EventResponse.Declined)));
        DeclineWithoutReplyItem.Text = Cal.DeclineWithoutReply;
        ChangeButton.Content = Cal.ChangeResponse;
        ShowButton.Content = Cal.ShowInCalendar;
        StateShowButton.Content = Cal.ShowInCalendar;
        RemoveButton.Content = Cal.RemoveFromCalendar;
        ProposeButton.Content = Cal.ProposeNewTime;
        ProposeHeading.Text = Cal.ProposeNewTime;
        StartsLabel.Text = Cal.FieldLabel(Cal.Field.Start);
        EndsLabel.Text = Cal.FieldLabel(Cal.Field.End);
        AutomationProperties.SetName(StartDate, Cal.FieldLabel(Cal.Field.Start));
        AutomationProperties.SetName(StartTime, Cal.FieldLabel(Cal.Field.Start));
        AutomationProperties.SetName(EndDate, Cal.FieldLabel(Cal.Field.End));
        AutomationProperties.SetName(EndTime, Cal.FieldLabel(Cal.Field.End));
        ProposalCancel.Content = Cal.Cancel;
        ProposalSend.Content = Cal.Propose;
        CheckIcon.Glyph = Icons.Glyph("object-select");
    }

    /// <summary>The controller the card shows; null while collapsed.</summary>
    public InvitationController? Controller => controller;

    /// <inheritdoc/>
    protected override AutomationPeer OnCreateAutomationPeer() => new GroupPeer(this);

    /// <summary>Shows the card of <paramref name="c"/> (null: collapsed); call <see cref="Render"/> after each of its changes.</summary>
    public void Show(InvitationController? c)
    {
        if (!ReferenceEquals(c, controller))
        {
            ProposeFlyout.Hide();
            targetIds = [];
        }
        controller = c;
        Render();
    }

    /// <summary>Draws what the controller says now.</summary>
    public void Render()
    {
        if (controller is not { } c || c.Mode == Cal.CardMode.None)
        {
            Visibility = Visibility.Collapsed;
            return;
        }
        Visibility = Visibility.Visible;
        var mode = c.Mode;
        var card = c.Card;
        var full = mode == Cal.CardMode.Full;
        var cancelled = mode == Cal.CardMode.Cancelled;
        var reply = mode == Cal.CardMode.Reply;

        AutomationProperties.SetName(this, c.Title);
        Pill.Kind = c.Pill;
        // A reply's card is its one line beside the pill.
        var title = reply ? c.ReplyCard : c.Title;
        TitleLabel.Style = (Style)Application.Current.Resources[reply ? "BodyLabelStyle" : "HeadingLabelStyle"];
        Set(TitleLabel, title);
        TitleLabel.TextDecorations = cancelled ? Windows.UI.Text.TextDecorations.Strikethrough : Windows.UI.Text.TextDecorations.None;

        var tile = reply ? null : c.DateTile;
        ShowTile(tile, c.Pill);

        Set(ClockLabel, reply ? "" : Cal.CardClockLine(card, c.Env));
        Fill(PreviousStack, full ? Cal.PreviousLines(card, c.Env) : [], null);
        Set(LocationLabel, full ? c.Location : "");
        JoinButton.Visibility = Visible(full && card.VideoCall?.Url is { Length: > 0 });
        ToolTipService.SetToolTip(JoinButton, c.VideoHost.Length > 0 ? c.VideoHost : null);
        LocationRow.Visibility = Visible(LocationLabel.Visibility == Visibility.Visible || JoinButton.Visibility == Visibility.Visible);
        Set(PeopleLabel, full ? c.PeopleLine : "");

        var live = full && c.ShowsResponses;
        var conflicts = live || (full && c.ShowsStateStrip);
        ConflictRow.Visibility = Visible(conflicts);
        if (conflicts)
        {
            var has = card.Conflicts.Count > 0;
            ConflictLabel.Text = c.ConflictLine;
            ConflictIcon.Glyph = Icons.Glyph(has ? "dialog-warning" : "object-select");
            VisualStateManager.GoToState(this, has ? "Conflict" : "NoConflict", false);
        }
        DayStrip.Show(conflicts ? Cal.DayStripOf(card, c.Env) : null);

        // A line for an invitation that cannot be answered (outdated, or not
        // from here); a cancellation's comes as the state line too.
        Set(StateLabel, reply ? "" : c.StateLine);
        // "You organise this event." with Show in Calendar on its row.
        StateShowButton.Visibility = Visible(StateLabel.Visibility == Visibility.Visible && !c.ShowsStateStrip && Cal.ShowsInCalendar(card));
        StateShowButton.IsEnabled = !c.Busy;
        StateRow.Visibility = StateLabel.Visibility;

        ActionsRow.Visibility = Visible(live);
        if (live)
        {
            RenderTargets(c);
        }
        AcceptButton.IsEnabled = !c.Busy;
        TentativeButton.IsEnabled = !c.Busy;
        DeclineButton.IsEnabled = !c.Busy;
        Spinner.IsActive = c.Busy;
        Spinner.Visibility = Visible(c.Busy && live);

        StripRow.Visibility = Visible(full && c.ShowsStateStrip);
        if (StripRow.Visibility == Visibility.Visible)
        {
            StripLabel.Text = c.StateStrip;
            RenderChangeMenu(c);
            ChangeButton.Visibility = Visible(card.CanRespond);
            ChangeButton.IsEnabled = !c.Busy;
            ShowButton.Visibility = Visible(Cal.ShowsInCalendar(card));
        }

        CancelRow.Visibility = Visible(cancelled && card.EventId is not null && !c.CancelApplied);
        RemoveButton.IsEnabled = !c.Busy;

        var note = live ? c.ReplyNote : [];
        Fill(NotesStack, note, card.OrganizerMismatch == true && note.Count > 0 ? note[^1] : null);
        ProposeRow.Visibility = Visible(full && c.ShowsProposeNewTime);
        ProposeButton.IsEnabled = !c.Busy;

        Set(ErrorLabel, c.Error);
    }

    // The tile at the start (none for a reply's one line): the column takes
    // the whole width without it.
    private void ShowTile(Cal.DateTile? tile, Cal.InvitationPill pill)
    {
        Tile.Visibility = Visible(tile is not null);
        Grid.SetColumn(Column, tile is null ? 0 : 1);
        Grid.SetColumnSpan(Column, tile is null ? 2 : 1);
        if (tile is null)
        {
            return;
        }
        TileWeekday.Text = tile.Weekday;
        TileDay.Text = tile.Day;
        TileMonth.Text = tile.Month;
        VisualStateManager.GoToState(this, pill switch
        {
            Cal.InvitationPill.Update => "BandUpdate",
            Cal.InvitationPill.Cancelled => "BandCancelled",
            _ => "BandInvitation",
        }, false);
    }

    // renderTargets: the ComboBox of the targets, or the label of the fixed
    // one, or nothing without targets.
    private void RenderTargets(InvitationController c)
    {
        var choices = c.Targets;
        AddToLabel.Visibility = Visible(choices.Count > 0);
        if (c.TargetIsPopup)
        {
            TargetBox.Visibility = Visibility.Visible;
            TargetLabel.Visibility = Visibility.Collapsed;
            var ids = choices.Select(t => t.Id).ToList();
            filling = true;
            try
            {
                if (!ids.SequenceEqual(targetIds) || TargetBox.Items.Count != ids.Count)
                {
                    TargetBox.Items.Clear();
                    foreach (var t in choices)
                    {
                        // The title as a TextBlock's Text: no parsing.
                        TargetBox.Items.Add(new ComboBoxItem
                        {
                            Content = new TextBlock { Text = t.Title, TextTrimming = TextTrimming.CharacterEllipsis, MaxLines = 1 },
                        });
                    }
                    targetIds = ids;
                }
                TargetBox.SelectedIndex = c.Target is { } target ? ids.IndexOf(target) : -1;
            }
            finally
            {
                filling = false;
            }
            TargetBox.IsEnabled = !c.Busy;
        }
        else
        {
            TargetBox.Visibility = Visibility.Collapsed;
            var t = choices.FirstOrDefault(x => x.Id == c.Target) ?? (choices.Count > 0 ? choices[0] : null);
            TargetLabel.Text = t?.Title ?? "";
            TargetLabel.Visibility = Visible(t is not null);
        }
    }

    // renderChangeMenu: the answers with the current one checked, then
    // Decline Without Reply.
    private void RenderChangeMenu(InvitationController c)
    {
        ChangeMenu.Items.Clear();
        foreach (var r in Cal.ResponseChoices)
        {
            var response = r;
            var item = new ToggleMenuFlyoutItem
            {
                Text = Cal.ResponseAction(r),
                IsChecked = c.Card.Response == r,
            };
            item.Click += (_, _) => controller?.Respond(response);
            ChangeMenu.Items.Add(item);
        }
        ChangeMenu.Items.Add(new MenuFlyoutSeparator());
        var without = new MenuFlyoutItem { Text = Cal.DeclineWithoutReply };
        without.Click += (_, _) => controller?.Respond(new EventResponse(EventResponse.Declined), withoutReply: true);
        ChangeMenu.Items.Add(without);
    }

    // fill: one grey caption line each (collapsed for none); the organiser
    // warning's line in the caution colour.
    private static void Fill(StackPanel stack, IReadOnlyList<string> lines, string? warning)
    {
        var have = stack.Children.OfType<TextBlock>().Select(t => t.Text).ToList();
        if (!have.SequenceEqual(lines))
        {
            stack.Children.Clear();
            foreach (var line in lines)
            {
                var label = new TextBlock
                {
                    Text = line,
                    TextWrapping = TextWrapping.Wrap,
                    IsTextSelectionEnabled = true,
                    Style = (Style)Application.Current.Resources[
                        warning is not null && line == warning ? "CautionCaptionLabelStyle" : "DimCaptionLabelStyle"],
                };
                stack.Children.Add(label);
            }
        }
        stack.Visibility = Visible(lines.Count > 0);
    }

    private static void Set(TextBlock label, string text)
    {
        label.Text = text;
        label.Visibility = Visible(text.Length > 0);
    }

    private static Visibility Visible(bool on) => on ? Visibility.Visible : Visibility.Collapsed;

    // Actions

    private void OnJoinClick(object sender, RoutedEventArgs e) => controller?.JoinVideoCall();

    private void OnTargetChanged(object sender, SelectionChangedEventArgs e)
    {
        var i = TargetBox.SelectedIndex;
        if (filling || i < 0 || i >= targetIds.Count)
        {
            return;
        }
        controller?.SetTarget(targetIds[i]);
    }

    private void OnAcceptClick(object sender, RoutedEventArgs e) => controller?.Respond(new EventResponse(EventResponse.Accepted));

    private void OnTentativeClick(object sender, RoutedEventArgs e) => controller?.Respond(new EventResponse(EventResponse.Tentative));

    private void OnDeclineClick(SplitButton sender, SplitButtonClickEventArgs args) => controller?.Respond(new EventResponse(EventResponse.Declined));

    private void OnDeclineWithoutReplyClick(object sender, RoutedEventArgs e) =>
        controller?.Respond(new EventResponse(EventResponse.Declined), withoutReply: true);

    private void OnShowClick(object sender, RoutedEventArgs e) => controller?.ShowInCalendar();

    private void OnRemoveClick(object sender, RoutedEventArgs e) => controller?.ApplyCancel();

    // Propose New Time…: the pickers start at the invitation's time in the
    // card's zone; the answer goes as Tentative with the proposal.
    private void OnProposeOpening(object? sender, object e)
    {
        if (controller is not { } c || c.Card.Start is not { } start || c.Card.End is not { } end)
        {
            ProposeFlyout.Hide();
            return;
        }
        var zone = c.Env.Zone;
        var s = TimeZoneInfo.ConvertTime(start, zone);
        var en = TimeZoneInfo.ConvertTime(end, zone);
        StartDate.Date = new DateTimeOffset(s.Date, TimeSpan.Zero);
        StartTime.Time = s.TimeOfDay;
        EndDate.Date = new DateTimeOffset(en.Date, TimeSpan.Zero);
        EndTime.Time = en.TimeOfDay;
        UpdateProposal();
    }

    private void OnProposalChanged(CalendarDatePicker sender, CalendarDatePickerDateChangedEventArgs args) => UpdateProposal();

    private void OnProposalTimeChanged(object? sender, TimePickerValueChangedEventArgs e) => UpdateProposal();

    private void UpdateProposal() =>
        ProposalSend.IsEnabled = Proposal() is { } p && p.End > p.Start;

    // The pickers' start and end in the card's zone; null while a date is empty.
    private (DateTimeOffset Start, DateTimeOffset End)? Proposal()
    {
        if (controller is not { } c || StartDate.Date is not { } sd || EndDate.Date is not { } ed)
        {
            return null;
        }
        var zone = c.Env.Zone;
        return (At(sd, StartTime.Time, zone), At(ed, EndTime.Time, zone));
    }

    // A wall-clock date and time of zone as an instant.
    private static DateTimeOffset At(DateTimeOffset date, TimeSpan time, TimeZoneInfo zone)
    {
        var local = DateTime.SpecifyKind(date.Date + time, DateTimeKind.Unspecified);
        return new DateTimeOffset(local, zone.GetUtcOffset(local));
    }

    private void OnProposalCancelClick(object sender, RoutedEventArgs e) => ProposeFlyout.Hide();

    private void OnProposalSendClick(object sender, RoutedEventArgs e)
    {
        if (Proposal() is not { } p || p.End <= p.Start)
        {
            return;
        }
        ProposeFlyout.Hide();
        controller?.ProposeNewTime(p.Start, p.End);
    }

    // The card as one group of the automation tree.
    private sealed partial class GroupPeer(FrameworkElement owner) : FrameworkElementAutomationPeer(owner)
    {
        protected override AutomationControlType GetAutomationControlTypeCore() => AutomationControlType.Group;

        protected override string GetClassNameCore() => nameof(InvitationCardView);

        protected override bool IsControlElementCore() => true;
    }
}
