// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file (docs/windows-port.md §17, MSIX trial): whether the app
// runs with the identity of an MSIX package (windows/msix). The unpackaged
// app, the installer's and every development build, has none; the package
// changes two things, the bridge the app hands out (Paths.InPackage: the
// package's app execution alias) and the notifications' registration (the
// package's manifest gives the name, icon and activator). No GTK or macOS
// counterpart.

using System;
using System.Runtime.InteropServices;

namespace Malachi.Platform.Windows;

/// <summary>The MSIX package identity of this process.</summary>
public static class PackageIdentity
{
    private static readonly Lazy<bool> Packaged = new(Read);

    /// <summary>Whether this process runs with a package identity (read once).</summary>
    public static bool IsPackaged => Packaged.Value;

    // Package.Current throws without an identity: InvalidOperationException,
    // or a COMException with APPMODEL_ERROR_NO_PACKAGE (0x80073D54) where
    // CsWinRT has no mapping for the HRESULT.
    private static bool Read()
    {
        try
        {
            return global::Windows.ApplicationModel.Package.Current.Id is not null;
        }
        catch (InvalidOperationException)
        {
            return false;
        }
        catch (COMException)
        {
            return false;
        }
    }
}
