using System.Diagnostics;
using System.IO;
using System.IO.Compression;
using System.Net.Http;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Launcher;

/// <summary>Snapshot de una descarga en curso — el ViewModel deriva %, velocidad y ETA de esto
/// en vez de que <see cref="ClientUpdater"/> los calcule (una sola fuente de tiempo real: el
/// Stopwatch que ya lleva el propio loop de lectura).</summary>
public readonly record struct DownloadProgress(long BytesReceived, long? TotalBytes, TimeSpan Elapsed);

/// <summary>Respuesta real de GET {server_http}/client-version — ver handleClientVersion en
/// server/cmd/server/main.go, es el único contrato entre este Launcher y el servidor.</summary>
public sealed class ClientVersionInfo
{
    [JsonPropertyName("version")] public string Version { get; set; } = "";
    [JsonPropertyName("download_url")] public string DownloadUrl { get; set; } = "";
}

/// <summary>
/// Instala/actualiza el bundle de cliente (ClientApp.exe self-contained + memory-maps/ +
/// data/pokemon/ + sprites de batalla — ver scripts/build-client-bundle.ps1 para qué entra
/// exactamente) en una carpeta "client/" al lado del propio Launcher.exe, y sabe lanzar el
/// ClientApp.exe resultante. No hace diffing/parches — cada actualización pisa la carpeta
/// entera con el .zip nuevo, mismo criterio "simple y real" que el resto del proyecto (esto
/// corre para un puñado de amigos, no una distribución masiva que necesite bajar solo deltas).
/// </summary>
public sealed class ClientUpdater
{
    private readonly HttpClient _http = new();

    public string InstallDir { get; } = Path.Combine(AppContext.BaseDirectory, "client");
    private string LocalVersionPath => Path.Combine(InstallDir, "VERSION");

    /// <summary>Ruta al .exe dentro del bundle instalado — mantiene la MISMA estructura
    /// relativa que tiene el repo (client-engine/ClientApp/bin/publish-win-x64/), así
    /// ClientApp.exe sigue encontrando memory-maps/ y data/pokemon/ como hermanos vía su propio
    /// FindRepoRoot (ver Program.cs) sin ningún cambio ahí.</summary>
    public string ClientExePath => Path.Combine(
        InstallDir, "client-engine", "ClientApp", "bin", "publish-win-x64", "ClientApp.exe");

    public string? GetLocalVersion() =>
        File.Exists(LocalVersionPath) ? File.ReadAllText(LocalVersionPath).Trim() : null;

    public async Task<ClientVersionInfo> FetchRemoteVersionAsync(string serverHttp)
    {
        using var response = await _http.GetAsync($"{serverHttp}/client-version");
        response.EnsureSuccessStatusCode();
        string json = await response.Content.ReadAsStringAsync();
        return JsonSerializer.Deserialize<ClientVersionInfo>(json)
            ?? throw new InvalidDataException("El servidor devolvió un client-version vacío o inválido.");
    }

    /// <summary>Descarga el .zip a un archivo temporal, lo extrae pisando InstallDir, y deja
    /// escrita la nueva versión — <paramref name="onProgress"/> recibe 0-100 (null si el
    /// servidor no manda Content-Length, ej. detrás de un proxy que hace chunked encoding).</summary>
    public async Task DownloadAndInstallAsync(ClientVersionInfo info, IProgress<DownloadProgress> onProgress)
    {
        string tempZip = Path.Combine(Path.GetTempPath(), $"pokemon-online-client-{Guid.NewGuid():N}.zip");
        try
        {
            using (var response = await _http.GetAsync(info.DownloadUrl, HttpCompletionOption.ResponseHeadersRead))
            {
                response.EnsureSuccessStatusCode();
                long? totalBytes = response.Content.Headers.ContentLength;
                await using var httpStream = await response.Content.ReadAsStreamAsync();
                await using var fileStream = new FileStream(tempZip, FileMode.Create, FileAccess.Write);

                var buffer = new byte[81920];
                long readSoFar = 0;
                int read;
                var stopwatch = Stopwatch.StartNew();
                while ((read = await httpStream.ReadAsync(buffer)) > 0)
                {
                    await fileStream.WriteAsync(buffer.AsMemory(0, read));
                    readSoFar += read;
                    onProgress.Report(new DownloadProgress(readSoFar, totalBytes, stopwatch.Elapsed));
                }
            }

            if (Directory.Exists(InstallDir))
                Directory.Delete(InstallDir, recursive: true);
            Directory.CreateDirectory(InstallDir);
            ZipFile.ExtractToDirectory(tempZip, InstallDir);

            File.WriteAllText(LocalVersionPath, info.Version);
        }
        finally
        {
            if (File.Exists(tempZip)) File.Delete(tempZip);
        }
    }

    /// <summary>La ROM ya fue detectada/validada acá en el Launcher (RomLocatorService) — se le
    /// pasa la ruta resuelta al cliente por argumento para que este no tenga que descubrir ni
    /// elegir ninguna ROM por su cuenta ("el cliente únicamente ejecuta el juego").</summary>
    public void LaunchClient(string serverHttp, string serverWs, string romPath, string memoryMapPath, string romId, string? rememberedUsername = null)
    {
        if (!File.Exists(ClientExePath))
            throw new FileNotFoundException($"No se encontró ClientApp.exe en el bundle instalado: {ClientExePath}");

        var startInfo = new System.Diagnostics.ProcessStartInfo
        {
            FileName = ClientExePath,
            WorkingDirectory = Path.GetDirectoryName(ClientExePath),
            UseShellExecute = false,
        };
        // ArgumentList (no un solo string "Arguments") porque la ruta de la ROM puede traer
        // espacios/paréntesis (nombres reales de dumps, ej. "Pokemon - Edicion Esmeralda
        // (Spain).gba") — concatenar a mano requeriría escapar comillas correctamente.
        startInfo.ArgumentList.Add("--server-http"); startInfo.ArgumentList.Add(serverHttp);
        startInfo.ArgumentList.Add("--server-ws"); startInfo.ArgumentList.Add(serverWs);
        startInfo.ArgumentList.Add("--rom"); startInfo.ArgumentList.Add(romPath);
        if (!string.IsNullOrEmpty(rememberedUsername))
        {
            // Solo precarga el campo Usuario de LoginFlow — nunca viaja ninguna contraseña acá.
            startInfo.ArgumentList.Add("--username"); startInfo.ArgumentList.Add(rememberedUsername);
        }
        startInfo.ArgumentList.Add("--memory-map"); startInfo.ArgumentList.Add(memoryMapPath);
        startInfo.ArgumentList.Add("--rom-id"); startInfo.ArgumentList.Add(romId);

        System.Diagnostics.Process.Start(startInfo);
    }
}
