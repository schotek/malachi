// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// The canary host and every process it starts afterwards (the WebView2
// browser, its renderers, the GPU and utility processes) run in a job whose
// priority class is normal. Chromium starts the renderer that shows a page
// again after its renderer died at idle priority, and raises it only once
// the page has committed; while the other test assemblies keep every core
// busy, an idle process gets no CPU at all (measured: none in 17 s), so the
// view's reload after a crash, and a load right after one, stalled until
// Chromium's 30 s commit timeout called the renderer unresponsive, and the
// recovery run failed or ran out of time. What the canary tests (the
// network, the request gate, the recovery's decisions) does not depend on
// how Chromium schedules its processes. A host that cannot set the job up
// runs anyway and says so in its progress.

using System.Runtime.InteropServices;
using Windows.Win32;
using Windows.Win32.Foundation;
using Windows.Win32.System.JobObjects;

namespace Malachi.App.Canary.Host;

/// <summary>Keeps the host's processes at normal priority.</summary>
internal static class ProcessPriority
{
    // NORMAL_PRIORITY_CLASS.
    private const uint Normal = 0x20;

    /// <summary>
    /// Puts this process, and every process it starts from now on, in a job
    /// that keeps them at normal priority; null when that is done, otherwise
    /// why not. The job lives on without its handle for as long as a
    /// process is in it.
    /// </summary>
    public static unsafe string? PinNormal()
    {
        using var job = PInvoke.CreateJobObject(null, null);
        if (job.IsInvalid)
        {
            return "CreateJobObject failed: " + Marshal.GetLastPInvokeErrorMessage();
        }
        var limits = new JOBOBJECT_BASIC_LIMIT_INFORMATION
        {
            LimitFlags = JOB_OBJECT_LIMIT.JOB_OBJECT_LIMIT_PRIORITY_CLASS,
            PriorityClass = Normal,
        };
        if (!PInvoke.SetInformationJobObject((HANDLE)job.DangerousGetHandle(), JOBOBJECTINFOCLASS.JobObjectBasicLimitInformation,
            &limits, (uint)sizeof(JOBOBJECT_BASIC_LIMIT_INFORMATION)))
        {
            return "SetInformationJobObject failed: " + Marshal.GetLastPInvokeErrorMessage();
        }
        if (!PInvoke.AssignProcessToJobObject(job, PInvoke.GetCurrentProcess_SafeHandle()))
        {
            return "AssignProcessToJobObject failed: " + Marshal.GetLastPInvokeErrorMessage();
        }
        return null;
    }
}
