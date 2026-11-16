// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Windows-only file: IStartupTask over Windows.ApplicationModel.StartupTask
// for the task the MSIX package's manifest declares. The WinRT calls are
// asynchronous; they run on a thread-pool thread and are waited for, so
// that the caller on the UI thread (an STA) never waits for a completion
// that needs its own message loop. A desktop app's request to enable shows
// no dialog (only a UWP app's does), so nothing needs the UI thread.
// Whatever WinRT throws becomes an IOException, which the callers of
// ILaunchAtLogin show.

using System;
using System.IO;
using System.Threading.Tasks;
using WinRt = Windows.ApplicationModel;

namespace Malachi.Platform.Windows.Startup;

/// <summary>The startup task of this process's MSIX package.</summary>
public sealed class PackageStartupTask : IStartupTask
{
    /// <summary>The TaskId of the manifest's <c>uap5:StartupTask</c>.</summary>
    public const string DefaultTaskId = "MalachiMailStartup";

    private readonly string taskId;

    /// <summary>The task <paramref name="taskId"/> of this package.</summary>
    public PackageStartupTask(string taskId = DefaultTaskId)
    {
        ArgumentException.ThrowIfNullOrEmpty(taskId);
        this.taskId = taskId;
    }

    /// <inheritdoc/>
    public StartupTaskStatus State => Run(async () => Of((await WinRt.StartupTask.GetAsync(taskId)).State));

    /// <inheritdoc/>
    public StartupTaskStatus RequestEnable() =>
        Run(async () => Of(await (await WinRt.StartupTask.GetAsync(taskId)).RequestEnableAsync()));

    /// <inheritdoc/>
    public void Disable() =>
        Run(async () =>
        {
            (await WinRt.StartupTask.GetAsync(taskId)).Disable();
            return StartupTaskStatus.Disabled;
        });

    private static StartupTaskStatus Of(WinRt.StartupTaskState state) => state switch
    {
        WinRt.StartupTaskState.Enabled => StartupTaskStatus.Enabled,
        WinRt.StartupTaskState.DisabledByUser => StartupTaskStatus.DisabledByUser,
        WinRt.StartupTaskState.DisabledByPolicy => StartupTaskStatus.DisabledByPolicy,
        WinRt.StartupTaskState.EnabledByPolicy => StartupTaskStatus.EnabledByPolicy,
        _ => StartupTaskStatus.Disabled,
    };

    private static StartupTaskStatus Run(Func<Task<StartupTaskStatus>> call)
    {
        try
        {
            return Task.Run(call).GetAwaiter().GetResult();
        }
        catch (Exception e) when (e is not (UnauthorizedAccessException or IOException or OutOfMemoryException))
        {
            // No package, no such task in the manifest, or the broker failed.
            throw new IOException(e.Message, e);
        }
    }
}
