// SPDX-FileCopyrightText: 2026 Vladislav Janeček
// SPDX-License-Identifier: GPL-3.0-or-later

// Port of macos/Sources/MalachiMail/App/AppState.swift (AppState) and of
// the composition in AppDelegate.swift's applicationDidFinishLaunching;
// GTK: ui/main.go (the values main threads through window.New: the
// client, the supervisor, the settings, the compose manager). What the
// application owns once, made in this order: the translations (every
// string from here on is translated), the settings over the registry
// (made on the UI thread, whose context their change handlers run on), the
// open directory swept of a previous run's files, the paths and the data
// directory (MALACHI_DATA_DIR), the socket's path check, the daemon's
// supervisor with its process host (MALACHI_DAEMON; the keyring helper
// beside the app) and the transport with the Windows key-file policy, the
// connection, the notification hub, and the shell's services (toasts,
// alerts, windows, hooks). The main window and the Integration follow in
// App.OnLaunched.

using System;
using System.Threading.Tasks;
using Malachi.Core.Controllers;
using Malachi.Core.Daemon;
using Malachi.Core.I18n;
using Malachi.Core.Platform;
using Malachi.Core.Presentation;
using Malachi.Core.Settings;
using Malachi.Core.Transport;
using Malachi.Platform.Windows.Attachments;
using Malachi.Platform.Windows.Consoles;
using Malachi.Platform.Windows.Files;
using Malachi.Platform.Windows.I18n;
using Malachi.Platform.Windows.Launch;
using Malachi.Platform.Windows.Processes;
using Malachi.Platform.Windows.Settings;
using Malachi.Platform.Windows.Transport;
using Microsoft.Extensions.Logging;
using Microsoft.UI.Dispatching;

namespace Malachi.App.Shell;

/// <summary>
/// What the application owns once, handed to every window and controller
/// (Swift AppState). Made and used on the UI thread.
/// </summary>
public sealed partial class AppState : IDisposable
{
    private readonly ILogger logger;
    private readonly RotatingLogFile daemonLog;

    private AppState(
        AppLog logs,
        ConsoleAttachment? console,
        Paths paths,
        ISettingsBackend settingsBackend,
        SettingsStore settings,
        OpenDir openDir,
        DaemonSupervisor supervisor,
        RotatingLogFile daemonLog,
        RpcClient client,
        ConnectionController connection,
        bool startHidden)
    {
        Logs = logs;
        Console = console;
        Paths = paths;
        SettingsBackend = settingsBackend;
        Settings = settings;
        OpenDir = openDir;
        Supervisor = supervisor;
        this.daemonLog = daemonLog;
        Client = client;
        Connection = connection;
        logger = logs.CreateLogger<AppState>();
        Dispatcher = DispatcherQueue.GetForCurrentThread();
        Launcher = new Launcher(new FileTypePolicy());
        Notifications = new NotificationHub(logs.CreateLogger<NotificationHub>());
        Notifications.Attach(connection);
        Toasts = new ToastRouter(logs.CreateLogger<ToastRouter>());
        Hooks = new AppHooks();
        Lifetime = new WindowLifetime(startHidden);
        Windows = new WindowTracker(settings, Hooks, Toasts, Lifetime, logs.CreateLogger<WindowTracker>());
        var alerts = new AlertService(() => MainWindow, ShowMainWindow, OpenUrlAsync, logs.CreateLogger<AlertService>());
        Alerts = alerts;
        Windows.Alerts = alerts;
    }

    /// <summary>The app's log.</summary>
    public AppLog Logs { get; }

    /// <summary>The terminal the app was started from, if any.</summary>
    public ConsoleAttachment? Console { get; }

    /// <summary>Where the socket, the data and the bundled programs are.</summary>
    public Paths Paths { get; }

    /// <summary>The registry key under the settings.</summary>
    public ISettingsBackend SettingsBackend { get; }

    /// <summary>The preferences (the gschema's keys).</summary>
    public SettingsStore Settings { get; }

    /// <summary>Where attachments are written for opening.</summary>
    public OpenDir OpenDir { get; }

    /// <summary>Opens links, files and web pages.</summary>
    public ILauncher Launcher { get; }

    /// <summary>Starts, adopts and stops malachid.</summary>
    public DaemonSupervisor Supervisor { get; }

    /// <summary>The transport.</summary>
    public RpcClient Client { get; }

    /// <summary>The connection to the daemon.</summary>
    public ConnectionController Connection { get; }

    /// <summary>The daemon's notifications and the connection state, fanned out.</summary>
    public NotificationHub Notifications { get; }

    /// <summary>The application's toasts.</summary>
    public ToastRouter Toasts { get; }

    /// <summary>The dialogs.</summary>
    public IAlerts Alerts { get; }

    /// <summary>The app's windows.</summary>
    public WindowTracker Windows { get; }

    /// <summary>When closing a window ends the app.</summary>
    public WindowLifetime Lifetime { get; }

    /// <summary>The entry points the parts of the app register.</summary>
    public AppHooks Hooks { get; }

    /// <summary>The UI thread.</summary>
    public DispatcherQueue Dispatcher { get; }

    /// <summary>The main window, once made.</summary>
    public MainWindow? MainWindow { get; internal set; }

    /// <summary>The wiring of the controllers, once made.</summary>
    public Integration? Integration { get; internal set; }

    /// <summary>Quit (app.quit), set by the app.</summary>
    public Func<QuitReason, Task<bool>>? QuitHandler { get; internal set; }

    /// <summary>
    /// Whether the main window is the active window (notify.go: no desktop
    /// notification then): shown, and neither another window of the app nor
    /// another application has the activation.
    /// </summary>
    public bool IsMainWindowActive =>
        MainWindow is { } w && w.AppWindow.IsVisible && Windows.IsActive(w);

    /// <summary>
    /// Makes the application's objects (see the file's header), on the UI
    /// thread. <paramref name="startHidden"/> is a background start's hold.
    /// </summary>
    public static AppState Create(AppLog logs, ConsoleAttachment? console, Paths paths, bool startHidden)
    {
        ArgumentNullException.ThrowIfNull(logs);
        ArgumentNullException.ThrowIfNull(paths);
        var log = logs.CreateLogger<AppState>();

        // Translations first: every string built from here on is translated.
        L10n.Catalogue = Catalogue.Default(preferredLanguages: WindowsPreferredLanguages.Instance, logger: log);

        var backend = RegistrySettingsBackend.Open(logger: logs.CreateLogger<RegistrySettingsBackend>());
        var settings = new SettingsStore(backend, System.Threading.SynchronizationContext.Current);

        var directories = new PrivateDirectory();
        // Attachments a previous run wrote for opening (docs/security.md §8).
        var openDir = OpenDir.InDataDirectory(paths.DataDir, directories, TimeProvider.System, AnsiLookAlikes.OfThisMachine);
        openDir.RemoveAll();
        try
        {
            paths.EnsureDirectories(directories);
        }
        catch (Exception e) when (e is System.IO.IOException or UnauthorizedAccessException or InvalidOperationException or System.ComponentModel.Win32Exception)
        {
            LogDataDirectory(log, e);
        }
        try
        {
            Paths.CheckSocket(paths.Socket);
        }
        catch (Core.Daemon.SocketPathTooLongException e)
        {
            LogSocketPath(log, e.Message);
        }

        // The daemon is ours to run: nothing on the desktop starts malachid.
        // One that already answers on the socket is used and left alone.
        DaemonLaunch? launch = null;
        try
        {
            if (DaemonSupervisor.Locate() is { } exe)
            {
                launch = new DaemonLaunch
                {
                    Executable = exe,
                    Socket = paths.Socket,
                    Config = paths.Config,
                    Store = paths.Store,
                    KeyringHelper = paths.KeyringHelper,
                };
            }
        }
        catch (DaemonSupervisorException e)
        {
            LogNoDaemon(log, e.Message);
        }
        var daemonLog = new RotatingLogFile(paths.DaemonLog);
        var supervisor = new DaemonSupervisor(launch, paths.Socket, new DaemonProcessHost(console, daemonLog))
        {
            Logger = logs.CreateLogger<DaemonSupervisor>(),
            BeforeStart = () => paths.EnsureSocketDirectory(directories),
        };
        var client = new RpcClient(paths.Socket, new WindowsKeyFilePolicy(), logger: logs.CreateLogger<RpcClient>());
        var connection = new ConnectionController(client, supervisor, logger: logs.CreateLogger<ConnectionController>());
        return new AppState(logs, console, paths, backend, settings, openDir, supervisor, daemonLog, client, connection, startHidden);
    }

    /// <summary>
    /// Presents the main window (a hidden one comes back: Run in
    /// Background) and brings it to the front, like the GTK app.show action.
    /// </summary>
    public void ShowMainWindow()
    {
        if (MainWindow is not { } w)
        {
            return;
        }
        w.PrepareShow();
        WindowPresenter.Present(w);
        Lifetime.MainWindowShown();
    }

    /// <summary>Quits the application (app.quit).</summary>
    public void Quit() => _ = QuitHandler?.Invoke(QuitReason.User);

    /// <summary>Opens a web page (https only) in the browser, over the active window.</summary>
    public async Task OpenUrlAsync(string url)
    {
        var owner = Windows.Active?.Window is { } w ? WindowPresenter.Handle(w) : 0;
        if (!await Launcher.OpenUrlAsync(url, owner))
        {
            LogOpenUrlFailed(logger);
        }
    }

    /// <summary>
    /// Lets go of what the app holds, after the daemon was stopped: the
    /// attachments written for opening, the settings, the logs.
    /// </summary>
    public void Dispose()
    {
        Integration?.Dispose();
        Windows.Dispose();
        Notifications.Dispose();
        Connection.Dispose();
        Client.Dispose();
        Supervisor.Dispose();
        OpenDir.RemoveAll();
        Settings.Dispose();
        SettingsBackend.Dispose();
        daemonLog.Dispose();
    }

    [LoggerMessage(Level = LogLevel.Error, Message = "data directory")]
    private static partial void LogDataDirectory(ILogger logger, Exception error);

    [LoggerMessage(Level = LogLevel.Error, Message = "socket path: {Reason}")]
    private static partial void LogSocketPath(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "not starting malachid; expecting one to be started by other means: {Reason}")]
    private static partial void LogNoDaemon(ILogger logger, string reason);

    [LoggerMessage(Level = LogLevel.Warning, Message = "a web page could not be opened")]
    private static partial void LogOpenUrlFailed(ILogger logger);
}
