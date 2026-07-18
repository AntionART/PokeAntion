using LibretroCore;
using RomLoader;

// Herramienta headless (sin ventana/D3D) para D.3: ejecuta un guion de botones sobre el
// core real, y puede volcar capturas (BMP) y snapshots de EWRAM en el camino. Soporta
// save states (retro_serialize) para retomar exploración entre invocaciones sin tener que
// rebootear la ROM y renavegar menús cada vez.
//
// Uso: Explorer <rom.gba> <script.txt> [--load-state <in>] [--save-state <out>]
//
// Comandos del script (uno por línea, # para comentarios):
//   WAIT <n>              corre n frames sin tocar nada
//   HOLD <boton> <n>      sostiene un botón (Up/Down/Left/Right/A/B/Start/Select/L/R) por n frames
//   DUMP <archivo.bmp>    vuelca el frame actual (escalado x3) a BMP
//   EWRAM <archivo.bin>   vuelca los 256KB de EWRAM crudos

if (args.Length < 2)
{
    Console.Error.WriteLine("Uso: Explorer <rom.gba> <script.txt> [--load-state <in>] [--save-state <out>]");
    return 1;
}

string romPath = args[0];
string scriptPath = args[1];
string? loadStatePath = GetOpt(args, "--load-state");
string? saveStatePath = GetOpt(args, "--save-state");

using var core = new MgbaCore();
if (!core.LoadGame(romPath))
{
    Console.Error.WriteLine("El core rechazó la ROM.");
    return 1;
}

IntPtr lastFrameData = IntPtr.Zero;
uint lastWidth = 0, lastHeight = 0;
nuint lastPitch = 0;
core.FrameReady += (data, w, h, pitch) => { lastFrameData = data; lastWidth = w; lastHeight = h; lastPitch = pitch; };

// Correr un frame antes de (des)serializar: asegura que el core ya inicializó sus buffers.
core.RunFrame();

if (loadStatePath != null)
{
    if (!File.Exists(loadStatePath))
    {
        Console.Error.WriteLine($"No existe el estado: {loadStatePath}");
        return 1;
    }
    core.LoadState(File.ReadAllBytes(loadStatePath));
    Console.WriteLine($"Estado cargado desde {loadStatePath}");
}

JoypadButton? heldButton = null;
core.InputState = (port, device, index, id) =>
{
    if (port != 0 || device != RetroDevice.Joypad || heldButton == null) return 0;
    return (uint)heldButton.Value == id ? (short)1 : (short)0;
};

if (!File.Exists(scriptPath))
{
    Console.Error.WriteLine($"No existe el script: {scriptPath}");
    return 1;
}

foreach (string rawLine in File.ReadAllLines(scriptPath))
{
    string line = rawLine.Trim();
    if (line.Length == 0 || line.StartsWith('#')) continue;
    string[] parts = line.Split(' ', StringSplitOptions.RemoveEmptyEntries);
    string cmd = parts[0].ToUpperInvariant();

    switch (cmd)
    {
        case "WAIT":
            {
                int n = int.Parse(parts[1]);
                heldButton = null;
                for (int i = 0; i < n; i++) core.RunFrame();
                Console.WriteLine($"WAIT {n}");
                break;
            }
        case "HOLD":
            {
                var button = Enum.Parse<JoypadButton>(parts[1], ignoreCase: true);
                int n = int.Parse(parts[2]);
                heldButton = button;
                for (int i = 0; i < n; i++) core.RunFrame();
                heldButton = null;
                Console.WriteLine($"HOLD {button} {n}");
                break;
            }
        case "DUMP":
            {
                string path = parts[1];
                DumpBmp(path, lastFrameData, lastWidth, lastHeight, lastPitch, scale: 3);
                Console.WriteLine($"DUMP -> {path}");
                break;
            }
        case "EWRAM":
            {
                string path = parts[1];
                var ewram = core.FindEwram();
                if (ewram == null) { Console.Error.WriteLine("No se encontró EWRAM"); break; }
                unsafe
                {
                    var span = new ReadOnlySpan<byte>((void*)ewram.Value.Ptr, (int)ewram.Value.Len);
                    File.WriteAllBytes(path, span.ToArray());
                }
                Console.WriteLine($"EWRAM -> {path}");
                break;
            }
        case "POS":
            {
                // Verifica el IMemoryAdapter (D.4) contra un memory-maps/*.json real, leyendo
                // la posición del jugador tal como la va a consumir el resto del cliente.
                string mapPath = parts[1];
                var config = MemoryMapConfig.LoadFromFile(mapPath);
                var adapter = new GbaMemoryAdapter(core, config);
                var pos = adapter.GetPlayerPosition();
                Console.WriteLine($"POS ({config.RomId}) -> x={pos.X} y={pos.Y}");
                break;
            }
        default:
            Console.Error.WriteLine($"Comando desconocido: {cmd}");
            break;
    }
}

if (saveStatePath != null)
{
    File.WriteAllBytes(saveStatePath, core.SaveState());
    Console.WriteLine($"Estado guardado en {saveStatePath}");
}

return 0;

static string? GetOpt(string[] args, string name)
{
    int idx = Array.IndexOf(args, name);
    return idx >= 0 && idx + 1 < args.Length ? args[idx + 1] : null;
}

static unsafe void DumpBmp(string path, IntPtr rgb565Data, uint width, uint height, nuint pitchBytes, int scale)
{
    if (rgb565Data == IntPtr.Zero || width == 0 || height == 0)
    {
        Console.Error.WriteLine("No hay frame todavía para volcar.");
        return;
    }

    int outW = (int)width * scale;
    int outH = (int)height * scale;
    int rowBytes = outW * 4;
    byte[] pixels = new byte[rowBytes * outH];
    byte* src = (byte*)rgb565Data;

    fixed (byte* dstBase = pixels)
    {
        for (int y = 0; y < outH; y++)
        {
            int srcY = y / scale;
            ushort* srcRow = (ushort*)(src + srcY * (long)pitchBytes);
            uint* dstRow = (uint*)(dstBase + y * rowBytes);
            for (int x = 0; x < outW; x++)
            {
                int srcX = x / scale;
                ushort px = srcRow[srcX];
                uint r5 = (uint)(px >> 11) & 0x1F;
                uint g6 = (uint)(px >> 5) & 0x3F;
                uint b5 = (uint)px & 0x1F;
                byte r = (byte)((r5 << 3) | (r5 >> 2));
                byte g = (byte)((g6 << 2) | (g6 >> 4));
                byte b = (byte)((b5 << 3) | (b5 >> 2));
                dstRow[x] = (uint)(0xFF << 24 | r << 16 | g << 8 | b);
            }
        }
    }

    WriteBmp32(path, outW, outH, pixels);
}

static void WriteBmp32(string path, int width, int height, byte[] bgraPixelsTopDown)
{
    int rowBytes = width * 4;
    int dataSize = rowBytes * height;
    int fileSize = 14 + 40 + dataSize;

    using var fs = new FileStream(path, FileMode.Create, FileAccess.Write);
    using var w = new BinaryWriter(fs);

    w.Write((byte)'B'); w.Write((byte)'M');
    w.Write(fileSize);
    w.Write(0);
    w.Write(14 + 40);

    w.Write(40);
    w.Write(width);
    w.Write(-height);
    w.Write((short)1);
    w.Write((short)32);
    w.Write(0);
    w.Write(dataSize);
    w.Write(0); w.Write(0);
    w.Write(0); w.Write(0);

    w.Write(bgraPixelsTopDown);
}
