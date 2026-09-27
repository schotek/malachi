// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §11.5): the Win32 virtual-key
// codes of the shortcuts, which WinUI's VirtualKey and a WM_KEYDOWN's
// wParam share. Letters and digits are their ASCII capitals, on every
// layout's key that types them.

namespace Malachi.Core.Presentation;

/// <summary>The virtual-key codes the shortcuts use (winuser.h <c>VK_*</c>).</summary>
public static class VirtualKey
{
    /// <summary>VK_RETURN (Enter).</summary>
    public const int Enter = 0x0D;

    /// <summary>VK_ESCAPE.</summary>
    public const int Escape = 0x1B;

    /// <summary>VK_UP.</summary>
    public const int Up = 0x26;

    /// <summary>VK_DOWN.</summary>
    public const int Down = 0x28;

    /// <summary>VK_DELETE.</summary>
    public const int Delete = 0x2E;

    /// <summary>'A'.</summary>
    public const int A = 0x41;

    /// <summary>'E'.</summary>
    public const int E = 0x45;

    /// <summary>'F'.</summary>
    public const int F = 0x46;

    /// <summary>'G'.</summary>
    public const int G = 0x47;

    /// <summary>'I'.</summary>
    public const int I = 0x49;

    /// <summary>'J'.</summary>
    public const int J = 0x4A;

    /// <summary>'N'.</summary>
    public const int N = 0x4E;

    /// <summary>'P'.</summary>
    public const int P = 0x50;

    /// <summary>'Q'.</summary>
    public const int Q = 0x51;

    /// <summary>'R'.</summary>
    public const int R = 0x52;

    /// <summary>'S'.</summary>
    public const int S = 0x53;

    /// <summary>'U'.</summary>
    public const int U = 0x55;

    /// <summary>'W'.</summary>
    public const int W = 0x57;

    /// <summary>VK_F3.</summary>
    public const int F3 = 0x72;

    /// <summary>VK_F5.</summary>
    public const int F5 = 0x74;

    /// <summary>VK_F7.</summary>
    public const int F7 = 0x76;

    /// <summary>VK_F12.</summary>
    public const int F12 = 0x7B;

    /// <summary>VK_OEM_COMMA, the key that types a comma on every layout.</summary>
    public const int Comma = 0xBC;
}
