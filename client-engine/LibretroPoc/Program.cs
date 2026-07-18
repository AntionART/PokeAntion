using LibretroCore;

// Prueba de concepto D.1: ¿se puede cargar mgba_libretro.dll desde C# vía P/Invoke,
// darle una ROM real, correr unos frames, y leer su RAM? No dibuja nada todavía
// (D.2, en ClientApp/, ya usa el framebuffer para renderizar). Esto solo valida la
// parte de más riesgo técnico: el binding con el core del emulador.
// Reescrito para usar LibretroCore.MgbaCore (compartido con ClientApp) en vez de
// duplicar el P/Invoke.

string romPath = args.Length > 0
    ? args[0]
    : Path.GetFullPath(Path.Combine(Directory.GetCurrentDirectory(), "..", "..", "Pokemon - Edicion Esmeralda (Spain).gba"));

if (!File.Exists(romPath))
{
    Console.Error.WriteLine($"No se encontró la ROM en: {romPath}");
    Console.Error.WriteLine("Pasala como primer argumento si está en otro lado.");
    return 1;
}

Console.WriteLine($"ROM: {romPath}");

using var core = new MgbaCore();

int frameCount = 0;
core.FrameReady += (data, width, height, pitch) =>
{
    if (frameCount == 0)
        Console.WriteLine($"Primer frame de video recibido: {width}x{height}, pitch={pitch}, formato={core.Format}");
    frameCount++;
};

if (!core.LoadGame(romPath))
{
    Console.Error.WriteLine("retro_load_game devolvió false: el core rechazó la ROM.");
    return 1;
}
Console.WriteLine("ROM cargada correctamente.");

const int framesToRun = 180; // ~3s a 60fps: pasar las pantallas de logo, llegar a un estado con RAM poblada
for (int i = 0; i < framesToRun; i++)
    core.RunFrame();
Console.WriteLine($"Corrí {framesToRun} frames ({frameCount} llegaron al callback de video).");

Console.WriteLine($"\nEl core anunció {core.Regions.Count} región(es) de memoria:");
foreach (var r in core.Regions)
    Console.WriteLine($"  '{r.Name}' start=0x{r.Start:X} len=0x{r.Len:X} ({r.Len} bytes) ptr={r.Ptr:X} flags=0x{r.Flags:X}");

var ewram = core.FindEwram();
if (ewram is null)
{
    Console.Error.WriteLine("FALLO: no se encontró una región de 256KB / start=0x02000000 (EWRAM) entre las anunciadas por el core.");
    return 1;
}
Console.WriteLine($"\nEWRAM encontrada: addrspace='{ewram.Value.Name}' start=0x{ewram.Value.Start:X} len={ewram.Value.Len} bytes ptr={ewram.Value.Ptr:X}");

unsafe
{
    byte* ewramBytes = (byte*)ewram.Value.Ptr;
    nuint size = ewram.Value.Len;
    long nonZero = 0;
    for (nuint i = 0; i < size; i++)
        if (ewramBytes[i] != 0) nonZero++;
    Console.WriteLine($"Bytes no-cero en EWRAM: {nonZero}/{size} ({100.0 * nonZero / size:F1}%)");

    Console.WriteLine("\nPrimeros 64 bytes de EWRAM (0x02000000..):");
    for (nuint i = 0; i < 64; i++)
    {
        Console.Write($"{ewramBytes[i]:X2} ");
        if ((i + 1) % 16 == 0) Console.WriteLine();
    }
}

Console.WriteLine("\nOK: el core cargó, corrió frames reales, y expone RAM legible vía P/Invoke.");
return 0;
