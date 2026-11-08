// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Preferences/AIAccount/AIAccountSheetController.swift
// (present, replaceKey, dismiss) and AIAccountViewController.swift (refresh,
// fillModels, showBusy, showBanner, flag, syncTyped, trustClicked); GTK:
// ui/internal/aiaccount/dialog.go (New, Present, ReplaceKey, build,
// fillModels, wire, dismiss, onSave, onTrust, refresh, flag, showBusy,
// showBanner). The texts, the rules and the calls live in Core's
// AiAccountController; this shows what it holds (Refresh, on every change)
// and feeds back what the user did. The Integrations page opens it from its
// catalogue's Add… of an AI provider (that entry's provider) and from an AI
// provider account's row (Edit, and Sign In while the provider refused the
// key, which opens it on Replace Key…); the application's hooks
// EditAiAccount and AddAiAccount do the same from the panel, the board's
// triage and the status bar (PreferencesEntryPoints).
//
// The models, the consent and Save wait for a successful test
// (AiAccountController.Unlocked, CanSave):
// until then the name, the server address, the key, the pin and Test are
// what can be changed. Trust Certificate… asks the account assistant's
// "Trust This Certificate?" with the certificate's details on this window,
// then pins it and tests again.
//
// Windows (docs/windows-port.md §11.3): an owned modal window (ModalDialog)
// of the Adw.Dialog's 600×720; its close button, Escape and Ctrl+W cancel
// it, as Cancel does; late replies are dropped from then on
// (AiAccountController.Close). The model fields are editable combo boxes
// with the ids Test listed (GTK: an entry row with a popover of the models;
// the macOS combo boxes); a typed model reaches the controller when it is
// submitted (Enter, focus leaving), and Test and Save read both fields first.

using System;
using System.Collections.Generic;
using System.Linq;
using Malachi.App.Shell;
using Malachi.App.Wizard;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.I18n;
using Malachi.Core.IssueTrackers;
using Malachi.Core.Presentation;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using AiAccount = Malachi.Core.Model.AiAccount;
using CertTrust = Malachi.Core.Wizard.CertTrust;
using SettingsCard = CommunityToolkit.WinUI.Controls.SettingsCard;
using TrustPrompt = Malachi.Core.Wizard.TrustPrompt;
using VirtualKey = Windows.System.VirtualKey;

namespace Malachi.App.Preferences;

/// <summary>The dialog that adds or edits an AI provider account.</summary>
[System.Diagnostics.CodeAnalysis.SuppressMessage("Design", "CA1001", Justification = "The controller is closed with the window (Closed).")]
public sealed partial class AiAccountWindow : Window
{
    /// <summary>ai_account.blp content-width.</summary>
    public const int ContentWidth = 600;

    /// <summary>ai_account.blp content-height.</summary>
    public const int ContentHeight = 720;

    private readonly AppState state;
    private readonly AiAccountController controller;
    private readonly Action<AccountId, AccountConfig>? done;
    private readonly ILogger logger;
    private readonly ModalDialog modal;

    // The ids the model combo boxes list.
    private IReadOnlyList<AiModel> shownModels = [];

    // The widgets are being set from the controller: their events are not the user's.
    private bool applying;

    private AiAccountWindow(AppState state, AiProvider provider, Account? editing, Action<AccountId, AccountConfig>? done)
    {
        this.state = state;
        this.done = done;
        logger = state.Logs.CreateLogger<AiAccountWindow>();
        InitializeComponent();
        controller = new AiAccountController(
            state.Client, provider, editing, logger: state.Logs.CreateLogger<AiAccountController>());
        var title = controller.Title;
        Title = title;
        ExtendsContentIntoTitleBar = true;
        SetTitleBar(Header);
        Header.Title = title;
        Header.Loaded += (_, _) => Title = title;
        var icon = System.IO.Path.Combine(AppContext.BaseDirectory, "Assets", "Malachi.ico");
        if (System.IO.File.Exists(icon))
        {
            AppWindow.SetIcon(icon);
        }
        Tracked = state.Windows.Track(this, WindowKind.Wizard, Root, ToastsHost);
        modal = new ModalDialog(this);
        Build();
        Wire();
        Refresh();
        ShowBusy(controller.Progress);
        ShowBanner(controller.Banner);
        Root.Loaded += (_, _) => NameBox.Focus(FocusState.Programmatic);
        Closed += (_, _) =>
        {
            controller.Close();
            if (modal.ReturnToOwner() is { } activated)
            {
                LogReturned(logger, activated);
            }
        };
    }

    /// <summary>The window as the shell tracks it.</summary>
    public TrackedWindow Tracked { get; }

    /// <summary>
    /// Opens the dialog over <paramref name="owner"/> (the main window when
    /// null or hidden, shown first): adding an account of
    /// <paramref name="provider"/> when <paramref name="editing"/> is null,
    /// else editing that account (its provider wins), with the key field
    /// open for <paramref name="replaceKey"/> (a route that says the provider
    /// refused the key). <paramref name="done"/> runs once the account was
    /// stored, before the window closes. Null when there is no window to
    /// open it over.
    /// </summary>
    public static AiAccountWindow? Show(
        AppState state, Window? owner, AiProvider provider, Account? editing = null, bool replaceKey = false,
        Action<AccountId, AccountConfig>? done = null)
    {
        ArgumentNullException.ThrowIfNull(state);
        if (owner is null || !owner.AppWindow.IsVisible)
        {
            state.ShowMainWindow();
            owner = state.MainWindow;
        }
        if (owner is null || state.IsStopping)
        {
            return null;
        }
        var w = new AiAccountWindow(state, provider, editing, done);
        w.modal.Present(owner, ContentWidth, ContentHeight);
        w.controller.Start();
        if (replaceKey && editing is not null)
        {
            w.ReplaceKey();
        }
        LogPresented(w.logger);
        return w;
    }

    /// <summary>Replace Key…: the key field opens, empty, and takes the keyboard.</summary>
    public void ReplaceKey()
    {
        controller.ReplaceKey();
        DispatcherQueue.TryEnqueue(() => KeyBox.Focus(FocusState.Programmatic));
    }

    // build: the fixed texts; nothing is wired yet.
    private void Build()
    {
        var c = controller;
        var t = c.Texts;
        ProviderRow.Header = t.ProviderLabel;
        ProviderValue.Text = c.ProviderName;
        NameRow.Header = t.NameLabel;
        AutomationProperties.SetName(NameBox, t.NameLabel);
        NameBox.Text = c.CurrentForm.Name;
        AddressRow.Header = t.ServerLabel;
        AutomationProperties.SetName(AddressBox, t.ServerLabel);
        AddressBox.Text = c.CurrentForm.BaseUrl;
        AddressHint.Text = t.ServerHint;
        var server = c.UsesServer ? Visibility.Visible : Visibility.Collapsed;
        AddressRow.Visibility = server;
        AddressHint.Visibility = server;

        KeyStoredRow.Header = t.KeyStored;
        ReplaceKeyButton.Content = t.ReplaceKey;
        KeepKeyButton.Content = t.KeepKey;
        PinRow.Header = t.PinLabel;
        ForgetPinButton.Content = t.PinForget;
        Localization.MnemonicLabel.Apply(TestButton, t.Test);
        ToolTipService.SetToolTip(TestButton, t.Testing);
        // TRANSLATORS: button of the AI provider dialog after a test refused an https server's certificate the user may trust.
        TrustButton.Content = L10n.T("Trust Certificate…");

        ModelDescription.Text = t.ModelHint;
        ModelRow.Header = t.ModelLabel;
        AutomationProperties.SetName(ModelBox, t.ModelLabel);
        ModelBox.Text = c.CurrentForm.Model;
        BoardModelRow.Header = t.BoardModelLabel;
        BoardModelRow.Description = t.BoardModelHint;
        AutomationProperties.SetName(BoardModelBox, t.BoardModelLabel);
        BoardModelBox.Text = c.CurrentForm.BoardModel;

        ConsentHeading.Text = c.Consent.Heading;
        ConsentRow.Header = c.Consent.Checkbox;
        AutomationProperties.SetName(ConsentSwitch, c.Consent.Checkbox);

        Localization.MnemonicLabel.Apply(SaveButton, c.SaveLabel);
    }

    // wire: the controller to the widgets.
    private void Wire()
    {
        controller.Changed += (_, _) => Refresh();
        controller.BusyChanged += (_, text) => ShowBusy(text);
        controller.BannerChanged += (_, text) => ShowBanner(text);
        controller.ProblemShown += (_, field) => Flag(field);
        controller.CloseRequested += (_, _) => Close();
        controller.Done += (_, saved) =>
        {
            try
            {
                done?.Invoke(saved.Id, saved.Config);
            }
            finally
            {
                Close();
            }
        };
    }

    // refresh: what the controller holds now.
    private void Refresh()
    {
        var c = controller;
        var was = applying;
        applying = true;
        try
        {
            var form = c.CurrentForm;
            var editable = !c.Saving;
            foreach (var row in new Control[] { NameRow, AddressRow })
            {
                row.IsEnabled = editable;
            }
            SetText(NameBox, form.Name);
            if (c.UsesServer)
            {
                SetText(AddressBox, form.BaseUrl);
            }
            var warning = c.HttpWarningText;
            HttpBanner.Message = warning;
            HttpBanner.IsOpen = warning.Length > 0;

            var stored = c.KeyStored;
            KeyStoredRow.Visibility = stored ? Visibility.Visible : Visibility.Collapsed;
            KeyRow.Visibility = stored ? Visibility.Collapsed : Visibility.Visible;
            KeyRow.Header = c.KeyLabel;
            AutomationProperties.SetName(KeyBox, c.KeyLabel);
            if (KeyBox.Password != form.Key && KeyBox.FocusState == FocusState.Unfocused)
            {
                KeyBox.Password = form.Key;
            }
            KeepKeyButton.Visibility = c.IsEditing && !stored ? Visibility.Visible : Visibility.Collapsed;
            PinRow.Visibility = c.Pinned ? Visibility.Visible : Visibility.Collapsed;
            PinText.Text = c.PinText;
            foreach (var row in new Control[] { KeyStoredRow, KeyRow, PinRow })
            {
                row.IsEnabled = editable;
            }

            TestResult.Text = c.Test is { } outcome ? outcome.Text : c.Busy ? c.Progress ?? "" : "";
            TrustButton.Visibility = c.TrustOffer is null ? Visibility.Collapsed : Visibility.Visible;
            TrustButton.IsEnabled = editable && !c.Busy;
            TestButton.IsEnabled = c.CanTest;

            if (!SameModels(c.Models, shownModels))
            {
                FillModels(c.Models);
            }
            SetText(ModelBox, form.Model);
            SetText(BoardModelBox, form.BoardModel);
            // The models, the consent and Save wait for a successful test.
            ModelRow.IsEnabled = c.Unlocked;
            BoardModelRow.IsEnabled = c.Unlocked;

            ConsentGroup.Visibility = c.NeedsConsent ? Visibility.Visible : Visibility.Collapsed;
            ConsentBody.Text = c.ConsentBody;
            ConsentSwitch.IsOn = form.Consent;
            ConsentRow.IsEnabled = c.Unlocked;

            SaveButton.IsEnabled = c.CanSave;
        }
        finally
        {
            applying = was;
        }
    }

    // Sets a field from the controller unless the user is typing in it.
    private static void SetText(TextBox box, string value)
    {
        if (box.Text != value && box.FocusState == FocusState.Unfocused)
        {
            box.Text = value;
        }
    }

    private static void SetText(ComboBox box, string value)
    {
        if (box.Text != value && box.FocusState == FocusState.Unfocused)
        {
            box.Text = value;
        }
    }

    private static bool SameModels(IReadOnlyList<AiModel> a, IReadOnlyList<AiModel> b) =>
        a.Count == b.Count && a.Select(m => m.Id).SequenceEqual(b.Select(m => m.Id), StringComparer.Ordinal);

    // fillModels: the ids the provider listed (cleaned as any of its text);
    // what was typed stays.
    private void FillModels(IReadOnlyList<AiModel> models)
    {
        shownModels = models;
        var ids = models.Select(m => Jira.Clean(m.Id)).Where(id => id.Length > 0).Distinct(StringComparer.Ordinal).ToList();
        foreach (var box in new[] { ModelBox, BoardModelBox })
        {
            var typed = box.Text;
            box.Items.Clear();
            foreach (var id in ids)
            {
                box.Items.Add(id);
            }
            if (box.Text != typed)
            {
                box.Text = typed;
            }
        }
    }

    // showBusy: the progress of a call (null when none runs) with a spinner.
    private void ShowBusy(string? text)
    {
        ProgressText.Text = text ?? "";
        ProgressSpinner.IsActive = text is not null;
        Progress.Visibility = text is not null ? Visibility.Visible : Visibility.Collapsed;
        Refresh();
    }

    // showBanner: the page's banner; null hides it.
    private void ShowBanner(string? text)
    {
        Banner.Message = text ?? "";
        Banner.IsOpen = text is not null;
    }

    // flag: the row of the field to fix in the error look, and the field
    // takes the keyboard; None clears the look.
    private void Flag(AiAccount.Field field)
    {
        foreach (var row in new SettingsCard[] { NameRow, AddressRow, KeyRow, ModelRow, BoardModelRow, ConsentRow })
        {
            WizardRows.SetError(Root, row, false);
        }
        (SettingsCard Row, Control Focus)? target = field switch
        {
            AiAccount.Field.Name => (NameRow, NameBox),
            AiAccount.Field.Server => (AddressRow, AddressBox),
            AiAccount.Field.Key => (KeyRow, KeyBox),
            AiAccount.Field.Model => (ModelRow, ModelBox),
            AiAccount.Field.BoardModel => (BoardModelRow, BoardModelBox),
            AiAccount.Field.Consent => (ConsentRow, ConsentSwitch),
            _ => null,
        };
        if (target is not { } t)
        {
            return;
        }
        WizardRows.SetError(Root, t.Row, true);
        t.Row.StartBringIntoView();
        t.Focus.Focus(FocusState.Programmatic);
    }

    // syncTyped: what is in the fields, in case a change did not report yet
    // (a model typed and not submitted).
    private void SyncTyped()
    {
        var c = controller;
        c.SetName(NameBox.Text);
        if (c.UsesServer)
        {
            c.SetBaseUrl(AddressBox.Text);
        }
        if (!c.KeyStored)
        {
            c.SetKey(KeyBox.Password);
        }
        c.SetModel(ModelBox.Text);
        c.SetBoardModel(BoardModelBox.Text);
    }

    // onSave: the fields as typed, then account.add or account.update.
    private void Save()
    {
        SyncTyped();
        controller.Save();
    }

    private void OnSaveClick(object sender, RoutedEventArgs e) => Save();

    private void OnCancelClick(object sender, RoutedEventArgs e) => Close();

    private void OnTestClick(object sender, RoutedEventArgs e)
    {
        SyncTyped();
        controller.RunTest();
    }

    private void OnReplaceKeyClick(object sender, RoutedEventArgs e) => ReplaceKey();

    private void OnKeepKeyClick(object sender, RoutedEventArgs e) => controller.KeepKey();

    private void OnForgetPinClick(object sender, RoutedEventArgs e) => controller.ForgetPin();

    // onTrust: "Trust This Certificate?" with the certificate's details on
    // this window, then the pin and the test again.
    private async void OnTrustClick(object sender, RoutedEventArgs e)
    {
        if (controller.TrustOffer is not { Cert: { } cert } offer)
        {
            return;
        }
        var changed = offer.Category == CertTrust.Category.Changed;
        var prompt = TrustPrompt.Create(
            cert, [controller.Host], DateTimeOffset.Now, changed, changed ? offer.Expected : "", timeZone: TimeZoneInfo.Local);
        var ok = await state.Alerts.ConfirmTrustCertificateAsync(this, prompt.Heading, prompt.Body, prompt.Details, prompt.ConfirmLabel);
        if (!ok || controller.IsClosed)
        {
            return;
        }
        controller.Trust();
        LogTrusted(logger);
        controller.RunTest();
    }

    private void OnNameChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            controller.SetName(NameBox.Text);
        }
    }

    private void OnAddressChanged(object sender, TextChangedEventArgs e)
    {
        if (!applying)
        {
            controller.SetBaseUrl(AddressBox.Text);
        }
    }

    private void OnKeyChanged(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetKey(KeyBox.Password);
        }
    }

    // A model picked from the list goes into the field.
    private void OnModelSelected(object sender, SelectionChangedEventArgs e)
    {
        if (applying || sender is not ComboBox box || box.SelectedItem is not string picked)
        {
            return;
        }
        SetModelOf(box, picked);
    }

    // A model typed and submitted (Enter, the focus leaving): the combo box
    // keeps the text even when it lists no such item.
    private void OnModelSubmitted(ComboBox sender, ComboBoxTextSubmittedEventArgs args)
    {
        if (!applying)
        {
            SetModelOf(sender, args.Text);
        }
    }

    private void SetModelOf(ComboBox box, string value)
    {
        if (ReferenceEquals(box, ModelBox))
        {
            controller.SetModel(value);
        }
        else if (ReferenceEquals(box, BoardModelBox))
        {
            controller.SetBoardModel(value);
        }
    }

    private void OnConsentToggled(object sender, RoutedEventArgs e)
    {
        if (!applying)
        {
            controller.SetConsent(ConsentSwitch.IsOn);
        }
    }

    // The fields activate the default: Save once it can, else Test.
    private void OnFieldKeyDown(object sender, KeyRoutedEventArgs e)
    {
        if (e.Key != VirtualKey.Enter)
        {
            return;
        }
        e.Handled = true;
        if (SaveButton.IsEnabled)
        {
            Save();
        }
        else if (TestButton.IsEnabled)
        {
            SyncTyped();
            controller.RunTest();
        }
    }

    [LoggerMessage(Level = LogLevel.Information, Message = "AI provider account dialog opened")]
    private static partial void LogPresented(ILogger logger);

    [LoggerMessage(Level = LogLevel.Information, Message = "certificate trusted")]
    private static partial void LogTrusted(ILogger logger);

    [LoggerMessage(Level = LogLevel.Debug, Message = "AI provider account dialog closed, its owner enabled, activated {Activated}")]
    private static partial void LogReturned(ILogger logger, bool activated);
}
