// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AboutYouGroup.swift; GTK:
// ui/internal/window/preferences_profile.go (about_you_group,
// about_you_pending_group). The group About You at the top of Preferences →
// AI (docs/plans/assistant-profile.md §5): I Write As (the four genders,
// written at once), the user's text with its counter, a note for each mail
// and Jira account (an Expander whose header previews the note or says No
// Note), the switch Send the Profile to Apps over MCP, and under the group
// the assistant's pending proposals with Save, Edit… and Dismiss. A text is
// written one second after the last change, when it loses the keyboard and
// when the page closes, and only when it differs from what the daemon
// stored. The application's ProfileStore holds the profile; a change of it
// never overwrites a field that has the keyboard or unsaved typing. The
// group is disabled until the store has read the profile and collapsed for
// a daemon without one. Everything from the daemon is TextBlock.Text or
// TextBox.Text, never markup.
//
// Windows differences: the counter is under the field, MaxLength refuses
// typing past the limit; a refused write puts the stored value back without
// a beep (no msgid for an error, as macOS and GTK).

using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;
using CommunityToolkit.WinUI.Controls;
using Malachi.App.Assistants;
using Malachi.Core.Api;
using Malachi.Core.Assistants;
using Malachi.Core.Transport;
using Microsoft.UI.Dispatching;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Preferences;

/// <summary>About You and the assistant's proposals.</summary>
public sealed partial class AiPage
{
    /// <summary>How long a field waits after the last change before it is written.</summary>
    private static readonly TimeSpan ProfileQuiet = TimeSpan.FromSeconds(1);

    /// <summary>How many characters of a note the account's header shows.</summary>
    private const int NotePreviewLength = 40;

    private readonly ComboBox genderBox = new() { MinWidth = 200 };
    private readonly ToggleSwitch shareSwitch = new();
    private readonly StackPanel accountRows = new() { Spacing = 4 };
    private readonly Dictionary<string, ProfileField> noteFields = [];
    private ProfileField? textField;
    private IReadOnlyList<Account> profileAccounts = [];
    private bool profileSyncing;
    private bool profileClosed;

    // A text written through the store: the box, its counter, the debounce.
    private sealed class ProfileField
    {
        public required TextBox Box { get; init; }

        public required TextBlock Counter { get; init; }

        public required DispatcherQueueTimer Timer { get; init; }

        public required Func<string> Stored { get; init; }

        public required Func<string, Task<bool>> Write { get; init; }

        public required Func<int> Limit { get; init; }

        /// <summary>A write of this field is on its way.</summary>
        public bool Saving { get; set; }

        /// <summary>An account's header preview, following the box.</summary>
        public TextBlock? Preview { get; set; }

        public bool Dirty => Box.Text != Stored();

        /// <summary>The field keeps its text: it has the keyboard, a write is waiting or on its way.</summary>
        public bool Busy => Box.FocusState != FocusState.Unfocused || Timer.IsRunning || Saving;
    }

    private void InitializeProfileGroup()
    {
        var t = Assistant.ProfileTexts();
        var store = state.Profile;

        var heading = new TextBlock { Text = t.Group, Style = PageStyle("SettingsGroupHeaderStyle") };
        AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level2);
        var description = new TextBlock { Text = t.GroupDescription, Style = PageStyle("SettingsGroupDescriptionStyle") };
        AboutYouGroup.Children.Add(heading);
        AboutYouGroup.Children.Add(description);

        foreach (var g in ProfileGender.All)
        {
            genderBox.Items.Add(new ComboBoxItem { Content = t.GenderLabel(g) });
        }
        AutomationProperties.SetAutomationId(genderBox, "ProfileGender");
        AutomationProperties.SetName(genderBox, t.GenderRow);
        genderBox.SelectionChanged += (_, _) => _ = OnGenderChangedAsync();
        AboutYouGroup.Children.Add(new SettingsCard { Header = t.GenderRow, Content = genderBox });

        textField = MakeField(
            t.Group,
            "ProfileText",
            t.TextPlaceholder,
            multiLine: true,
            stored: () => store.Stored.Text,
            limit: () => store.Limits.Text,
            write: text => WriteProfileAsync(new AssistantProfileSetParams { Text = text }));
        var textCard = new StackPanel { Spacing = 4 };
        textCard.Children.Add(textField.Box);
        textCard.Children.Add(textField.Counter);
        AboutYouGroup.Children.Add(textCard);

        AboutYouGroup.Children.Add(accountRows);

        AutomationProperties.SetAutomationId(shareSwitch, "ProfileShareWithMcp");
        AutomationProperties.SetName(shareSwitch, t.ShareRow);
        shareSwitch.Toggled += (_, _) => _ = OnShareToggledAsync();
        AboutYouGroup.Children.Add(new SettingsCard { Header = t.ShareRow, Description = t.ShareSubtitle, Content = shareSwitch });

        store.Changed += OnProfileChanged;
        ApplyProfile();
    }

    // The page came up: the accounts are listed again, the profile read.
    private void RefreshProfileGroup()
    {
        state.Profile.Refresh();
        _ = LoadProfileAccountsAsync();
    }

    // The window closed: what was typed is written, the store let go.
    private void CloseProfileGroup()
    {
        if (profileClosed)
        {
            return;
        }
        profileClosed = true;
        state.Profile.Changed -= OnProfileChanged;
        foreach (var f in AllFields())
        {
            f.Timer.Stop();
            if (f.Dirty)
            {
                _ = f.Write(f.Box.Text);
            }
        }
    }

    private IEnumerable<ProfileField> AllFields() =>
        textField is { } t ? noteFields.Values.Prepend(t) : noteFields.Values;

    private async Task LoadProfileAccountsAsync()
    {
        try
        {
            var result = await state.Client.CallAsync(API.AccountList, new EmptyParams());
            profileAccounts = result.Accounts;
        }
        catch (Exception e) when (e is RpcException or RpcClientException or TimeoutException or OperationCanceledException)
        {
            // The rows stay as they were; the next visit asks again.
            return;
        }
        if (!profileClosed)
        {
            BuildAccountRows();
        }
    }

    /// <summary>Whether the account can have a note: an assistant writes in the user's name in mail and Jira.</summary>
    internal static bool HasProfileNote(Account account) =>
        account.Config.ProtocolKind.Value is AccountKind.Imap or AccountKind.Graph or AccountKind.Jira;

    /// <summary>The header's preview of a note: its text on one line, at most <see cref="NotePreviewLength"/> characters; <paramref name="none"/> for an empty one.</summary>
    internal static string NotePreview(string note, string none)
    {
        var line = string.Join(' ', note.Split(['\r', '\n', '\t', ' '], StringSplitOptions.RemoveEmptyEntries));
        if (line.Length == 0)
        {
            return none;
        }
        var runes = line.EnumerateRunes().ToArray();
        return runes.Length <= NotePreviewLength ? line : string.Concat(runes.Take(NotePreviewLength).Select(r => r.ToString())) + "…";
    }

    private void BuildAccountRows()
    {
        var t = Assistant.ProfileTexts();
        var store = state.Profile;
        var keep = new Dictionary<string, ProfileField>();
        accountRows.Children.Clear();
        foreach (var a in profileAccounts.Where(HasProfileNote))
        {
            var id = a.Id.Value;
            var name = a.Config.Name.Length > 0 ? a.Config.Name : a.Config.Email;
            // A field being edited keeps its text through the rebuild.
            var field = noteFields.TryGetValue(id, out var old) ? old : MakeField(
                name,
                "ProfileNote",
                t.NotePlaceholder,
                multiLine: false,
                stored: () => StoredNote(id),
                limit: () => store.Limits.AccountNote,
                write: text => WriteProfileAsync(new AssistantProfileSetParams { Accounts = [new ProfileAccountNote(a.Id, text)] }));
            keep[id] = field;
            if (field.Box.Parent is Panel parent)
            {
                parent.Children.Remove(field.Box);
                parent.Children.Remove(field.Counter);
            }
            var preview = new TextBlock
            {
                Text = NotePreview(field.Box.Text, t.NoNote),
                Style = PageStyle("DimCaptionLabelStyle"),
                TextTrimming = TextTrimming.CharacterEllipsis,
            };
            field.Preview = preview;
            var header = new Grid { ColumnSpacing = 12 };
            header.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            header.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            var title = new TextBlock { Text = name, TextTrimming = TextTrimming.CharacterEllipsis, VerticalAlignment = VerticalAlignment.Center };
            preview.VerticalAlignment = VerticalAlignment.Center;
            Grid.SetColumn(preview, 1);
            header.Children.Add(title);
            header.Children.Add(preview);
            var body = new StackPanel { Spacing = 4 };
            body.Children.Add(field.Box);
            body.Children.Add(field.Counter);
            var expander = new Expander
            {
                Header = header,
                Content = body,
                HorizontalAlignment = HorizontalAlignment.Stretch,
                HorizontalContentAlignment = HorizontalAlignment.Stretch,
            };
            AutomationProperties.SetAutomationId(expander, "ProfileAccount_" + id);
            AutomationProperties.SetName(expander, name);
            accountRows.Children.Add(expander);
        }
        foreach (var (id, f) in noteFields)
        {
            if (!keep.ContainsKey(id))
            {
                f.Timer.Stop();
            }
        }
        noteFields.Clear();
        foreach (var (id, f) in keep)
        {
            noteFields[id] = f;
        }
        // The new fields: the store's notes, enabled once it has read them.
        ApplyProfile();
    }

    private string StoredNote(string accountId) =>
        state.Profile.Stored.Accounts.FirstOrDefault(n => n.AccountId.Value == accountId)?.Note ?? "";

    private ProfileField MakeField(string name, string automationId, string placeholder, bool multiLine, Func<string> stored, Func<int> limit, Func<string, Task<bool>> write)
    {
        var box = new TextBox
        {
            PlaceholderText = placeholder,
            AcceptsReturn = multiLine,
            TextWrapping = TextWrapping.Wrap,
            MinHeight = multiLine ? 120 : 0,
            Text = stored(),
        };
        AutomationProperties.SetAutomationId(box, automationId);
        AutomationProperties.SetName(box, name);
        var counter = new TextBlock { Style = PageStyle("DimCaptionLabelStyle"), HorizontalAlignment = HorizontalAlignment.Right };
        var timer = DispatcherQueue.CreateTimer();
        timer.Interval = ProfileQuiet;
        timer.IsRepeating = false;
        var field = new ProfileField { Box = box, Counter = counter, Timer = timer, Stored = stored, Write = write, Limit = limit };
        timer.Tick += (t, _) =>
        {
            t.Stop();
            _ = SaveFieldAsync(field);
        };
        box.TextChanged += (_, _) =>
        {
            UpdateCounter(field);
            if (field.Preview is { } preview)
            {
                preview.Text = NotePreview(box.Text, Assistant.ProfileTexts().NoNote);
            }
            if (!profileSyncing)
            {
                timer.Stop();
                timer.Start();
            }
        };
        box.LostFocus += (_, _) =>
        {
            timer.Stop();
            _ = SaveFieldAsync(field);
        };
        UpdateCounter(field);
        return field;
    }

    private static void UpdateCounter(ProfileField f)
    {
        var limit = f.Limit();
        f.Box.MaxLength = limit > 0 ? limit : 0;
        f.Counter.Text = Assistant.ProfileCounter(f.Box.Text.EnumerateRunes().Count(), limit);
    }

    private async Task SaveFieldAsync(ProfileField f)
    {
        if (!f.Dirty || !state.Profile.Loaded || f.Saving)
        {
            return;
        }
        f.Saving = true;
        var ok = await f.Write(f.Box.Text);
        f.Saving = false;
        if (!ok && !f.Busy)
        {
            // Refused: the stored value again.
            SetFieldText(f, f.Stored());
        }
    }

    private async Task<bool> WriteProfileAsync(AssistantProfileSetParams p)
    {
        var error = await state.Profile.SetAsync(p);
        return error is null;
    }

    private async Task OnGenderChangedAsync()
    {
        if (profileSyncing || genderBox.SelectedIndex < 0 || genderBox.SelectedIndex >= ProfileGender.All.Count)
        {
            return;
        }
        var g = ProfileGender.All[genderBox.SelectedIndex];
        if (g == state.Profile.Stored.Gender)
        {
            return;
        }
        if (!await WriteProfileAsync(new AssistantProfileSetParams { Gender = g }))
        {
            ApplyProfile();
        }
    }

    private async Task OnShareToggledAsync()
    {
        if (profileSyncing || shareSwitch.IsOn == state.Profile.Stored.ShareWithMcp)
        {
            return;
        }
        if (!await WriteProfileAsync(new AssistantProfileSetParams { ShareWithMcp = shareSwitch.IsOn }))
        {
            ApplyProfile();
        }
    }

    private void OnProfileChanged(object? sender, EventArgs e) => ApplyProfile();

    // The store's profile in the controls, but never into a field that has
    // the keyboard or unsaved typing.
    private void ApplyProfile()
    {
        if (profileClosed)
        {
            return;
        }
        var store = state.Profile;
        // A panel cannot be disabled: its controls are.
        genderBox.IsEnabled = shareSwitch.IsEnabled = store.Loaded;
        foreach (var f in AllFields())
        {
            f.Box.IsEnabled = store.Loaded;
        }
        AboutYouGroup.Visibility = store.Loaded && !store.Supported ? Visibility.Collapsed : Visibility.Visible;
        profileSyncing = true;
        try
        {
            var g = store.Stored.Gender;
            var i = ProfileGender.All.ToList().IndexOf(g);
            genderBox.SelectedIndex = i >= 0 ? i : 0;
            shareSwitch.IsOn = store.Stored.ShareWithMcp;
        }
        finally
        {
            profileSyncing = false;
        }
        foreach (var f in AllFields())
        {
            if (!f.Busy)
            {
                SetFieldText(f, f.Stored());
            }
            UpdateCounter(f);
        }
        BuildPending();
    }

    private void SetFieldText(ProfileField f, string text)
    {
        if (f.Box.Text == text)
        {
            return;
        }
        profileSyncing = true;
        try
        {
            f.Box.Text = text;
        }
        finally
        {
            profileSyncing = false;
        }
    }

    // The assistant's proposals, while there are any.
    private void BuildPending()
    {
        var t = Assistant.ProfileTexts();
        var store = state.Profile;
        ProfilePendingGroup.Children.Clear();
        var proposals = store.Supported ? store.Proposals : [];
        ProfilePendingGroup.Visibility = proposals.Count > 0 ? Visibility.Visible : Visibility.Collapsed;
        if (proposals.Count == 0)
        {
            return;
        }
        var heading = new TextBlock { Text = t.Pending, Style = PageStyle("SettingsGroupHeaderStyle") };
        AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level2);
        ProfilePendingGroup.Children.Add(heading);
        foreach (var q in proposals)
        {
            ProfilePendingGroup.Children.Add(ProposalRow(q, t));
        }
    }

    private Border ProposalRow(ProfileProposal q, ProfileStrings t)
    {
        var text = new TextBlock { Text = q.Text, TextWrapping = TextWrapping.WrapWholeWords };
        var source = q.Source == ProfileSource.Panel ? t.FromPanel : t.FromOtherApp;
        var account = ProfileDialogs.AccountLine(state.Profile.Profile, q.AccountId?.Value);
        var from = new TextBlock { Text = source, Style = PageStyle("DimCaptionLabelStyle"), TextWrapping = TextWrapping.WrapWholeWords };
        var detail = new TextBlock
        {
            Text = account,
            Style = PageStyle("DimCaptionLabelStyle"),
            TextWrapping = TextWrapping.WrapWholeWords,
            Visibility = account.Length > 0 ? Visibility.Visible : Visibility.Collapsed,
        };
        var save = new Button { Content = t.Save };
        var edit = new Button { Content = t.Edit };
        var dismiss = new Button { Content = t.Dismiss };
        AutomationProperties.SetAutomationId(save, "ProfileProposalSave");
        AutomationProperties.SetAutomationId(edit, "ProfileProposalEdit");
        AutomationProperties.SetAutomationId(dismiss, "ProfileProposalDismiss");
        var id = q.Id;
        save.Click += (_, _) => _ = state.Profile.ResolveAsync(id, accept: true);
        dismiss.Click += (_, _) => _ = state.Profile.ResolveAsync(id, accept: false);
        edit.Click += async (_, _) =>
        {
            if (state.Profile.Proposal(id) is not { } current)
            {
                return;
            }
            var edited = await ProfileDialogs.EditProposalAsync(state.Alerts, window(), current.Text, state.Profile.Limits.Proposal);
            if (edited is not null)
            {
                await state.Profile.ResolveAsync(id, accept: true, edited);
            }
        };
        var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        buttons.Children.Add(save);
        buttons.Children.Add(edit);
        buttons.Children.Add(dismiss);
        var column = new StackPanel { Spacing = 6 };
        column.Children.Add(text);
        column.Children.Add(from);
        column.Children.Add(detail);
        column.Children.Add(buttons);
        var card = new Border
        {
            Child = column,
            Padding = new Thickness(16, 12, 16, 12),
            CornerRadius = new CornerRadius(4),
            BorderThickness = new Thickness(1),
            Background = (Brush)Application.Current.Resources["CardBackgroundFillColorDefaultBrush"],
            BorderBrush = (Brush)Application.Current.Resources["CardStrokeColorDefaultBrush"],
        };
        AutomationProperties.SetAutomationId(card, "ProfileProposal_" + id);
        return card;
    }

    // The page's styles (SettingsStyles.xaml, merged into its resources)
    // before the application's.
    private Style PageStyle(string key) =>
        Resources.TryGetValue(key, out var own) && own is Style s ? s : (Style)Application.Current.Resources[key];
}
