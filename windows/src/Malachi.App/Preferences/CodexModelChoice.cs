// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of the ChatGPT branch of macos/Sources/MalachiMail/Preferences/
// ProviderModelBinding.swift; GTK: ui/internal/window/preferences_chatgpt.go
// (bindProviderModel with the ChatGPT provider in effect). The Model combo
// box of the assistant in the app over ChatGPT (Codex): the provider's
// default first, then the models of Codex's catalogue by their display
// names, and a stored model the catalogue no longer lists kept as an item
// of its own, so that it stays visible (the runtime still checks access).
// Choosing one writes assistant-chatgpt-model. Shared by the AI page and
// the ChatGPT window, which ask Codex for the catalogue themselves.

using System;
using System.Collections.Generic;
using Malachi.App.Assistants;
using Malachi.Core.ChatGPT;
using Microsoft.UI.Xaml.Controls;

namespace Malachi.App.Preferences;

/// <summary>A combo box of ChatGPT's models bound to a setting.</summary>
internal sealed class CodexModelChoice
{
    private readonly ComboBox box;
    private readonly Func<string> get;
    private readonly Action<string> set;
    private readonly List<string> ids = [];
    private bool syncing;

    /// <summary>The choice in <paramref name="box"/> of the model <paramref name="get"/> reads and <paramref name="set"/> writes.</summary>
    public CodexModelChoice(ComboBox box, Func<string> get, Action<string> set)
    {
        this.box = box;
        this.get = get;
        this.set = set;
        box.SelectionChanged += OnSelectionChanged;
        Show([]);
    }

    /// <summary>The default, <paramref name="models"/> and the stored model, the stored one chosen.</summary>
    public void Show(IReadOnlyList<CodexModel> models)
    {
        syncing = true;
        try
        {
            var selected = get();
            ids.Clear();
            box.Items.Clear();
            ids.Add("");
            box.Items.Add(new ComboBoxItem { Content = ChatGptText.DefaultModel });
            foreach (var model in models)
            {
                if (model.Id.Length == 0 || ids.Contains(model.Id))
                {
                    continue;
                }
                ids.Add(model.Id);
                box.Items.Add(new ComboBoxItem { Content = model.DisplayName });
            }
            if (selected.Length != 0 && !ids.Contains(selected))
            {
                ids.Add(selected);
                box.Items.Add(new ComboBoxItem { Content = selected });
            }
            box.SelectedIndex = ids.IndexOf(selected);
        }
        finally
        {
            syncing = false;
        }
    }

    private void OnSelectionChanged(object sender, SelectionChangedEventArgs e)
    {
        if (!syncing && box.SelectedIndex is var i && i >= 0 && i < ids.Count && ids[i] != get())
        {
            set(ids[i]);
        }
    }
}
