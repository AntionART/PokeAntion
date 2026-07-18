using NAudio.Wave;
using ClientApp.Network;

namespace ClientApp;

/// <summary>
/// Fase F: chat de voz. Captura de micrófono con push-to-talk y reproducción de voz remota,
/// sobre los frames binarios crudos del WebSocket (ver Router.HandleBinaryMessage / Hub.
/// BroadcastBinaryToMap del lado servidor: 36 bytes ASCII de character_id + PCM16 mono).
///
/// PCM16 mono a 16kHz: 32000 bytes/seg, de sobra para voz y liviano para el relay.
/// </summary>
public sealed class VoiceChat : IDisposable
{
    private const int CharacterIdLen = 36; // debe matchear characterIDLen en router.go
    private static readonly WaveFormat Format = new(16000, 16, 1);

    private readonly WebSocketClient _ws;
    private readonly WaveInEvent? _waveIn;
    private readonly Dictionary<string, (BufferedWaveProvider Provider, WaveOutEvent Output)> _playback = new();
    private readonly object _playbackLock = new();
    private bool _talking;

    /// <summary>False si no hay micrófono disponible en esta máquina (entorno sin hardware
    /// de audio, ej. algunos hosts RDP/servidor): la captura queda deshabilitada pero la
    /// reproducción de voz remota sigue funcionando igual.</summary>
    public bool CaptureAvailable { get; }

    /// <summary>Se pone en false si el primer intento de abrir un dispositivo de salida falla
    /// (entorno sin hardware de audio, ej. algunos hosts RDP/servidor): a partir de ahí se
    /// descarta el audio remoto en vez de seguir intentando reproducirlo. NAudio no expone un
    /// DeviceCount estático para WaveOutEvent (solo para WaveInEvent), así que esto se
    /// descubre recién al intentar abrir el device, no de antemano.</summary>
    public bool PlaybackAvailable { get; private set; } = true;

    public VoiceChat(WebSocketClient ws)
    {
        _ws = ws;
        CaptureAvailable = WaveInEvent.DeviceCount > 0;

        if (CaptureAvailable)
        {
            _waveIn = new WaveInEvent { WaveFormat = Format, BufferMilliseconds = 50 };
            _waveIn.DataAvailable += OnMicDataAvailable;
        }

        ws.OnBinaryMessage += OnVoicePacketReceived;
    }

    /// <summary>Llamar en cada frame del loop principal con el estado de la tecla de
    /// push-to-talk. Edge-detected internamente: no hace falta que el caller lo sea.</summary>
    public void SetTalking(bool talking)
    {
        if (!CaptureAvailable || _waveIn == null || talking == _talking) return;
        _talking = talking;
        if (talking) _waveIn.StartRecording();
        else _waveIn.StopRecording();
    }

    private void OnMicDataAvailable(object? sender, WaveInEventArgs e)
    {
        if (e.BytesRecorded == 0) return;
        byte[] packet = new byte[e.BytesRecorded];
        Array.Copy(e.Buffer, packet, e.BytesRecorded);
        // Fire-and-forget: igual que el envío de "move" (ver Program.cs), un `await` acá
        // sacaría este callback del hilo del thread-pool de NAudio sin romper nada del lado
        // de la ventana Win32 (este código no corre en el hilo del loop principal), pero
        // bloquear tampoco aporta nada — el micrófono sigue capturando en su propio hilo.
        _ws.SendBinaryAsync(packet).ContinueWith(
            t => Console.Error.WriteLine($"[voz] error enviando paquete: {t.Exception?.GetBaseException().Message}"),
            TaskContinuationOptions.OnlyOnFaulted);
    }

    private void OnVoicePacketReceived(byte[] data)
    {
        if (!PlaybackAvailable || data.Length <= CharacterIdLen) return;
        string speakerId = System.Text.Encoding.ASCII.GetString(data, 0, CharacterIdLen).TrimEnd('\0');
        int audioLen = data.Length - CharacterIdLen;

        lock (_playbackLock)
        {
            if (!_playback.TryGetValue(speakerId, out var entry))
            {
                var provider = new BufferedWaveProvider(Format)
                {
                    DiscardOnBufferOverflow = true,
                    BufferDuration = TimeSpan.FromSeconds(2),
                };
                var output = new WaveOutEvent();
                try
                {
                    output.Init(provider);
                    output.Play();
                }
                catch (Exception ex)
                {
                    // Sin hardware de salida disponible en este equipo: se apaga la reproducción
                    // por completo en vez de reintentar por cada paquete/hablante nuevo.
                    Console.Error.WriteLine($"[voz] no se pudo abrir el dispositivo de salida, reproducción deshabilitada: {ex.Message}");
                    PlaybackAvailable = false;
                    output.Dispose();
                    return;
                }
                entry = (provider, output);
                _playback[speakerId] = entry;
            }
            entry.Provider.AddSamples(data, CharacterIdLen, audioLen);
        }
    }

    public void Dispose()
    {
        if (_waveIn != null)
        {
            _waveIn.DataAvailable -= OnMicDataAvailable;
            try { _waveIn.StopRecording(); } catch { /* ya puede estar parado */ }
            _waveIn.Dispose();
        }
        if (PlaybackAvailable) _ws.OnBinaryMessage -= OnVoicePacketReceived;
        lock (_playbackLock)
        {
            foreach (var (_, output) in _playback.Values) output.Dispose();
            _playback.Clear();
        }
    }
}
