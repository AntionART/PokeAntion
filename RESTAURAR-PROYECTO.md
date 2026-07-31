# Cómo levantar este proyecto en cualquier máquina

Esta guía sirve para retomar el proyecto desde cero en una computadora distinta (o la misma,
después de reinstalar Windows). Está pensada para vos, no para repartir a otra persona (aunque
el código en sí es público en GitHub — ver nota de ROMs/datos en el paso 8).

Hay dos caminos según de dónde vengas:

- **Partiendo de un `git clone`** (bajaste el repo de GitHub en una máquina nueva): el repo NO
  trae los binarios portables de Go/Postgres ni tus datos reales (están en `.gitignore` a
  propósito — ver `.gitignore` y la nota del paso 8). Corré esto y saltá directo al paso 6:

  ```powershell
  powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\setup.ps1
  ```

  Descarga Go y Postgres portables, crea la base vacía con el esquema (migraciones), y te deja
  listo para arrancar el servidor. Vas a empezar sin tus cuentas/personajes viejos (base nueva)
  — si tenés un dump SQL tuyo guardado aparte (no en el repo), restauralo con el paso 4.3 en vez
  de dejar que `setup.ps1` cree la base vacía.

- **Partiendo de una copia de carpeta** (disco externo, backup, robocopy): seguí los pasos 1-5
  de abajo, que asumen que `go1.26.5\`, `postgresql-16.5\` y `postgres_data\` ya vinieron
  copiados con el resto del proyecto.

**Lo más rápido para saber qué falta**: corré `scripts\check-prerequisites.ps1` — te dice
exactamente qué hay y qué no antes de perder tiempo con los pasos de abajo.

## 1. Requisitos de la máquina nueva

- **Windows 10/11 de 64 bits.** El cliente (Launcher + juego) usa Direct3D/Win32 — no corre en
  Linux ni Mac.
- **.NET 10 SDK** — el único requisito que hay que instalar a mano. Descarga oficial:
  https://dotnet.microsoft.com/download (elegí "SDK", no "Runtime"). Sin esto podés levantar el
  servidor igual, pero no vas a poder compilar/abrir el Launcher ni el cliente.
- **Go y Postgres NO hace falta instalarlos a mano** — son binarios portables que viven en
  `go1.26.5\` y `postgresql-16.5\`. Si copiaste la carpeta completa, ya están ahí. Si venís de
  un `git clone`, no están (no viajan por git) pero `scripts\setup.ps1` los descarga solo.
- **Redis es opcional** — si no está, el servidor arranca igual y solo se desactiva el límite de
  mensajes de chat (falla "abierto", no bloquea nada). No hace falta instalarlo para uso normal.

## 2. Copiar el proyecto

*(Este paso y los siguientes hasta el 5 son para el camino "copia de carpeta". Si veniste de
`git clone` y ya corriste `setup.ps1`, saltealos y andá al paso 6.)*

Copiá la carpeta completa a la máquina nueva (o al disco desde el que vas a trabajar). Si venís
de un backup hecho con robocopy excluyendo `bin`/`obj`/`node_modules`, no pasa nada — se
regeneran solos al compilar.

## 3. Levantar Postgres con tus datos reales

Si la carpeta `postgres_data\` vino copiada (tiene un archivo `PG_VERSION` adentro), tus
personajes/cuentas ya están ahí — no hace falta restaurar nada:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-postgres.ps1
```

Esto arranca Postgres apuntando a `postgres_data\` tal cual está. Listo, seguí al paso 5.

## 4. Plan B: si `postgres_data\` no vino, o no es compatible

Esto arranca una base **completamente vacía** y la reconstruye desde el dump SQL más reciente
de `database\backups\` (buscá el archivo con la fecha más nueva, formato
`pokemon_online_AAAA-MM-DD_HHMMSS.sql`).

**4.1 — Arrancar Postgres vacío** (crea `postgres_data\` nueva si no existe):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\start-postgres.ps1
```

**4.2 — Crear el rol y las bases** (esto NO está scripteado — se hizo a mano la primera vez, así
que hay que repetirlo en una instalación nueva). Con Postgres recién iniciado por `initdb`, el
único rol que existe es `postgres` sin contraseña (`--auth=trust`):

```powershell
$psql = ".\postgresql-16.5\pgsql\bin\psql.exe"
& $psql -U postgres -h localhost -p 5432 -d postgres -c "CREATE ROLE pokemon WITH LOGIN PASSWORD 'pokemon' CREATEDB;"
& $psql -U postgres -h localhost -p 5432 -d postgres -c "CREATE DATABASE pokemon_online OWNER pokemon;"
& $psql -U postgres -h localhost -p 5432 -d postgres -c "CREATE DATABASE pokemon_online_test OWNER pokemon;"
```

**4.3 — Restaurar el dump SQL más reciente** (esto crea todas las tablas Y carga tus datos
reales — no hace falta correr las migraciones aparte si restaurás el dump):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\restore-database.ps1 -BackupFile "database\backups\pokemon_online_2026-07-29_140358.sql" -Force
```

(Cambiá el nombre de archivo por el dump que tengas — `Get-ChildItem database\backups` para
ver cuáles hay.)

**Si no tenés ningún dump SQL a mano** (base completamente nueva, sin tus datos viejos): en vez
del paso 4.3, corré las migraciones vacías:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File .\scripts\apply-migrations.ps1
```

## 5. Levantar el servidor

```powershell
cd server
..\go1.26.5\go\bin\go.exe run ./cmd/server
```

Si ves `servidor escuchando port=8080` sin errores, anduvo. Dejalo corriendo en esa ventana.

**Atajo**: `scripts\start-everything.ps1` hace los pasos 3 y 5 juntos (arranca Postgres si hace
falta, y después el servidor) — no reemplaza el paso 4 si necesitás restaurar desde cero.

## 6. Abrir el Launcher

En otra ventana:

```powershell
cd client-engine\Launcher
dotnet run
```

Primera vez: te va a pedir seleccionar tu ROM (nunca se incluye/distribuye — la tenés que tener
vos, de una copia a la que tengas derecho). Después de elegirla, el Launcher se acuerda de la
ruta sola.

Si ves la pantalla principal con "En línea" y el botón JUGAR, todo quedó funcionando igual que
antes.

## 7. Valores/nombres importantes (por si algo falla y hay que revisar a mano)

| Qué | Valor |
|---|---|
| Usuario de Postgres | `pokemon` |
| Contraseña de Postgres | `pokemon` |
| Base de datos principal | `pokemon_online` |
| Base de datos de tests | `pokemon_online_test` |
| Host/puerto de Postgres | `localhost:5432` |
| Puerto del servidor (HTTP + WebSocket) | `8080` |
| `DATABASE_URL` (si hiciera falta setearla a mano) | `postgres://pokemon:pokemon@localhost:5432/pokemon_online?sslmode=disable` |
| `JWT_SECRET` (default de desarrollo) | `dev-secret-change-me` |
| Carpeta de datos reales de Postgres | `postgres_data\` |
| Backups SQL de emergencia | `database\backups\*.sql` |
| Binario portable de Go | `go1.26.5\go\bin\go.exe` |
| Binarios portables de Postgres | `postgresql-16.5\pgsql\bin\` |

Ninguno de estos valores es secreto real (es un proyecto personal/de amigos, no producción
pública) — están todos hardcodeados como default en `server/internal/config/config.go` también,
así que esta tabla no revela nada que el código mismo no tenga ya.

## 8. Notas sobre las ROMs

Este proyecto **nunca** descarga, incluye ni distribuye ROMs. Si copiaste la carpeta con las
ROMs `.gba` adentro (uso personal, tu propia copia), el Launcher las va a detectar solo por
hash. Si no, seleccionalas a mano la primera vez que abrís el Launcher.
