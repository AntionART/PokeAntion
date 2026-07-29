using System.Diagnostics;
using System.Net.Http;
using System.Net.Http.Json;
using System.Text.Json.Serialization;

namespace Launcher.Services;

public sealed record ServerStatusInfo(bool Online, int PlayersOnline, TimeSpan? Ping);

public sealed class ServerStatusService
{
    private sealed class ServerStatusResponse
    {
        [JsonPropertyName("status")] public string Status { get; set; } = "";
        [JsonPropertyName("players_online")] public int PlayersOnline { get; set; }
    }

    private readonly HttpClient _http = new() { Timeout = TimeSpan.FromSeconds(5) };

    /// <summary>El ping mostrado es el RTT real de este mismo request HTTP — no hay un ping ICMP
    /// separado, y no hace falta: es exactamente la latencia que le importa al jugador (la que
    /// va a tener el WebSocket del juego contra el mismo host).</summary>
    public async Task<ServerStatusInfo> GetStatusAsync(string serverHttp)
    {
        var stopwatch = Stopwatch.StartNew();
        try
        {
            var response = await _http.GetFromJsonAsync<ServerStatusResponse>($"{serverHttp}/server-status");
            stopwatch.Stop();
            return new ServerStatusInfo(true, response?.PlayersOnline ?? 0, stopwatch.Elapsed);
        }
        catch
        {
            return new ServerStatusInfo(false, 0, null);
        }
    }
}
