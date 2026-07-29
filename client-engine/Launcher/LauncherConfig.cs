using System.IO;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace Launcher;

/// <summary>
/// Config del Launcher (a qué servidor conectarse) — un JSON de texto plano al lado del .exe,
/// para que quien hostee el server para sus amigos edite un solo archivo con la IP/dominio real
/// antes de repartir el Launcher, sin tener que recompilar nada. Si falta el archivo (primera
/// vez, o alguien corrió el .exe suelto), caen los mismos defaults de localhost que ya usa
/// ClientApp (ver Program.cs --server-http/--server-ws) — server local sigue andando sin config.
/// </summary>
public sealed class LauncherConfig
{
    [JsonPropertyName("server_http")] public string ServerHttp { get; set; } = "http://localhost:8080";
    [JsonPropertyName("server_ws")] public string ServerWs { get; set; } = "ws://localhost:8080/ws";

    /// <summary>rom_id -> ruta absoluta que el usuario eligió a mano (OpenFileDialog) para esa
    /// ROM — nunca se guarda ni distribuye el archivo en sí, solo dónde vive en SU equipo.</summary>
    [JsonPropertyName("rom_paths")] public Dictionary<string, string> RomPaths { get; set; } = new();

    /// <summary>"es" o "en" — qué eligió el usuario la última vez (ver Localization.Loc).</summary>
    [JsonPropertyName("language")] public string Language { get; set; } = "es";

    /// <summary>"Recordar usuario": el Launcher nunca ve ni guarda la contraseña (eso lo sigue
    /// pidiendo ClientApp.LoginFlow) — solo precarga este nombre en el campo Usuario para no
    /// tener que tipearlo cada vez.</summary>
    [JsonPropertyName("remember_username")] public bool RememberUsername { get; set; }
    [JsonPropertyName("remembered_username")] public string RememberedUsername { get; set; } = "";

    private static string ConfigPath => Path.Combine(AppContext.BaseDirectory, "launcher-config.json");

    public static LauncherConfig Load()
    {
        if (!File.Exists(ConfigPath)) return new LauncherConfig();
        try
        {
            string json = File.ReadAllText(ConfigPath);
            return JsonSerializer.Deserialize<LauncherConfig>(json) ?? new LauncherConfig();
        }
        catch
        {
            // Un JSON roto no debería impedir arrancar — mismo criterio de "fallar abierto"
            // que el resto del proyecto (ver email.ConsoleSender, ratelimit fail-open).
            return new LauncherConfig();
        }
    }

    /// <summary>Reescribe launcher-config.json completo (server_http/server_ws quedan tal cual
    /// estaban — solo se llama esto para persistir una ROM recién elegida por el usuario).</summary>
    public void Save()
    {
        string json = JsonSerializer.Serialize(this, new JsonSerializerOptions { WriteIndented = true });
        File.WriteAllText(ConfigPath, json);
    }
}
