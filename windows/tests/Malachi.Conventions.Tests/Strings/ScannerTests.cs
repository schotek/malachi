// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The scanners of the strings check on small sources, as
// macos/scripts/test_check_strings.py tests check-strings.py: what counts
// as a msgid, a sink and the Windows-only mark.

using System.Linq;
using Xunit;

namespace Malachi.Conventions.Tests.Strings;

public sealed class ScannerTests
{
    [Fact]
    public void CSharpCallsAreFoundWithTheirKind()
    {
        var s = CSharpStrings.Parse("""
            class A
            {
                void F(int n, string x)
                {
                    var a = L10n.T("Inbox");
                    var b = Malachi.Core.I18n.L10n.C("folder", "Sent");
                    var c = L10n.N("%d message", "%d messages", n);
                    var d = L10n.T(x);
                    var e = L10n.T("Escaped … and \"quoted\"");
                    var f = T("not the shim");
                }
            }
            """);
        Assert.Equal(5, s.Calls.Count);
        Assert.Contains(s.Calls, c => c is { Kind: 'T', Msgid: "Inbox", Where: "test.cs:5" });
        Assert.Contains(s.Calls, c => c is { Kind: 'C', Context: "folder", Msgid: "Sent" });
        Assert.Contains(s.Calls, c => c is { Kind: 'N', Msgid: "%d message", Plural: "%d messages" });
        Assert.Contains(s.Calls, c => c is { Kind: 'T', Msgid: null });
        Assert.Contains(s.Calls, c => c.Msgid == "Escaped " + (char)0x2026 + " and \"quoted\"");
        Assert.Contains("not the shim", s.Literals);
    }

    [Fact]
    public void ALiteralInASinkNeedsTheMark()
    {
        var s = CSharpStrings.Parse("""
            class A
            {
                void F(TextBlock t, Button b)
                {
                    t.Text = "Unmarked";
                    t.Text = "Trailing mark"; // Windows-only string
                    // Windows-only string: the line above marks it.
                    b.Content = "Marked above";
                    var d = new ContentDialog
                    {
                        // Windows-only string
                        CloseButtonText = "Close",
                        PrimaryButtonText = "Unmarked in an initializer",
                    };
                    ToolTipService.SetToolTip(b, "Unmarked tooltip");
                    t.Text = L10n.T("Inbox");
                    t.Text = "";
                    t.Text = "123";
                }

                void G(TextBlock t)
                {
                    // Windows-only strings from here on.
                    t.Text = "One";
                    t.Text = "Two";
                }
            }
            """);
        Assert.Equal(
            ["test.cs:5: \"Unmarked\"", "test.cs:13: \"Unmarked in an initializer\"", "test.cs:15: \"Unmarked tooltip\""],
            s.UnmarkedSinks);
    }

    [Fact]
    public void ALiteralFormattedIntoATranslationNeedsTheMark()
    {
        var s = CSharpStrings.Parse("""
            class A
            {
                string F(string x) => L10n.T("Opening %s failed", "the file");
                string G(string x) => L10n.T("Opening %s failed", "the link"); // Windows-only string
                string H(string x) => L10n.T("Opening %s failed", x);
            }
            """);
        Assert.Equal(["test.cs:3: \"the file\""], s.LiteralArguments);
        Assert.Contains(s.Calls, c => c.WindowsOnly && c.Where == "test.cs:4");
    }

    [Fact]
    public void XamlMsgidsAndSinks()
    {
        var s = XamlStrings.Parse("""
            <Grid xmlns="http://schemas.microsoft.com/winfx/2006/xaml/presentation"
                  xmlns:x="http://schemas.microsoft.com/winfx/2006/xaml"
                  xmlns:loc="using:Malachi.App.Localization">
                <TextBox PlaceholderText="{loc:T Msgid='Search Mail'}" />
                <ToggleButton Content="{loc:T Msgid='Folder', Context='search scope'}" />
                <Button>
                    <Button.Content>
                        <loc:T Msgid="Load More" />
                    </Button.Content>
                </Button>
                <TextBlock Text="Unmarked" />
                <!-- Windows-only string -->
                <TextBlock Text="Marked" />
                <!-- Windows-only strings: the provisional panel. -->
                <StackPanel>
                    <TextBlock Text="Marked by the panel" />
                    <TextBlock>Inner text, marked too</TextBlock>
                </StackPanel>
                <TextBlock>Inner text</TextBlock>
                <TextBlock Text="{x:Bind Title}" AutomationProperties.Name="Unmarked name" />
            </Grid>
            """);
        Assert.Equal(3, s.Calls.Count);
        Assert.Contains(s.Calls, c => c is { Kind: 'T', Msgid: "Search Mail", Where: "test.xaml:4" });
        Assert.Contains(s.Calls, c => c is { Kind: 'C', Context: "search scope", Msgid: "Folder" });
        Assert.Contains(s.Calls, c => c is { Kind: 'T', Msgid: "Load More" });
        Assert.Equal(
            ["test.xaml:11: Text=\"Unmarked\"", "test.xaml:19: <TextBlock>Inner text", "test.xaml:20: AutomationProperties.Name=\"Unmarked name\""],
            s.UnmarkedSinks.ToList());
    }
}
