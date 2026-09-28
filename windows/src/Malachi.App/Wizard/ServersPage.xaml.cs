// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/AccountWizard/ServersPageController.swift
// (EndpointRows, apply, showPins, showProblems, setBusy, sync,
// securityChanged, forgetClicked); GTK: ui/internal/accountwizard/wizard.go
// (serverRows: read, apply, applyConfig, the security row's notify
// handler with PortForSecurityChange, the host and user rows' changed
// handlers, setBusy, onTest) and trust.go (the pin rows, Forget). Every
// change goes to the controller (WizardController.SetServers), which gives
// each endpoint the pin trusted for its host and port; the controller's
// ConfigApplied fills the rows without the port logic, and PinsChanged
// shows or hides the Pinned Certificate rows.

using System;
using CommunityToolkit.WinUI.Controls;
using Malachi.Core.Api;
using Malachi.Core.Controllers;
using Malachi.Core.Wizard;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Wizard;

/// <summary>The wizard's Servers page.</summary>
public sealed partial class ServersPage : UserControl
{
    private readonly WizardController wizard;
    private readonly Rows imap;
    private readonly Rows smtp;

    // The rows are being filled from the controller: no port logic.
    private bool applying = true;

    /// <summary>The page of <paramref name="wizard"/>.</summary>
    public ServersPage(WizardController wizard)
    {
        this.wizard = wizard;
        InitializeComponent();
        imap = new Rows(Endpoint.Imap, ImapHostRow, ImapHost, ImapPort, ImapSecurity, ImapUserRow, ImapUser, ImapPinRow, ImapPin, Security.Tls);
        smtp = new Rows(Endpoint.Smtp, SmtpHostRow, SmtpHost, SmtpPort, SmtpSecurity, SmtpUserRow, SmtpUser, SmtpPinRow, SmtpPin, Security.Starttls);
        applying = false;
        wizard.ConfigApplied += (_, cfg) => Apply(cfg);
        wizard.PinsChanged += (_, pins) => ShowPins(pins.Imap, pins.Smtp);
        wizard.ServerProblemsShown += (_, p) => ShowProblems(p);
        wizard.BusyChanged += (_, on) => SetBusy(on);
        ShowPins(wizard.PinnedFingerprints.Imap, wizard.PinnedFingerprints.Smtp);
    }

    // wizard.go applyConfig: fills the page without triggering the port logic.
    private void Apply(AccountConfig cfg)
    {
        applying = true;
        AccountNameBox.Text = cfg.Name;
        imap.Apply(cfg.Imap);
        smtp.Apply(cfg.Smtp);
        applying = false;
    }

    // The certificates pinned to the endpoints: a Pinned Certificate row
    // with the fingerprint per pin.
    private void ShowPins(string imapPin, string smtpPin)
    {
        imap.ShowPin(imapPin);
        smtp.ShowPin(smtpPin);
    }

    private void ShowProblems(ServerProblems p)
    {
        WizardRows.SetError(imap.HostRow, p.ImapHost);
        WizardRows.SetError(imap.UserRow, p.ImapUser);
        WizardRows.SetError(smtp.HostRow, p.SmtpHost);
        WizardRows.SetError(smtp.UserRow, p.SmtpUser);
    }

    // wizard.go setBusy: the page and its button wait for the call.
    private void SetBusy(bool on)
    {
        ServersPrefs.IsEnabled = !on;
        AccountNameBox.IsEnabled = !on;
        imap.SetEnabled(!on);
        smtp.SetEnabled(!on);
        TestButton.IsEnabled = !on;
    }

    // What the rows hold now, to the controller; not while they are being
    // filled from it, which would hand it a half-filled endpoint (a
    // TextBox's TextChanged comes later, with the whole of it).
    private void Sync()
    {
        if (!applying)
        {
            wizard.SetServers(AccountNameBox.Text, imap.Read(), smtp.Read());
        }
    }

    private void OnTextChanged(object sender, TextChangedEventArgs e)
    {
        // Typing in a flagged row clears its flag, as the GTK rows do.
        foreach (var rows in (Rows[])[imap, smtp])
        {
            if (ReferenceEquals(sender, rows.Host))
            {
                WizardRows.SetError(rows.HostRow, false);
            }
            else if (ReferenceEquals(sender, rows.User))
            {
                WizardRows.SetError(rows.UserRow, false);
            }
        }
        Sync();
    }

    private void OnPortChanged(NumberBox sender, NumberBoxValueChangedEventArgs args)
    {
        if (imap is null || smtp is null)
        {
            return;
        }
        if (double.IsNaN(args.NewValue))
        {
            // An emptied field: the SpinRow of GTK always holds a port.
            sender.Value = double.IsNaN(args.OldValue) ? Fields.DefaultPort(ReferenceEquals(sender, ImapPort) ? Endpoint.Imap : Endpoint.Smtp, Security.Tls) : args.OldValue;
            return;
        }
        Sync();
    }

    // The security row's notify::selected: the port follows the security
    // unless the rows are being filled from the controller.
    private void OnSecurityChanged(object sender, SelectionChangedEventArgs e)
    {
        if (imap is null || smtp is null || applying)
        {
            return;
        }
        var rows = ReferenceEquals(sender, ImapSecurity) ? imap : smtp;
        var to = Fields.SecurityAt(rows.SecurityBox.SelectedIndex);
        rows.SetPort(Fields.PortForSecurityChange(rows.Kind, rows.Port, rows.LastSecurity, to));
        rows.LastSecurity = to;
        Sync();
    }

    private void OnForgetClick(object sender, RoutedEventArgs e) =>
        wizard.ForgetPin(ReferenceEquals(sender, ImapForget) ? Endpoint.Imap : Endpoint.Smtp);

    private void OnTestClick(object sender, RoutedEventArgs e)
    {
        Sync();
        wizard.TestServers();
    }

    /// <summary>One endpoint's editors (wizard.go <c>serverRows</c>).</summary>
    private sealed class Rows(
        Endpoint kind,
        SettingsCard hostRow,
        TextBox host,
        NumberBox port,
        ComboBox security,
        SettingsCard userRow,
        TextBox user,
        SettingsCard pinRow,
        TextBlock pin,
        Security defaultSecurity)
    {
        public Endpoint Kind { get; } = kind;

        public SettingsCard HostRow { get; } = hostRow;

        public TextBox Host { get; } = host;

        public ComboBox SecurityBox { get; } = security;

        public SettingsCard UserRow { get; } = userRow;

        public TextBox User { get; } = user;

        public Security LastSecurity { get; set; } = defaultSecurity;

        public int Port => double.IsNaN(port.Value) ? 0 : (int)Math.Round(port.Value);

        // wizard.go serverRows.read (the pin is the controller's).
        public ServerFields Read() => new()
        {
            Host = Host.Text,
            Port = Port,
            Security = Fields.SecurityAt(SecurityBox.SelectedIndex),
            Username = User.Text,
        };

        // wizard.go serverRows.apply.
        public void Apply(ServerConfig? sc)
        {
            if (sc is null)
            {
                return;
            }
            Host.Text = sc.Host;
            SetPort(sc.Port);
            SecurityBox.SelectedIndex = Fields.IndexOfSecurity(sc.Security);
            LastSecurity = sc.Security;
            User.Text = sc.Username;
        }

        public void SetPort(int value) => port.Value = value;

        public void ShowPin(string fingerprint)
        {
            pin.Text = fingerprint;
            pinRow.Visibility = fingerprint.Length == 0 ? Visibility.Collapsed : Visibility.Visible;
        }

        public void SetEnabled(bool on)
        {
            Host.IsEnabled = on;
            port.IsEnabled = on;
            SecurityBox.IsEnabled = on;
            User.IsEnabled = on;
            pinRow.IsEnabled = on;
        }
    }
}
