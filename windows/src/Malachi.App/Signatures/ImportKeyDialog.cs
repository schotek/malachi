// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first view of ui/internal/signature (ImportDialogTexts,
// PastedParams, FileParams, ImportFailedText); the macOS port is the sheet in
// Preferences/ImportPGPKeySheet.swift (written at the same time), GTK's the
// Adw.AlertDialog with a text area. The Import OpenPGP Key dialog of
// Preferences → Security: a text area for a pasted public key (its
// placeholder the armour's first line, not translated), Choose File… (a key
// file of at most api.MaxPGPKeyBytes, read here and imported at once), Cancel
// and Import, which is enabled while the pasted text can be imported. The
// dialog stays open while pgp.keyImport runs and shows the failure's sentence
// (a secret key is refused whole, nothing is stored) under the field; it
// closes when the key is in, and the page's toast says how many. Windows
// shows one ContentDialog at a time per window (DialogScheduler), so the
// file dialog of the shell is the only thing over it.

using System;
using System.IO;
using System.Threading.Tasks;
using Malachi.App.Compose;
using Malachi.App.Shell;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Malachi.Core.Signatures;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Signatures;

/// <summary>The Import OpenPGP Key dialog.</summary>
internal sealed partial class ImportKeyDialog : ContentDialog
{
    private readonly PgpKeysController keys;
    private readonly Window? owner;
    private readonly SignatureImportDialog texts = SignatureText.ImportDialogTexts();
    private readonly TextBox paste;
    private readonly TextBlock problem;
    private readonly Button choose;
    private bool busy;

    /// <summary>The dialog over <paramref name="keys"/>; <paramref name="ownerWindow"/> owns its file dialog.</summary>
    public ImportKeyDialog(PgpKeysController keys, Window? ownerWindow)
    {
        ArgumentNullException.ThrowIfNull(keys);
        this.keys = keys;
        owner = ownerWindow;
        Title = new TextBlock { Text = texts.Title, TextWrapping = TextWrapping.WrapWholeWords, MaxLines = 2 };
        PrimaryButtonText = Mnemonic.Strip(texts.Import);
        CloseButtonText = Mnemonic.Strip(texts.Cancel);
        DefaultButton = ContentDialogButton.Primary;
        IsPrimaryButtonEnabled = false;

        paste = new TextBox
        {
            AcceptsReturn = true,
            TextWrapping = TextWrapping.NoWrap,
            PlaceholderText = SignatureView.ArmorPlaceholder,
            FontFamily = (FontFamily)Application.Current.Resources["MonospaceFontFamily"],
            IsSpellCheckEnabled = false,
            MinWidth = 400,
            MinHeight = 140,
            MaxHeight = 260,
        };
        ScrollViewer.SetHorizontalScrollBarVisibility(paste, ScrollBarVisibility.Auto);
        ScrollViewer.SetVerticalScrollBarVisibility(paste, ScrollBarVisibility.Auto);
        AutomationProperties.SetAutomationId(paste, "ImportKeyText");
        AutomationProperties.SetName(paste, texts.Body);
        paste.TextChanged += (_, _) => Update();

        choose = new Button { Content = texts.ChooseFile };
        AutomationProperties.SetAutomationId(choose, "ImportKeyChooseFile");
        choose.Click += async (_, _) => await ChooseFileAsync();

        problem = new TextBlock
        {
            Style = (Style)Application.Current.Resources["CriticalCaptionLabelStyle"],
            TextWrapping = TextWrapping.Wrap,
            IsTextSelectionEnabled = true,
            Visibility = Visibility.Collapsed,
        };
        AutomationProperties.SetAutomationId(problem, "ImportKeyProblem");
        AutomationProperties.SetLiveSetting(problem, AutomationLiveSetting.Polite);

        var panel = new StackPanel { Spacing = 12 };
        panel.Children.Add(new TextBlock { Text = texts.Body, TextWrapping = TextWrapping.Wrap });
        panel.Children.Add(paste);
        panel.Children.Add(choose);
        panel.Children.Add(problem);
        Content = panel;

        // The key goes to the daemon on Import; the dialog stays open until it has it.
        PrimaryButtonClick += (_, args) =>
        {
            args.Cancel = true;
            if (SignatureView.PastedParams(paste.Text) is { } parameters)
            {
                Submit(parameters);
            }
        };
        Opened += (_, _) => paste.Focus(FocusState.Programmatic);
    }

    // Import is enabled while the pasted text can be imported (PastedParams)
    // and no import runs.
    private void Update()
    {
        IsPrimaryButtonEnabled = !busy && SignatureView.PastedParams(paste.Text) is not null;
        choose.IsEnabled = !busy;
        paste.IsEnabled = !busy;
    }

    private void Submit(PgpKeyImportParams parameters)
    {
        busy = true;
        Fail("");
        Update();
        keys.Import(parameters, failure =>
        {
            busy = false;
            Update();
            if (failure.Length == 0)
            {
                Hide();
                return;
            }
            Fail(failure);
        });
    }

    private void Fail(string text)
    {
        problem.Text = text;
        problem.Visibility = text.Length > 0 ? Visibility.Visible : Visibility.Collapsed;
    }

    // Choose File…: a key file, at most MaxPGPKeyBytes + 1 bytes of it read
    // (more is "too large"), imported at once.
    private async Task ChooseFileAsync()
    {
        if (busy || owner is null)
        {
            return;
        }
        string?[]? chosen;
        try
        {
            var picked = await ComposeFileDialog.OpenAsync(
                WindowPresenter.Handle(owner), texts.Title, multiple: false, (texts.FileFilter, "*.asc;*.gpg;*.pgp;*.key"));
            chosen = picked is null ? null : [.. picked];
        }
        catch (Exception e) when (e is System.Runtime.InteropServices.COMException or InvalidOperationException)
        {
            Fail(SignatureText.ImportFailedText(null));
            return;
        }
        if (chosen is not [{ } path])
        {
            return;
        }
        byte[] data;
        try
        {
            data = await ReadAsync(path);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            Fail(SignatureText.ImportFailedText(PgpImportFailure.Malformed));
            return;
        }
        if (data.Length > API.Limits.MaxPGPKeyBytes)
        {
            Fail(SignatureText.ImportFailedText(PgpImportFailure.TooLarge));
            return;
        }
        if (SignatureView.FileParams(data) is not { } parameters)
        {
            Fail(SignatureText.ImportFailedText(PgpImportFailure.NoKey));
            return;
        }
        Submit(parameters);
    }

    // At most MaxPGPKeyBytes + 1 bytes: the one over says "too large".
    private static async Task<byte[]> ReadAsync(string path)
    {
        await using var stream = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.Read, 4096, useAsync: true);
        var buffer = new byte[API.Limits.MaxPGPKeyBytes + 1];
        var read = 0;
        while (read < buffer.Length)
        {
            var n = await stream.ReadAsync(buffer.AsMemory(read));
            if (n == 0)
            {
                break;
            }
            read += n;
        }
        return buffer.AsSpan(0, read).ToArray();
    }
}
