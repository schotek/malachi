// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only (docs/windows-port.md §6.6): an icon as pixels, so that Core
// and the platform services need no imaging type; the app turns it into a
// WriteableBitmap.

using System;

namespace Malachi.Core.Platform;

/// <summary>An icon: top-down rows of premultiplied BGRA pixels.</summary>
/// <param name="Width">Its width in pixels.</param>
/// <param name="Height">Its height in pixels.</param>
/// <param name="Pixels"><paramref name="Width"/> × <paramref name="Height"/> × 4 bytes.</param>
public sealed record FileIcon(int Width, int Height, ReadOnlyMemory<byte> Pixels);
