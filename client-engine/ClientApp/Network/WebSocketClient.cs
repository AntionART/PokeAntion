using System.Net.WebSockets;
using System.Text;
using System.Text.Json;
using PokemonOnline.Protocol;

namespace ClientApp.Network;

/// <summary>
/// Cliente WebSocket real contra server/internal/ws (ver common/protocol/PROTOCOL.md).
/// Fase E: conecta el IMemoryAdapter (posición real leída de la ROM, Fase D) al protocolo
/// del servidor — el primer movimiento sincronizado de punta a punta del proyecto.
/// </summary>
public sealed class WebSocketClient : IAsyncDisposable
{
    private readonly ClientWebSocket _ws = new();
    private Task? _receiveLoop;
    private CancellationTokenSource? _cts;

    /// <summary>(type, payload crudo) por cada mensaje recibido del servidor.</summary>
    public event Action<string, JsonElement>? OnMessage;
    /// <summary>Frame binario crudo recibido (hoy: paquetes de voz, ver Router.HandleBinaryMessage
    /// del lado servidor — 36 bytes de character_id ASCII + PCM16 mono).</summary>
    public event Action<byte[]>? OnBinaryMessage;
    public event Action<Exception>? OnError;

    public async Task ConnectAsync(string url)
    {
        await _ws.ConnectAsync(new Uri(url), CancellationToken.None);
        _cts = new CancellationTokenSource();
        _receiveLoop = Task.Run(() => ReceiveLoopAsync(_cts.Token));
    }

    public Task SendAsync(string type, object payload) => SendEnvelopeAsync(type, payload);

    private async Task SendEnvelopeAsync(string type, object payload)
    {
        string json = ProtocolCodec.Encode(type, payload);
        byte[] bytes = Encoding.UTF8.GetBytes(json);
        await _ws.SendAsync(bytes, WebSocketMessageType.Text, endOfMessage: true, CancellationToken.None);
    }

    /// <summary>Manda un frame binario crudo (paquete de voz PCM16) sin pasar por JSON/base64.</summary>
    public Task SendBinaryAsync(byte[] data) =>
        _ws.SendAsync(data, WebSocketMessageType.Binary, endOfMessage: true, CancellationToken.None);

    private async Task ReceiveLoopAsync(CancellationToken ct)
    {
        var buffer = new byte[8192];
        try
        {
            while (_ws.State == WebSocketState.Open && !ct.IsCancellationRequested)
            {
                using var ms = new MemoryStream();
                WebSocketReceiveResult result;
                do
                {
                    result = await _ws.ReceiveAsync(buffer, ct);
                    if (result.MessageType == WebSocketMessageType.Close) return;
                    ms.Write(buffer, 0, result.Count);
                } while (!result.EndOfMessage);

                if (result.MessageType == WebSocketMessageType.Binary)
                {
                    OnBinaryMessage?.Invoke(ms.ToArray());
                    continue;
                }

                ms.Position = 0;
                using var doc = await JsonDocument.ParseAsync(ms, cancellationToken: ct);
                string type = doc.RootElement.GetProperty("type").GetString() ?? "";
                JsonElement payload = doc.RootElement.TryGetProperty("payload", out var p) ? p.Clone() : default;
                OnMessage?.Invoke(type, payload);
            }
        }
        catch (OperationCanceledException) { /* cierre normal */ }
        catch (Exception ex)
        {
            OnError?.Invoke(ex);
        }
    }

    public async ValueTask DisposeAsync()
    {
        _cts?.Cancel();
        if (_ws.State == WebSocketState.Open)
        {
            try { await _ws.CloseAsync(WebSocketCloseStatus.NormalClosure, "bye", CancellationToken.None); }
            catch { /* best-effort */ }
        }
        if (_receiveLoop != null)
        {
            try { await _receiveLoop; } catch { /* ya logueado por OnError */ }
        }
        _ws.Dispose();
    }
}
