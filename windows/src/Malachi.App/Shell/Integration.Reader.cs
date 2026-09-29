// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Wave 2 (E4) of Integration.cs: the reader, the message windows, the
// attachments and the links (Reader/ReaderHub), the counterpart of
// Integration.swift's reader, MessageWindows and MessageActionsController.
// Made last, once the controllers exist and are wired to each other; let go
// with the Integration's other tokens.

using Malachi.App.Reader;

namespace Malachi.App.Shell;

/// <summary>The reading half of the Integration.</summary>
public sealed partial class Integration
{
    /// <summary>The reader, the message windows and what they share (wave 2, E4).</summary>
    public ReaderHub? Reader { get; private set; }

    private void WireReader()
    {
        var hub = new ReaderHub(state, mainWindow, this);
        Reader = hub;
        tokens.Add(hub);
        // The Assistant's menus and panel act on the list and the reader.
        mainWindow.AttachAssistant(this, hub.Services);
    }
}
