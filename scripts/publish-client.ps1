<#
Publica el cliente como self-contained (win-x64): la carpeta resultante incluye el runtime de
.NET, asi que corre en una PC sin .NET instalado -- solo hace falta copiar la carpeta entera.

Lo que el usuario TIENE que traer aparte (por la restriccion legal del proyecto: nunca se
distribuyen ROMs ni assets de Nintendo/Game Freak, ver README.md seccion 8):
  - Su propia ROM (.gba) legalmente obtenida, en la ruta que declare memory-maps/*.json
    (rom_path), o pasada con --rom.
  - El repo completo (o al menos memory-maps/, data/pokemon/, y el checkout de
    "Pokemon Esmeralda/pokeemerald-master" para los sprites de batalla) tiene que estar
    presente junto a la carpeta publicada -- ClientApp.exe busca la raiz del repo subiendo
    directorios hasta encontrar memory-maps/ y data/pokemon/ (ver FindRepoRoot en Program.cs),
    asi que no alcanza con copiar SOLO el .exe a otra PC sin el resto del repo al lado.

Uso: desde la raiz del repo:
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\publish-client.ps1
#>
$RepoRoot = Split-Path -Parent $PSScriptRoot
$ClientAppDir = Join-Path $RepoRoot "client-engine\ClientApp"
$OutDir = Join-Path $ClientAppDir "bin\publish-win-x64"

if (-not (Test-Path $ClientAppDir)) { Write-Error "No se encontro $ClientAppDir"; exit 1 }

Write-Output "Publicando ClientApp (Release, win-x64, self-contained) en $OutDir ..."
Push-Location $ClientAppDir
try {
    dotnet publish -c Release -r win-x64 --self-contained true -o "bin\publish-win-x64"
    if ($LASTEXITCODE -ne 0) { Write-Error "dotnet publish fallo"; exit $LASTEXITCODE }
} finally {
    Pop-Location
}

Write-Output ""
Write-Output "Listo: $OutDir"
Write-Output "Para probar: copiar esa carpeta junto con el resto del repo (memory-maps/, data/pokemon/,"
Write-Output "el checkout de pokeemerald-master, y la ROM propia) a la PC destino, y correr ClientApp.exe ahi."
