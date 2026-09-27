// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only: Attachment Services (IAttachmentExecute,
// CLSID_AttachmentServices) as MarkOfTheWeb uses it, one instance per call,
// on the calling STA thread. The sequence is Microsoft's e-mail client
// sample and Chromium's quarantine_win.cc: SetClientGuid, SetLocalPath,
// SetFileName, SetSource only when told, Save.

using System;
using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.UI.Shell;

namespace Malachi.Platform.Windows.Attachments;

/// <summary>IAttachmentExecute, created per call and released at once.</summary>
internal sealed class AttachmentExecute : IAttachmentServices
{
    /// <inheritdoc/>
    public bool PolicyBlocks(string fileName)
    {
        IAttachmentExecute? services = null;
        try
        {
            services = Create();
            services.SetFileName(fileName);
            // S_OK allows, S_FALSE prompts; any failure disables (blocks).
            services.CheckPolicy();
            return false;
        }
        catch (Exception)
        {
            // Unable to ask is treated like a no.
            return true;
        }
        finally
        {
            Release(services);
        }
    }

    /// <inheritdoc/>
    public int Save(string path, string fileName, string? source)
    {
        IAttachmentExecute? services = null;
        try
        {
            services = Create();
            services.SetLocalPath(path);
            services.SetFileName(fileName);
            if (source is not null)
            {
                services.SetSource(source);
            }
            // May scan the file, may delete it, writes the zone.
            services.Save();
            return 0;
        }
        catch (Exception e)
        {
            // The runtime maps well-known HRESULTs to exception types of
            // their own (E_ACCESSDENIED, E_INVALIDARG, …); each keeps its
            // HRESULT, and a class that cannot be created fails the same way.
            return e.HResult;
        }
        finally
        {
            Release(services);
        }
    }

    private static IAttachmentExecute Create()
    {
        var services = (IAttachmentExecute)new AttachmentServices();
        services.SetClientGuid(in MarkOfTheWeb.ClientGuid);
        return services;
    }

    // Released on the thread that made it, before that thread ends.
    private static void Release(IAttachmentExecute? services)
    {
        if (services is not null)
        {
            Marshal.FinalReleaseComObject(services);
        }
    }
}
