// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The parameters of the DevTools protocol's Input.dispatchDragEvent: a file
// dragged onto the editor, as the WebView2 spike (E1) dropped one.

namespace Malachi.App.Canary.Host;

/// <summary>Input.dispatchDragEvent.</summary>
/// <param name="Type">dragEnter, dragOver or drop.</param>
/// <param name="X">Where, in CSS pixels.</param>
/// <param name="Y">Where, in CSS pixels.</param>
/// <param name="Data">What is dragged.</param>
internal sealed record DragInput(string Type, double X, double Y, DragData Data);
