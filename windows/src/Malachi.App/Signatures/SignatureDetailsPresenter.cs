// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-first view of ui/internal/signature (DetailsFor, Details); the macOS
// port is macos/Sources/MalachiMail/MessageView/SignatureDetailsPopover.swift
// (written at the same time), GTK's the details dialog
// (signature_details.blp). The details of a message's signature
// (docs/plans/signatures.md §6, §11.3) as a Flyout under the badge, one per
// click: the verdict's shield and short name, the explanation, the rows (who
// signed, the algorithm, the revocation state, the key's fingerprint, …), the
// notes, the certificates of an S/MIME chain and the signed headers that
// differ, the line a valid signature needs ("tells who sent the message, not
// that its content is true"), and the buttons: Check Revocation (the daemon
// waits at most 15 s, the client 20 s; a note under it says the issuer learns
// the certificate is in use), Import Key and Confirm Key… (a question with the
// fingerprint in monospace first). Everything shown comes from a certificate or
// a key and is set as TextBlock.Text only, selectable; the daemon's answer to
// notify.signaturesChanged rebuilds an open flyout. A toast (keys imported or
// confirmed) goes to the window of the message.
//
// The Confirm Key… question is a ContentDialog, which light-dismisses the
// flyout under it: the session lives until the question is answered.

using System;
using System.Threading.Tasks;
using Malachi.App.Controls;
using Malachi.App.Reader;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Presentation;
using Malachi.Core.Signatures;
using Malachi.Core.Text;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Controls.Primitives;
using Microsoft.UI.Xaml.Media;

namespace Malachi.App.Signatures;

/// <summary>Opens the details flyout of a message's signature.</summary>
internal sealed class SignatureDetailsPresenter
{
    private const double ContentWidth = 440;

    private readonly ReaderServices services;
    private readonly Func<Window?> host;

    /// <summary>A presenter over the application's services; the flyouts' toasts and questions go to <paramref name="hostWindow"/>.</summary>
    public SignatureDetailsPresenter(ReaderServices readerServices, Func<Window?> hostWindow)
    {
        services = readerServices;
        host = hostWindow;
    }

    /// <summary>
    /// Opens the flyout under <paramref name="anchor"/> for message
    /// <paramref name="id"/> whose From address is <paramref name="from"/> and
    /// whose verdict <c>message.get</c> carried as <paramref name="summary"/>.
    /// </summary>
    public void Show(FrameworkElement anchor, MessageId id, SignatureSummary summary, string from)
    {
        var state = services.State;
        var controller = new SignatureDetailsController(
            state.Client, id, summary, from, state.Logs.CreateLogger<SignatureDetailsController>());
        var session = new Session(controller, services, host);
        var flyout = new Flyout
        {
            Placement = FlyoutPlacementMode.BottomEdgeAlignedLeft,
            Content = session.Content,
            FlyoutPresenterStyle = PresenterStyle(),
        };
        session.Flyout = flyout;
        flyout.Closed += (_, _) => session.FlyoutClosed();
        session.Subscribe(state.Notifications.AddSignaturesChanged(controller.Refresh));
        controller.Load();
        flyout.ShowAt(anchor);
    }

    // The presenter's width follows the content; the height scrolls on its own.
    private static Style PresenterStyle()
    {
        var style = new Style(typeof(FlyoutPresenter));
        style.Setters.Add(new Setter(FrameworkElement.MaxWidthProperty, ContentWidth + 40));
        style.Setters.Add(new Setter(Control.PaddingProperty, new Thickness(16)));
        return style;
    }

    // One open flyout: the controller, the content it keeps up to date, and
    // what has to be let go of once the flyout and any question are over.
    private sealed class Session
    {
        private readonly SignatureDetailsController controller;
        private readonly ReaderServices services;
        private readonly Func<Window?> host;
        private readonly StackPanel body = new() { Spacing = 10, Width = ContentWidth };
        private IDisposable? subscription;
        private bool asking;
        private bool closed;

        public Session(SignatureDetailsController controller, ReaderServices services, Func<Window?> host)
        {
            this.controller = controller;
            this.services = services;
            this.host = host;
            Content = new ScrollViewer
            {
                MaxHeight = 560,
                Content = body,
                HorizontalScrollMode = ScrollMode.Disabled,
                HorizontalScrollBarVisibility = ScrollBarVisibility.Disabled,
                VerticalScrollBarVisibility = ScrollBarVisibility.Auto,
            };
            AutomationProperties.SetAutomationId(Content, "SignatureDetails");
            controller.Changed += (_, _) => Rebuild();
            controller.ToastRequested += (_, text) => services.ToastIn(host(), text);
            Rebuild();
        }

        public ScrollViewer Content { get; }

        public Flyout? Flyout { get; set; }

        public void Subscribe(IDisposable token) => subscription = token;

        public void FlyoutClosed()
        {
            if (!asking)
            {
                Dispose();
            }
        }

        private void Dispose()
        {
            if (closed)
            {
                return;
            }
            closed = true;
            subscription?.Dispose();
            controller.Dispose();
        }

        private void Rebuild()
        {
            if (closed)
            {
                return;
            }
            var view = controller.View(t => Format.FormatDateTime(t));
            body.Children.Clear();

            var heading = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 10 };
            heading.Children.Add(new SignatureGlyph { IconName = view.Icon, Tone = view.Style, GlyphSize = 24 });
            var title = new TextBlock
            {
                Text = view.Title,
                Style = StyleOf("Title2LabelStyle"),
                TextWrapping = TextWrapping.Wrap,
                VerticalAlignment = VerticalAlignment.Center,
            };
            AutomationProperties.SetAutomationId(title, "SignatureTitle");
            heading.Children.Add(title);
            body.Children.Add(heading);

            body.Children.Add(Text(view.Explanation, "BodyLabelStyle"));
            if (controller.Problem.Length > 0)
            {
                var problem = Text(controller.Problem, "CriticalLabelStyle");
                AutomationProperties.SetAutomationId(problem, "SignatureProblem");
                body.Children.Add(problem);
            }

            AddRows(view.Rows);
            foreach (var note in view.Notes)
            {
                body.Children.Add(Text(note, "CaptionLabelStyle"));
            }
            foreach (var section in view.Sections)
            {
                body.Children.Add(new TextBlock { Text = section.Title, Style = StyleOf("HeadingLabelStyle"), Margin = new Thickness(0, 6, 0, 0) });
                AddRows(section.Rows);
            }
            if (view.Caveat.Length > 0)
            {
                body.Children.Add(Text(view.Caveat, "DimCaptionLabelStyle"));
            }

            var buttons = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 8, Margin = new Thickness(0, 6, 0, 0) };
            foreach (var b in view.Buttons)
            {
                var button = new Button { Content = b.Label, IsEnabled = b.Enabled };
                AutomationProperties.SetAutomationId(button, "Signature" + b.Action);
                var action = b.Action;
                button.Click += (_, _) => OnAction(action, view.KeyFingerprint);
                buttons.Children.Add(button);
            }
            var close = new Button { Content = SignatureText.CloseLabel(), HorizontalAlignment = HorizontalAlignment.Right };
            AutomationProperties.SetAutomationId(close, "SignatureClose");
            close.Click += (_, _) => Flyout?.Hide();
            buttons.Children.Add(close);
            body.Children.Add(buttons);
            if (view.RevocationCaption.Length > 0)
            {
                body.Children.Add(Text(view.RevocationCaption, "DimCaptionLabelStyle"));
            }
        }

        private void AddRows(System.Collections.Generic.IReadOnlyList<SignatureRow> rows)
        {
            if (rows.Count == 0)
            {
                return;
            }
            var grid = new Grid { ColumnSpacing = 12, RowSpacing = 4 };
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(140) });
            grid.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            for (var i = 0; i < rows.Count; i++)
            {
                grid.RowDefinitions.Add(new RowDefinition { Height = GridLength.Auto });
                var label = new TextBlock { Text = rows[i].Label, Style = StyleOf("DimCaptionLabelStyle"), TextWrapping = TextWrapping.Wrap };
                var value = new TextBlock
                {
                    Text = rows[i].Value,
                    Style = StyleOf("CaptionLabelStyle"),
                    TextWrapping = TextWrapping.Wrap,
                    IsTextSelectionEnabled = true,
                };
                if (rows[i].Monospace)
                {
                    value.FontFamily = (FontFamily)Application.Current.Resources["MonospaceFontFamily"];
                }
                Grid.SetRow(label, i);
                Grid.SetRow(value, i);
                Grid.SetColumn(value, 1);
                grid.Children.Add(label);
                grid.Children.Add(value);
            }
            body.Children.Add(grid);
        }

        private void OnAction(SignatureAction action, string keyFingerprint)
        {
            switch (action)
            {
                case SignatureAction.CheckRevocation:
                    controller.CheckRevocation();
                    break;
                case SignatureAction.ImportKey:
                    controller.ImportKey();
                    break;
                case SignatureAction.ConfirmKey:
                    _ = ConfirmKeyAsync(keyFingerprint);
                    break;
            }
        }

        // Confirm Key…: the question with the fingerprint first, then pgp.keyVerify.
        private async Task ConfirmKeyAsync(string fingerprint)
        {
            asking = true;
            try
            {
                var question = SignatureText.ConfirmKeyQuestion();
                var yes = await services.State.Alerts.ShowDialogAsync(host(), () => KeyQuestion(question, fingerprint));
                if (yes)
                {
                    controller.ConfirmKey();
                }
            }
            finally
            {
                asking = false;
                if (Flyout is not { IsOpen: true })
                {
                    Dispose();
                }
            }
        }

        private static TextBlock Text(string text, string style) => new()
        {
            Text = text,
            Style = StyleOf(style),
            TextWrapping = TextWrapping.Wrap,
            IsTextSelectionEnabled = true,
        };

        private static Style StyleOf(string key) => (Style)Application.Current.Resources[key];
    }

    /// <summary>
    /// The question of Confirm Key…: the sentence and, in monospace under it,
    /// the key's fingerprint to compare; Cancel is the default.
    /// </summary>
    internal static ContentDialog KeyQuestion(SignatureQuestion question, string fingerprint)
    {
        ArgumentNullException.ThrowIfNull(question);
        var panel = new StackPanel { Spacing = 12 };
        panel.Children.Add(new TextBlock { Text = question.Body, TextWrapping = TextWrapping.Wrap, IsTextSelectionEnabled = true });
        if (fingerprint.Length > 0)
        {
            var print = new TextBlock
            {
                Text = fingerprint,
                FontFamily = (FontFamily)Application.Current.Resources["MonospaceFontFamily"],
                TextWrapping = TextWrapping.Wrap,
                IsTextSelectionEnabled = true,
            };
            AutomationProperties.SetAutomationId(print, "SignatureKeyFingerprint");
            panel.Children.Add(print);
        }
        return new ContentDialog
        {
            Title = new TextBlock { Text = question.Heading, TextWrapping = TextWrapping.WrapWholeWords, MaxLines = 4 },
            Content = panel,
            PrimaryButtonText = Mnemonic.Strip(question.Accept),
            CloseButtonText = Mnemonic.Strip(question.Cancel),
            DefaultButton = ContentDialogButton.Close,
        };
    }
}
