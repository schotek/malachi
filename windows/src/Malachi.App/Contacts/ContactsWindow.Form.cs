// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/Contacts/ContactFormView.swift (the
// form on the card, W4: the book of a new contact, the name parts, company
// and job title, the labelled rows with their label menus and Add …, the
// street of several lines, the birthday, the note); GTK:
// ui/internal/window/contacts_form.go (renderForm, renderProblems, marked,
// buildForm, labelPicker, removeButton, addButton, labelledRows,
// addressRows, streetField, birthdayRow, noteField). The fields write into
// Core's ContactBook.Form as the user types (ContactsDetailController.
// UpdateForm); the form is built again only when its structure changes (a
// row added or removed, Reload after a conflict put the server's version
// under the user's changes) or another form opens. The validation's
// problems and a failed save's sentence stand in the detail's InfoBar, the
// fields concerned marked in the critical colour. Done and Cancel are the
// CommandBar's.

using System;
using System.Collections.Generic;
using System.Globalization;
using Malachi.Core.Api;
using Malachi.Core.Contacts;
using Microsoft.UI.Xaml;
using Microsoft.UI.Xaml.Automation;
using Microsoft.UI.Xaml.Controls;
using Microsoft.UI.Xaml.Input;
using Microsoft.UI.Xaml.Media;
using FieldKey = Malachi.Core.Contacts.ContactBook.FieldKey;
using FieldKind = Malachi.Core.Contacts.ContactBook.FieldKind;
using LabelledValue = Malachi.Core.Contacts.ContactBook.LabelledValue;

namespace Malachi.App.Contacts;

/// <summary>The contact form of the Contacts window.</summary>
public sealed partial class ContactsWindow
{
    // The form built into the detail, the card it was built over (Rebase
    // replaces it), a row was added or removed, the field to focus after a
    // rebuild ("" the first).
    private ContactBook.Form? formShown;
    private ContactCard? formCard;
    private bool formRebuild;
    private string formFocus = "";
    private StackPanel? formPanel;

    // The fields by problem key ("Emails:1", "Given:-1"), for the marks.
    private readonly Dictionary<string, Control> fields = new(StringComparer.Ordinal);

    private static string ProblemKey(FieldKey k, int i) => k.ToString() + ":" + i.ToString(CultureInfo.InvariantCulture);

    private void ForgetForm()
    {
        if (formShown is null)
        {
            return;
        }
        formShown = null;
        formCard = null;
        formPanel = null;
        fields.Clear();
        DetailHost.Children.Clear();
    }

    // renderForm: the form when needed, its problems marked.
    private void RenderForm(ContactBook.Form f)
    {
        if (!ReferenceEquals(formShown, f) || !ReferenceEquals(formCard, detail.Card) || formRebuild)
        {
            var focus = ReferenceEquals(formShown, f) ? formFocus : "";
            BuildForm(f, focus);
        }
        if (formPanel is not null)
        {
            formPanel.IsHitTestVisible = !detail.Saving && !detail.Deleting;
            formPanel.Opacity = detail.Saving || detail.Deleting ? 0.6 : 1;
        }
        foreach (var c in fields.Values)
        {
            c.ClearValue(Control.BorderBrushProperty);
        }
        var critical = (Brush)Application.Current.Resources["SystemFillColorCriticalBrush"];
        foreach (var p in detail.Problems)
        {
            foreach (var c in Marked(p))
            {
                c.BorderBrush = critical;
            }
        }
    }

    // marked: a problem's field, or every row of a list it names as a whole.
    private List<Control> Marked(ContactBook.Problem p)
    {
        if (fields.TryGetValue(ProblemKey(p.Key, p.Index ?? -1), out var one))
        {
            return [one];
        }
        var output = new List<Control>();
        if (p.Index is not null)
        {
            return output;
        }
        var prefix = p.Key.ToString() + ":";
        foreach (var (k, c) in fields)
        {
            if (k.StartsWith(prefix, StringComparison.Ordinal))
            {
                output.Add(c);
            }
        }
        return output;
    }

    private void BuildForm(ContactBook.Form f, string focus)
    {
        DetailHost.Children.Clear();
        fields.Clear();
        formShown = f;
        formCard = detail.Card;
        formRebuild = false;
        formFocus = "";
        var panel = new StackPanel { Spacing = 14 };
        formPanel = panel;
        AutomationProperties.SetAutomationId(panel, "ContactsForm");
        DetailHost.Children.Add(panel);
        var heading = new TextBlock
        {
            Text = f.IsNew ? ContactsText.NewContact : ContactBook.Title(f.Original),
            Style = (Style)Application.Current.Resources["Title2LabelStyle"],
            TextWrapping = TextWrapping.Wrap,
        };
        AutomationProperties.SetHeadingLevel(heading, Microsoft.UI.Xaml.Automation.Peers.AutomationHeadingLevel.Level1);
        panel.Children.Add(heading);

        if (f.IsNew)
        {
            var choices = ContactBook.BookChoices(list.Books, list.Accounts);
            if (choices.Count > 0)
            {
                var box = new ComboBox { Header = ContactBook.BookLabel, MinWidth = 260 };
                AutomationProperties.SetAutomationId(box, "ContactsFormBook");
                AutomationProperties.SetName(box, ContactBook.BookLabel);
                var at = 0;
                for (var i = 0; i < choices.Count; i++)
                {
                    // A string content is shown by a TextBlock's Text, never parsed.
                    box.Items.Add(choices[i].Text);
                    if (choices[i].Book == f.Book)
                    {
                        at = i;
                    }
                }
                box.SelectedIndex = at;
                box.SelectionChanged += (_, _) =>
                {
                    if (box.SelectedIndex is var i && i >= 0 && i < choices.Count)
                    {
                        var book = choices[i].Book;
                        detail.UpdateForm(x => x.Book = book);
                    }
                };
                panel.Children.Add(box);
            }
        }

        panel.Children.Add(Group(
            Text(FieldKey.Prefix, f.Prefix, (x, v) => x.Prefix = v),
            Text(FieldKey.Given, f.Given, (x, v) => x.Given = v),
            Text(FieldKey.Middle, f.Middle, (x, v) => x.Middle = v),
            Text(FieldKey.Family, f.Family, (x, v) => x.Family = v),
            Text(FieldKey.Suffix, f.Suffix, (x, v) => x.Suffix = v),
            Text(FieldKey.Nickname, f.Nickname, (x, v) => x.Nickname = v)));
        panel.Children.Add(Group(
            Text(FieldKey.Org, f.Org, (x, v) => x.Org = v),
            Text(FieldKey.Title, f.Title, (x, v) => x.Title = v)));

        panel.Children.Add(LabelledRows(f, FieldKind.Email, FieldKey.Emails, x => x.Emails, InputScopeNameValue.EmailSmtpAddress));
        panel.Children.Add(LabelledRows(f, FieldKind.Phone, FieldKey.Phones, x => x.Phones, InputScopeNameValue.TelephoneNumber));
        panel.Children.Add(AddressRows(f));
        panel.Children.Add(LabelledRows(f, FieldKind.Url, FieldKey.Urls, x => x.Urls, InputScopeNameValue.Url));
        panel.Children.Add(BirthdayRow(f));
        panel.Children.Add(NoteField(f));

        var grab = fields.TryGetValue(focus, out var w) ? w : fields.GetValueOrDefault(ProblemKey(FieldKey.Given, -1));
        if (grab is not null)
        {
            DispatcherQueue.TryEnqueue(() => grab.Focus(FocusState.Programmatic));
        }
    }

    private static StackPanel Group(params UIElement[] children)
    {
        var g = new StackPanel { Spacing = 6 };
        foreach (var c in children)
        {
            g.Children.Add(c);
        }
        return g;
    }

    // A text field with its label; what is typed goes into the form.
    private TextBox Text(FieldKey k, string value, Action<ContactBook.Form, string> set)
    {
        var label = ContactBook.Placeholder(k);
        var box = new TextBox { Header = label, Text = value, IsSpellCheckEnabled = false };
        AutomationProperties.SetAutomationId(box, "ContactsForm:" + k);
        box.TextChanged += (_, _) =>
        {
            var v = box.Text;
            detail.UpdateForm(x => set(x, v));
        };
        fields[ProblemKey(k, -1)] = box;
        return box;
    }

    // A row's label choice.
    private static ComboBox LabelPicker(FieldKind k, ContactLabel current, Action<ContactLabel> set)
    {
        var choices = ContactBook.LabelChoices(k, current);
        var box = new ComboBox { MinWidth = 110, VerticalAlignment = VerticalAlignment.Top };
        var at = 0;
        for (var i = 0; i < choices.Count; i++)
        {
            box.Items.Add(ContactBook.LabelText(k, choices[i]));
            if (choices[i] == current)
            {
                at = i;
            }
        }
        box.SelectedIndex = at;
        box.SelectionChanged += (_, _) =>
        {
            if (box.SelectedIndex is var i && i >= 0 && i < choices.Count)
            {
                set(choices[i]);
            }
        };
        return box;
    }

    // A row's remove button.
    private Button RemoveButton(string automationId, Action remove)
    {
        var b = new Button
        {
            Content = Resources.Icons.Create("list-remove", 14),
            Width = 32,
            Height = 32,
            Padding = new Thickness(0),
            VerticalAlignment = VerticalAlignment.Top,
            Style = (Style)Application.Current.Resources["SubtleButtonStyle"],
        };
        ToolTipService.SetToolTip(b, ContactBook.RemoveRowText);
        AutomationProperties.SetName(b, ContactBook.RemoveRowText);
        AutomationProperties.SetAutomationId(b, automationId);
        b.Click += (_, _) =>
        {
            detail.UpdateForm(_ => remove());
            formRebuild = true;
            RenderDetail();
        };
        return b;
    }

    // "Add Email" and the like.
    private HyperlinkButton AddButton(ContactBook.Form f, FieldKind k, FieldKey key)
    {
        var b = new HyperlinkButton { Content = ContactBook.AddRowText(k), IsEnabled = f.CanAdd(k), Padding = new Thickness(2, 4, 2, 4) };
        AutomationProperties.SetAutomationId(b, "ContactsAdd:" + k);
        b.Click += (_, _) =>
        {
            int? added = null;
            detail.UpdateForm(x => added = x.AddRow(k));
            if (added is { } i && i >= 0)
            {
                formFocus = ProblemKey(key, i);
                formRebuild = true;
                RenderDetail();
            }
        };
        return b;
    }

    // The e-mail, phone or website rows with Add under them.
    private StackPanel LabelledRows(
        ContactBook.Form f, FieldKind k, FieldKey key, Func<ContactBook.Form, List<LabelledValue>> rowsOf, InputScopeNameValue scope)
    {
        var box = new StackPanel { Spacing = 6 };
        var rows = rowsOf(f);
        for (var i = 0; i < rows.Count; i++)
        {
            var idx = i;
            var line = new Grid { ColumnSpacing = 6 };
            line.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            line.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            line.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            line.Children.Add(LabelPicker(k, rows[i].Label, l => detail.UpdateForm(x =>
            {
                var list = rowsOf(x);
                if (idx < list.Count)
                {
                    list[idx] = list[idx] with { Label = l };
                }
            })));
            var placeholder = ContactBook.Placeholder(key);
            var value = new TextBox { Text = rows[i].Value, PlaceholderText = placeholder, IsSpellCheckEnabled = false };
            value.InputScope = new InputScope { Names = { new InputScopeName(scope) } };
            AutomationProperties.SetName(value, placeholder);
            AutomationProperties.SetAutomationId(value, "ContactsForm:" + key + ":" + i.ToString(CultureInfo.InvariantCulture));
            value.TextChanged += (_, _) =>
            {
                var v = value.Text;
                detail.UpdateForm(x =>
                {
                    var list = rowsOf(x);
                    if (idx < list.Count)
                    {
                        list[idx] = list[idx] with { Value = v };
                    }
                });
            };
            fields[ProblemKey(key, i)] = value;
            Grid.SetColumn(value, 1);
            line.Children.Add(value);
            var remove = RemoveButton("ContactsRemove:" + key + ":" + i.ToString(CultureInfo.InvariantCulture), () => formShown?.RemoveRow(k, idx));
            Grid.SetColumn(remove, 2);
            line.Children.Add(remove);
            box.Children.Add(line);
        }
        box.Children.Add(AddButton(f, k, key));
        return box;
    }

    // The postal addresses: the label, the street of several lines (Enter
    // adds a line), postal code and city, region and country.
    private StackPanel AddressRows(ContactBook.Form f)
    {
        var box = new StackPanel { Spacing = 6 };
        for (var i = 0; i < f.Addresses.Count; i++)
        {
            var idx = i;
            var a = f.Addresses[i];
            void Edit(Func<ContactAddress, ContactAddress> change) => detail.UpdateForm(x =>
            {
                if (idx < x.Addresses.Count)
                {
                    x.Addresses[idx] = change(x.Addresses[idx]);
                }
            });
            TextBox Part(ContactBook.AddressPart p, string? v, Func<ContactAddress, string, ContactAddress> set)
            {
                var name = ContactBook.AddressPlaceholder(p);
                var t = new TextBox { Text = v ?? "", PlaceholderText = name, IsSpellCheckEnabled = false };
                AutomationProperties.SetName(t, name);
                AutomationProperties.SetAutomationId(t, "ContactsForm:" + p + ":" + idx.ToString(CultureInfo.InvariantCulture));
                t.TextChanged += (_, _) =>
                {
                    var text = t.Text;
                    Edit(c => set(c, text));
                };
                return t;
            }
            var top = new Grid { ColumnSpacing = 6 };
            top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            top.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            top.ColumnDefinitions.Add(new ColumnDefinition { Width = GridLength.Auto });
            top.Children.Add(LabelPicker(FieldKind.Address, a.Label, l => Edit(c => c with { Label = l })));
            var street = Part(ContactBook.AddressPart.Street, a.Street, (c, v) => c with { Street = v });
            street.AcceptsReturn = true;
            street.TextWrapping = TextWrapping.Wrap;
            street.MinHeight = 56;
            fields[ProblemKey(FieldKey.Addresses, i)] = street;
            Grid.SetColumn(street, 1);
            top.Children.Add(street);
            var remove = RemoveButton("ContactsRemove:" + FieldKey.Addresses + ":" + i.ToString(CultureInfo.InvariantCulture), () => formShown?.RemoveRow(FieldKind.Address, idx));
            Grid.SetColumn(remove, 2);
            top.Children.Add(remove);

            var city = new Grid { ColumnSpacing = 6, Margin = new Thickness(116, 0, 38, 0) };
            city.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(100) });
            city.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            city.Children.Add(Part(ContactBook.AddressPart.Postcode, a.Postcode, (c, v) => c with { Postcode = v }));
            var cityBox = Part(ContactBook.AddressPart.City, a.City, (c, v) => c with { City = v });
            Grid.SetColumn(cityBox, 1);
            city.Children.Add(cityBox);
            var rest = new Grid { ColumnSpacing = 6, Margin = new Thickness(116, 0, 38, 0) };
            rest.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            rest.ColumnDefinitions.Add(new ColumnDefinition { Width = new GridLength(1, GridUnitType.Star) });
            rest.Children.Add(Part(ContactBook.AddressPart.Region, a.Region, (c, v) => c with { Region = v }));
            var country = Part(ContactBook.AddressPart.Country, a.Country, (c, v) => c with { Country = v });
            Grid.SetColumn(country, 1);
            rest.Children.Add(country);
            box.Children.Add(Group(top, city, rest));
        }
        box.Children.Add(AddButton(f, FieldKind.Address, FieldKey.Addresses));
        return box;
    }

    // Add Birthday, or the day, the month and the optional year with a remove button.
    private UIElement BirthdayRow(ContactBook.Form f)
    {
        if (f.Birthday is not { } bd)
        {
            var add = new HyperlinkButton { Content = ContactBook.AddBirthday, Padding = new Thickness(2, 4, 2, 4) };
            AutomationProperties.SetAutomationId(add, "ContactsAdd:Birthday");
            add.Click += (_, _) =>
            {
                detail.UpdateForm(x => x.Birthday = new ContactBirthday { Month = 1, Day = 1 });
                formFocus = ProblemKey(FieldKey.Birthday, -1);
                formRebuild = true;
                RenderDetail();
            };
            return add;
        }
        var label = ContactBook.Placeholder(FieldKey.Birthday);
        var line = new StackPanel { Orientation = Orientation.Horizontal, Spacing = 6 };
        var day = new ComboBox { Header = label, MinWidth = 72 };
        for (var d = 1; d <= 31; d++)
        {
            day.Items.Add(d.ToString(CultureInfo.CurrentCulture));
        }
        day.SelectedIndex = Math.Clamp(bd.Day, 1, 31) - 1;
        AutomationProperties.SetName(day, label);
        AutomationProperties.SetAutomationId(day, "ContactsForm:BirthdayDay");
        day.SelectionChanged += (_, _) =>
        {
            var v = day.SelectedIndex + 1;
            detail.UpdateForm(x => x.Birthday = x.Birthday is { } b ? b with { Day = v } : null);
        };
        var month = new ComboBox { MinWidth = 140, VerticalAlignment = VerticalAlignment.Bottom };
        for (var m = 1; m <= 12; m++)
        {
            month.Items.Add(contacts.Options.Format(new DateTimeOffset(2000, m, 1, 12, 0, 0, TimeSpan.Zero), "%B"));
        }
        month.SelectedIndex = Math.Clamp(bd.Month, 1, 12) - 1;
        AutomationProperties.SetName(month, label);
        AutomationProperties.SetAutomationId(month, "ContactsForm:BirthdayMonth");
        month.SelectionChanged += (_, _) =>
        {
            var v = month.SelectedIndex + 1;
            detail.UpdateForm(x => x.Birthday = x.Birthday is { } b ? b with { Month = v } : null);
        };
        var year = new TextBox
        {
            Text = bd.Year > 0 ? bd.Year.ToString(CultureInfo.InvariantCulture) : "",
            MaxLength = 4,
            Width = 72,
            VerticalAlignment = VerticalAlignment.Bottom,
            InputScope = new InputScope { Names = { new InputScopeName(InputScopeNameValue.Number) } },
        };
        AutomationProperties.SetName(year, label);
        AutomationProperties.SetAutomationId(year, "ContactsForm:BirthdayYear");
        year.TextChanged += (_, _) =>
        {
            var n = int.TryParse(year.Text.Trim(), NumberStyles.None, CultureInfo.InvariantCulture, out var y) ? y : 0;
            detail.UpdateForm(x => x.Birthday = x.Birthday is { } b ? b with { Year = n } : null);
        };
        var remove = RemoveButton("ContactsRemove:Birthday", () =>
        {
            if (formShown is not null)
            {
                formShown.Birthday = null;
            }
        });
        remove.VerticalAlignment = VerticalAlignment.Bottom;
        line.Children.Add(day);
        line.Children.Add(month);
        line.Children.Add(year);
        line.Children.Add(remove);
        fields[ProblemKey(FieldKey.Birthday, -1)] = day;
        return line;
    }

    // The note: several lines.
    private TextBox NoteField(ContactBook.Form f)
    {
        var label = ContactBook.Placeholder(FieldKey.Note);
        var note = new TextBox
        {
            Header = label,
            Text = f.Note,
            AcceptsReturn = true,
            TextWrapping = TextWrapping.Wrap,
            MinHeight = 90,
        };
        AutomationProperties.SetName(note, label);
        AutomationProperties.SetAutomationId(note, "ContactsForm:" + FieldKey.Note);
        note.TextChanged += (_, _) =>
        {
            var v = note.Text;
            detail.UpdateForm(x => x.Note = v);
        };
        fields[ProblemKey(FieldKey.Note, -1)] = note;
        return note;
    }
}
