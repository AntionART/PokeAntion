using System.Collections.ObjectModel;
using System.ComponentModel;
using System.IO;
using System.Linq;
using System.Runtime.CompilerServices;
using System.Windows.Threading;
using Launcher.Localization;
using Launcher.Services;

namespace Launcher.ViewModels;

public sealed class MainWindowViewModel : INotifyPropertyChanged
{
    private readonly LauncherConfig _config;
    public Loc Loc { get; } = new();
    public AppLanguage[] AvailableLanguages { get; } = [AppLanguage.Es, AppLanguage.En];

    public AppLanguage SelectedLanguage
    {
        get => Loc.Language;
        set
        {
            if (Loc.Language == value) return;
            Loc.Language = value;
            _config.Language = value == AppLanguage.En ? "en" : "es";
            _config.Save();
        }
    }

    private readonly ClientUpdater _updater = new();
    private readonly ServerStatusService _serverStatusService = new();
    private readonly NewsService _newsService = new();
    private List<RomCatalogEntry>? _romCatalog;
    private RomStatus? _selectedRom;

    /// <summary>Refresca estado del servidor/ping cada 15s mientras la pantalla principal está
    /// esperando (no durante la descarga/lanzamiento, ver el chequeo de IsBusy en el Tick) — un
    /// launcher real (PokeMMO, Battle.net) nunca muestra un ping/jugadores-conectados congelado
    /// desde el instante en que abriste la ventana.</summary>
    private readonly DispatcherTimer _statusTimer = new() { Interval = TimeSpan.FromSeconds(15) };

    public event PropertyChangedEventHandler? PropertyChanged;
    /// <summary>El launcher se cierra solo al terminar (misma UX que el WinForms viejo) —
    /// la Window se suscribe a esto en vez de que el VM conozca su propia vista.</summary>
    public event Action? RequestClose;

    private string _status = "Iniciando...";
    public string Status { get => _status; private set => Set(ref _status, value); }

    private bool _isIndeterminate = true;
    public bool IsIndeterminate { get => _isIndeterminate; private set => Set(ref _isIndeterminate, value); }

    private double _progressValue;
    public double ProgressValue { get => _progressValue; private set => Set(ref _progressValue, value); }

    private string _percentText = "";
    public string PercentText { get => _percentText; private set => Set(ref _percentText, value); }

    private string _speedText = "";
    public string SpeedText { get => _speedText; private set => Set(ref _speedText, value); }

    private string _etaText = "";
    public string EtaText { get => _etaText; private set => Set(ref _etaText, value); }

    private string _versionText = "";
    public string VersionText { get => _versionText; private set => Set(ref _versionText, value); }

    private bool _hasError;
    public bool HasError { get => _hasError; private set => Set(ref _hasError, value); }

    private string _errorMessage = "";
    public string ErrorMessage { get => _errorMessage; private set => Set(ref _errorMessage, value); }

    private bool _needsRom;
    /// <summary>True cuando ninguna ROM soportada se pudo ubicar/validar en este equipo — la
    /// vista reemplaza el cuerpo entero por la pantalla de onboarding "Seleccionar ROM" en vez
    /// de arrancar el flujo de actualización (no tiene sentido buscar updates de un cliente que
    /// no va a poder arrancar igual).</summary>
    public bool NeedsRom { get => _needsRom; private set => Set(ref _needsRom, value); }

    private string _romMessage = "";
    public string RomMessage { get => _romMessage; private set => Set(ref _romMessage, value); }

    /// <summary>Pantalla principal (server status + noticias + botón Jugar) vs. pantalla de
    /// progreso (buscando update / descargando / iniciando) — el launcher ya no arranca solo:
    /// se queda esperando en la principal hasta que el jugador aprieta "Jugar".</summary>
    private bool _isBusy;
    public bool IsBusy { get => _isBusy; private set => Set(ref _isBusy, value); }

    private bool _serverOnline;
    public bool ServerOnline { get => _serverOnline; private set => Set(ref _serverOnline, value); }

    private string _pingText = "";
    public string PingText { get => _pingText; private set => Set(ref _pingText, value); }

    private string _playersOnlineText = "";
    public string PlayersOnlineText { get => _playersOnlineText; private set => Set(ref _playersOnlineText, value); }

    public ObservableCollection<NewsItem> NewsItems { get; } = new();

    private string _username = "";
    /// <summary>Solo precarga el campo Usuario de ClientApp.LoginFlow — el launcher nunca ve ni
    /// guarda contraseñas. Se persiste únicamente si <see cref="RememberUsername"/> está activo.</summary>
    public string Username
    {
        get => _username;
        set
        {
            if (!Set(ref _username, value)) return;
            if (_config.RememberUsername) { _config.RememberedUsername = value; _config.Save(); }
        }
    }

    private bool _rememberUsername;
    public bool RememberUsername
    {
        get => _rememberUsername;
        set
        {
            if (!Set(ref _rememberUsername, value)) return;
            _config.RememberUsername = value;
            _config.RememberedUsername = value ? Username : "";
            _config.Save();
        }
    }

    public RelayCommand RetryCommand { get; }
    public RelayCommand ExitCommand { get; }
    public RelayCommand BrowseRomCommand { get; }
    public RelayCommand PlayCommand { get; }

    public MainWindowViewModel()
    {
        _config = LauncherConfig.Load();
        Loc.Language = _config.Language == "en" ? AppLanguage.En : AppLanguage.Es;
        _rememberUsername = _config.RememberUsername;
        _username = _config.RememberedUsername;
        RetryCommand = new RelayCommand(PlayAsync);
        ExitCommand = new RelayCommand(() => { Environment.Exit(0); return Task.CompletedTask; });
        BrowseRomCommand = new RelayCommand(BrowseForRomAsync);
        PlayCommand = new RelayCommand(PlayAsync);
        _statusTimer.Tick += async (_, _) => await RefreshServerStatusAsync();
    }

    /// <summary>Corre una sola vez al abrir la ventana: resuelve la ROM (o pide elegirla) y
    /// carga server status + noticias — todo lo que el jugador debería ver ANTES de decidir
    /// apretar "Jugar", no después.</summary>
    public async Task InitializeAsync()
    {
        if (!TryResolveRom(out string romProblem))
        {
            NeedsRom = true;
            RomMessage = romProblem;
            return;
        }
        NeedsRom = false;

        var statusTask = _serverStatusService.GetStatusAsync(_config.ServerHttp);
        var newsTask = _newsService.GetNewsAsync(_config.ServerHttp);
        await Task.WhenAll(statusTask, newsTask);

        ApplyServerStatus(statusTask.Result);
        NewsItems.Clear();
        foreach (var item in newsTask.Result) NewsItems.Add(item);

        _statusTimer.Start();
    }

    private async Task RefreshServerStatusAsync()
    {
        // No pisar la pantalla de progreso con un fetch de background, y no tiene sentido
        // seguir midiendo ping de un servidor mientras ya estás jugando (el proceso está a punto
        // de cerrarse de cualquier forma, ver PlayAsync/RequestClose).
        if (IsBusy || NeedsRom) return;
        ApplyServerStatus(await _serverStatusService.GetStatusAsync(_config.ServerHttp));
    }

    private void ApplyServerStatus(ServerStatusInfo info)
    {
        ServerOnline = info.Online;
        PlayersOnlineText = info.Online ? $"{info.PlayersOnline} jugador(es) conectado(s)" : "Servidor sin conexión";
        PingText = info.Ping is { } ping ? $"{(int)ping.TotalMilliseconds} ms" : "";
    }

    public async Task PlayAsync()
    {
        HasError = false;
        IsBusy = true;
        _statusTimer.Stop();
        try
        {
            if (!TryResolveRom(out string romProblem))
            {
                IsBusy = false;
                NeedsRom = true;
                RomMessage = romProblem;
                return;
            }

            SetStatus("Buscando actualizaciones...", indeterminate: true);
            ClientVersionInfo remote;
            try
            {
                remote = await _updater.FetchRemoteVersionAsync(_config.ServerHttp);
            }
            catch (Exception ex)
            {
                await HandleServerUnreachableAsync(ex);
                return;
            }

            string? local = _updater.GetLocalVersion();
            VersionText = $"v{remote.Version}  ·  {_config.ServerHttp}";

            if (local != remote.Version)
            {
                SetStatus($"Descargando versión {remote.Version}...", indeterminate: false);
                var progress = new Progress<DownloadProgress>(ReportDownloadProgress);
                await _updater.DownloadAndInstallAsync(remote, progress);
            }

            SetStatus("Iniciando Pokémon Online...", indeterminate: true);
            LaunchClientWithResolvedRom();
            await Task.Delay(500);
            RequestClose?.Invoke();
        }
        catch (Exception ex)
        {
            ShowError(ex.Message);
        }
    }

    /// <summary>Servidor inalcanzable: si ya hay un cliente instalado localmente, jugar offline
    /// contra ese server es mejor que bloquear con un error (ver mismo criterio en el
    /// LauncherForm anterior) — sin instalación previa no hay nada que lanzar, ahí sí es error.</summary>
    private async Task HandleServerUnreachableAsync(Exception ex)
    {
        if (_updater.GetLocalVersion() is not null)
        {
            SetStatus("No se pudo contactar al servidor — iniciando con la versión instalada...", indeterminate: true);
            await Task.Delay(1200);
            LaunchClientWithResolvedRom();
            RequestClose?.Invoke();
            return;
        }

        ShowError($"No se pudo contactar al servidor ({_config.ServerHttp}) y todavía no hay ningún cliente instalado.\n\nDetalle: {ex.Message}");
    }

    private void LaunchClientWithResolvedRom()
    {
        var rom = _selectedRom ?? throw new InvalidOperationException("LaunchClient llamado sin una ROM resuelta.");
        string? username = RememberUsername && Username.Length > 0 ? Username : null;
        _updater.LaunchClient(_config.ServerHttp, _config.ServerWs, rom.ResolvedPath!, rom.Catalog.MemoryMapPath, rom.Catalog.RomId, username);
    }

    /// <summary>Arma el catálogo (una sola vez) y busca la primera entrada que resuelva a un
    /// archivo válido — ruta ya guardada por el usuario, o la ruta relativa al repo por
    /// comodidad de desarrollo. Nunca descarga ni sugiere de dónde conseguir una ROM.</summary>
    private bool TryResolveRom(out string problem)
    {
        if (_selectedRom is { IsValid: true })
        {
            problem = "";
            return true;
        }

        string repoRoot = RomCatalogService.FindRepoRoot(AppContext.BaseDirectory);
        _romCatalog ??= RomCatalogService.Discover(Path.Combine(repoRoot, "memory-maps"), repoRoot);

        if (_romCatalog.Count == 0)
        {
            problem = "Este launcher no tiene ninguna ROM soportada configurada (memory-maps/ está vacío).";
            return false;
        }

        foreach (var entry in _romCatalog)
        {
            var status = RomLocatorService.Resolve(entry, _config.RomPaths.GetValueOrDefault(entry.RomId));
            if (status.IsValid)
            {
                _selectedRom = status;
                problem = "";
                return true;
            }
        }

        problem = "No se detectó ninguna ROM compatible en este equipo. Seleccioná el archivo de tu propia ROM de Pokémon Esmeralda (no la proveemos ni la distribuimos).";
        return false;
    }

    private Task BrowseForRomAsync()
    {
        var dialog = new Microsoft.Win32.OpenFileDialog
        {
            Title = "Seleccionar ROM de Pokémon Esmeralda",
            Filter = "ROM de Game Boy Advance (*.gba)|*.gba|Todos los archivos (*.*)|*.*",
        };
        if (dialog.ShowDialog() != true) return Task.CompletedTask;

        string chosenPath = dialog.FileName;
        string actualHash = RomLocatorService.ComputeSha1(chosenPath);
        var match = _romCatalog!.FirstOrDefault(e => e.ExpectedSha1 is { Length: > 0 } expected
            && string.Equals(expected, actualHash, StringComparison.OrdinalIgnoreCase));

        if (match == null)
        {
            RomMessage = "El archivo seleccionado no coincide con ninguna ROM compatible conocida (el hash SHA1 no coincide) — verificá que sea una copia sin modificar de Pokémon Esmeralda.";
            return Task.CompletedTask;
        }

        _config.RomPaths[match.RomId] = chosenPath;
        _config.Save();
        _selectedRom = new RomStatus(match, chosenPath, true, null);
        return InitializeAsync();
    }

    private void ReportDownloadProgress(DownloadProgress p)
    {
        if (p.TotalBytes is > 0)
        {
            IsIndeterminate = false;
            ProgressValue = Math.Clamp(p.BytesReceived * 100.0 / p.TotalBytes.Value, 0, 100);
            PercentText = $"{ProgressValue:0}%";

            double bytesPerSecond = p.Elapsed.TotalSeconds > 0.1 ? p.BytesReceived / p.Elapsed.TotalSeconds : 0;
            SpeedText = FormatSpeed(bytesPerSecond);

            long remainingBytes = p.TotalBytes.Value - p.BytesReceived;
            EtaText = bytesPerSecond > 0 ? FormatEta(TimeSpan.FromSeconds(remainingBytes / bytesPerSecond)) : "";
        }
        else
        {
            IsIndeterminate = true;
            PercentText = "";
            SpeedText = "";
            EtaText = "";
        }
    }

    private static string FormatSpeed(double bytesPerSecond) => bytesPerSecond switch
    {
        >= 1024 * 1024 => $"{bytesPerSecond / (1024 * 1024):0.0} MB/s",
        >= 1024 => $"{bytesPerSecond / 1024:0.0} KB/s",
        _ => $"{bytesPerSecond:0} B/s",
    };

    private static string FormatEta(TimeSpan remaining) =>
        remaining.TotalHours >= 1 ? $"{(int)remaining.TotalHours:0}h {remaining.Minutes:00}m restantes" : $"{remaining.Minutes:0}:{remaining.Seconds:00} restantes";

    private void SetStatus(string text, bool indeterminate)
    {
        Status = text;
        IsIndeterminate = indeterminate;
        if (indeterminate) { ProgressValue = 0; PercentText = ""; SpeedText = ""; EtaText = ""; }
    }

    private void ShowError(string message)
    {
        ErrorMessage = message;
        HasError = true;
    }

    private bool Set<T>(ref T field, T value, [CallerMemberName] string? propertyName = null)
    {
        if (EqualityComparer<T>.Default.Equals(field, value)) return false;
        field = value;
        PropertyChanged?.Invoke(this, new PropertyChangedEventArgs(propertyName));
        return true;
    }
}
