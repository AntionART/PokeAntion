# PROMPT MAESTRO — Pokémon Online (proyecto tipo MMORPG sobre ROM emulada)

## Rol que debe asumir quien desarrolle esto

Actúa como arquitecto de software senior especializado en videojuegos online, emulación de GBA y MMORPGs. El objetivo es diseñar e implementar, de forma profesional y escalable, una capa multijugador sobre Pokémon Esmeralda (y en el futuro FireRed, LeafGreen, Ruby, Sapphire), que permita a varios amigos compartir el mismo mundo, verse caminando en tiempo real, chatear, intercambiar Pokémon, formar grupos y tener progreso individual guardado en servidor.

---

## 1. Visión del producto

Un juego donde:

- Cada jugador corre su propia instancia de la ROM (vía emulación local), pero **ve a los demás jugadores conectados** moviéndose en el mismo mapa, en tiempo real.
- El progreso de cada jugador (Pokémon, objetos, dinero, Pokédex, misiones) vive en el **servidor**, no en archivos locales.
- Los jugadores pueden **chatear** (global, local, privado, por comandos), **agregarse como amigos**, **formar grupos** y **hacer intercambios seguros** de Pokémon.
- Por ahora las batallas son **PvE individuales**; el sistema debe quedar preparado para agregar PvP más adelante sin rediseñar la arquitectura.
- El proyecto debe soportar múltiples ROMs en el futuro como módulos independientes, sin tocar el código base del motor online.

**No objetivo (por ahora):** PvP, economía global, servidores propios de distribución de ROMs (el jugador aporta su propia ROM legalmente obtenida; el proyecto nunca aloja ni distribuye ROMs ni assets de Nintendo/Game Freak).

---

## 2. Restricción técnica fundamental (léase antes de diseñar nada)

La lógica de juego (movimiento, batallas, cálculo de daño, scripts, eventos) **vive dentro del binario de la ROM ejecutándose en el emulador**, no en código propio. Esto tiene una consecuencia directa sobre el diseño:

> El servidor **no puede ser 100% autoritativo sobre el gameplay** (posición, batallas PvE, diálogos) porque no controla esa lógica. Solo puede ser autoritativo sobre los **sistemas que se construyen fuera de la ROM**: cuentas, intercambios, chat, amigos, grupos, y validación/mitigación de anomalías de movimiento (velocidad, teletransporte).

Por lo tanto, el diseño de "autoridad de servidor" debe dividirse explícitamente en dos categorías:

| Autoridad del servidor (fuerte) | Autoridad del cliente (con mitigación) |
|---|---|
| Cuentas y contraseñas | Posición y movimiento dentro del mapa |
| Inventario, dinero, Pokémon (fuente de verdad final) | Resultado de batallas PvE |
| Intercambios (transacción atómica) | Animaciones, diálogos, scripts de la ROM |
| Chat, amigos, grupos | Progreso de historia/eventos locales |
| Guardado de partida | |

Para que el inventario/Pokémon/dinero sean realmente autoritativos del servidor (y evitar duplicación), el sistema de intercambio y guardado debe **leer y escribir directamente sobre la estructura de datos del save (RAM/SRAM)**, no confiar en que "el cliente reportó que ganó un Pokémon". Esto requiere un **mapa de memoria por ROM** (direcciones conocidas de la comunidad de romhacking), que además es la pieza clave para soportar múltiples ROMs: cada ROM tiene su propio archivo de mapeo de memoria, mientras el motor online es genérico.

---

## 3. Arquitectura por capas

```
Cliente (Godot 4 + C#)
   ↓
Motor de juego (core de emulación GBA embebido, ej. mGBA vía libretro)
   ↓
Adaptador de memoria por ROM (lee/escribe posición, dirección, animación, save data)
   ↓
Sincronizador Online (empaqueta estado del jugador, aplica interpolación de otros jugadores)
   ↓
Protocolo / API (WebSocket para todo; UDP opcional a futuro solo si hace falta)
   ↓
Servidor (Go) — autoritativo sobre cuentas, inventario, intercambios, chat, grupos
   ↓
Base de datos (PostgreSQL + Redis)
```

**Explicación de cada capa:**

- **Cliente**: renderiza el juego, captura input, muestra UI de chat/amigos/intercambios.
- **Motor de juego**: el núcleo de emulación real, ejecuta la ROM tal cual, sin modificarla.
- **Adaptador de memoria por ROM**: capa de traducción — "dame posición X,Y", "dame lista de Pokémon", sin que el resto del sistema sepa en qué dirección de RAM está eso. Es la pieza que permite soportar múltiples ROMs.
- **Sincronizador Online**: decide qué se envía al servidor y con qué frecuencia; aplica interpolación/extrapolación de los jugadores remotos para que el movimiento se vea fluido aunque lleguen paquetes cada 100-150ms.
- **Protocolo/API**: contrato de mensajes cliente↔servidor, versionado, independiente del transporte.
- **Servidor**: lógica de negocio real (autenticación, intercambios, chat, grupos), autoritativo donde debe serlo.
- **Base de datos**: persistencia durable (Postgres) + estado efímero/sesiones (Redis).

---

## 4. Diseño de red

**Decisión recomendada: WebSockets para todo en el MVP.**

Razón: el movimiento en Pokémon es por **casillas discretas** (grid-based), no continuo como un shooter. No hay requerimiento real de latencia sub-frame. Un protocolo confiable y simple (WebSocket sobre TCP) es suficiente y evita meses de complejidad extra (NAT traversal, capa de fiabilidad propia, etc.).

| Opción | Ventaja | Desventaja | Uso recomendado |
|---|---|---|---|
| WebSockets | Simple, confiable, funciona en cualquier red/firewall, un solo protocolo para todo | Algo más de overhead que UDP puro | **Elegido para MVP: movimiento, chat, intercambios, grupos** |
| UDP/ENet | Baja latencia, ideal para acción rápida | Requiere capa propia de fiabilidad, más complejidad de NAT | Solo si en el futuro se agrega PvP en tiempo real o el juego deja de ser grid-based |
| TCP crudo | Confiable | Hay que reimplementar framing de mensajes | Sin ventaja sobre WebSocket para este caso |
| Steam Networking | Buen NAT traversal, gratis con Steamworks | Ata el proyecto a la plataforma Steam | Solo si se distribuye por Steam |

Dejar el protocolo (capa 4 de la arquitectura) desacoplado del transporte, para poder migrar a UDP/ENet en zonas críticas (ej. batallas PvP futuras) sin rediseñar todo.

---

## 5. Sincronización — qué se sincroniza y qué no

**Se sincroniza (vía servidor, a los demás jugadores del mismo mapa):**
- Jugadores: nombre, sprite, posición, dirección, animación, mapa actual, estado (caminando/quieto/luchando).
- Chat (global, local, privado, comandos).
- Intercambios (solicitud, aceptación, cancelación, confirmación).
- Grupos (invitación, aceptación, salida, ubicación de miembros).
- Eventos compartidos que definamos a futuro (ej. jugador entra/sale de un mapa).

**No se sincroniza (se ejecuta local, dentro de la ROM de cada cliente):**
- NPCs.
- Batallas PvE (resultado se reporta al servidor solo para actualizar Pokémon/objetos/dinero tras la batalla).
- Diálogos, cinemáticas, scripts de historia.
- Animaciones internas del motor gráfico.

**Movimiento:** cliente predice localmente (responsividad inmediata), envía posición al servidor a intervalos cortos, servidor valida (velocidad máxima, colisiones básicas de mapa) y retransmite a los demás jugadores del mismo mapa; los clientes remotos aplican **interpolación** entre la última posición conocida y la nueva para evitar saltos bruscos.

---

## 6. Sistema de intercambio (trading)

Flujo con doble confirmación y sin ventana de duplicación:

1. **Solicitud**: jugador A envía solicitud de trade a jugador B (por proximidad o por nombre).
2. **Aceptación**: B acepta → se abre sesión de trade (bloquea ambos inventarios de Pokémon en servidor, marcándolos como "en transacción").
3. **Selección**: cada jugador elige qué Pokémon ofrece; ambos ven en tiempo real la oferta del otro.
4. **Confirmación doble**: ambos deben confirmar explícitamente después de ver la oferta final (evita cambios de última hora sin darse cuenta).
5. **Transacción atómica en servidor**: el intercambio se ejecuta como una única transacción de base de datos (todo o nada). Si falla a mitad de camino → **rollback completo**, ningún Pokémon se pierde ni se duplica.
6. **Confirmación al cliente**: servidor notifica éxito, cada cliente actualiza su vista local (y si aplica, escribe el resultado en el save de la ROM vía el adaptador de memoria).

Reglas de seguridad: un Pokémon en trade queda bloqueado para cualquier otra operación (otro trade, PC, batalla) hasta que la transacción termine o se cancele; toda la lógica de "quién tiene qué" vive en base de datos, nunca se confía en el estado que reporta el cliente.

---

## 7. Chat, amigos y grupos

**Chat:**
- Global (todo el servidor o por región/mapa amplio).
- Local (solo jugadores en el mismo mapa/radio).
- Privado (mensaje directo).
- Comandos: `/trade`, `/msg`, `/help`, `/friend`, `/group`, extensible.

**Amigos:**
- Lista de amigos, invitar, eliminar, ver estado de conexión (online/offline/en batalla).

**Grupos:**
- Invitar, aceptar, salir, ver ubicación de los miembros del grupo en el mapa (mini-mapa o lista con mapa actual de cada uno).

---

## 8. Base de datos — diseño conceptual

**PostgreSQL (persistente, fuente de verdad):**
- `accounts` (usuario, hash de contraseña, email, fecha de creación)
- `characters` (perfil del jugador, mapa actual, posición, dinero)
- `pokemon` (instancia de Pokémon: especie, nivel, stats, movimientos, dueño actual, ubicación: equipo/PC/en-trade)
- `pc_boxes` (organización del PC)
- `items` / `inventory` (objetos y cantidades por jugador)
- `pokedex_entries` (visto/capturado por jugador)
- `quests_progress` (misiones)
- `friends` (relaciones jugador↔jugador)
- `groups` / `group_members`
- `trade_log` (auditoría de intercambios, para poder investigar disputas o bugs)

**Redis (efímero, alta frecuencia):**
- Sesiones activas / tokens
- Posición en tiempo real de jugadores conectados (antes de persistir a Postgres periódicamente)
- Presencia (quién está online, en qué mapa)
- Rate limiting (anti-spam de chat, anti-flood de movimiento)

Contraseñas: hash con Argon2id o bcrypt, nunca texto plano ni cifrado reversible.

---

## 9. Flujo de paquetes (ejemplo: movimiento)

```
Cliente A presiona "arriba"
   → Cliente A mueve sprite localmente (predicción inmediata)
   → Cliente A envía { tipo: "move", dir: "up", pos: {x,y}, mapa } al servidor
   → Servidor valida (¿velocidad razonable? ¿colisión válida?)
   → Servidor actualiza posición en Redis
   → Servidor difunde { tipo: "player_update", playerId: A, pos, dir, anim, mapa }
     a todos los jugadores en el mismo mapa (excepto A)
   → Cliente B recibe el update
   → Cliente B interpola el sprite de A hacia la nueva posición
```

---

## 10. Tecnologías — comparación y recomendación

| Componente | Opciones evaluadas | Recomendación |
|---|---|---|
| Cliente | C++/Raylib, C++/SDL, C#/Godot, C#/Unity | **Godot 4 + C#**: desarrollo rápido, buen 2D, gratuito, sin regalías |
| Emulación | Core propio, mGBA, VBA-M | **mGBA vía libretro** (más preciso y mantenido) |
| Servidor | Node.js, Go, Rust, Java, C# | **Go**: concurrencia simple (goroutines), ideal para miles de conexiones, desarrollo ágil |
| Base de datos persistente | PostgreSQL, SQLite | **PostgreSQL**: soporta concurrencia real y escalar más allá de un solo servidor |
| Base de datos efímera | Redis | **Redis**: sesiones, presencia, rate limiting |
| Transporte | TCP, UDP, WebSockets, ENet, Steam Networking | **WebSockets** para MVP (ver sección 4) |

---

## 11. Estructura del proyecto

```
Client/
  Engine/          → integración del core de emulación (mGBA) y su ciclo de vida
  Renderer/        → salida gráfica, cámara, sprites de otros jugadores
  Audio/           → sonido del juego emulado + sonidos propios de UI online
  Network/         → cliente WebSocket, reconexión, colas de mensajes
  Protocol/        → definición de mensajes cliente↔servidor (compartido con Server)
  UI/              → chat, lista de amigos, ventana de trade, grupos

Server/
  Auth/            → registro, login, sesiones
  World/           → estado de mapas, presencia de jugadores por mapa
  Trade/           → máquina de estados de intercambio, transacciones atómicas
  Chat/            → enrutamiento de mensajes, comandos
  Social/          → amigos, grupos
  Persistence/     → capa de acceso a Postgres/Redis

Database/
  migrations/      → esquema versionado de Postgres
  seeds/           → datos iniciales (opcional)

Common/
  MemoryMaps/      → un archivo de mapeo de memoria por ROM soportada (Esmeralda, FireRed, etc.)
  Protocol/        → contratos de mensajes compartidos entre Client y Server

Assets/            → assets propios de UI online (no assets de Nintendo)

Tools/             → utilidades de desarrollo (inspector de memoria, generador de mapas)

Launcher/          → verifica versión del cliente, permite seleccionar/verificar ROM local del usuario

Updater/           → actualización del cliente (no de la ROM)

Documentation/      → arquitectura, protocolo, guías de contribución

Tests/             → pruebas unitarias e integración (especialmente Trade y Persistence)
```

---

## 12. Roadmap por fases

**Fase 1 — Prueba de concepto de emulación (la parte de mayor riesgo, hacerla primero)**
- Integrar mGBA como librería dentro de Godot/C#.
- Leer posición/dirección del jugador desde memoria (mapa de memoria de Esmeralda).
- Mover el personaje y confirmar que se puede leer el estado de forma confiable.

**Fase 2 — Base del servidor**
- Login/registro, contraseñas cifradas.
- Chat básico (global).
- Conexión WebSocket estable con reconexión.

**Fase 3 — Mundo compartido**
- Ver a otros jugadores caminando (posición, dirección, animación, interpolación).
- Presencia por mapa.

**Fase 4 — Sistemas sociales y de intercambio**
- Amigos, grupos.
- Intercambio completo con transacción atómica y rollback.
- Chat local/privado/comandos.

**Fase 5 — Persistencia completa y pulido**
- Guardado automático en servidor (Pokémon, PC, objetos, dinero, Pokédex, misiones).
- Batallas PvE reportando resultados al servidor.

**Fase 6 — Escalabilidad y herramientas**
- Pruebas de carga (5 → 20 → 100 → 500 → 1000 jugadores).
- Launcher y actualizador.
- Preparar (sin implementar aún) los ganchos necesarios para PvP futuro.

**Fase 7 — Multi-ROM**
- Extraer el mapa de memoria de Esmeralda a un módulo independiente.
- Agregar mapas de memoria de FireRed/LeafGreen/Ruby/Sapphire como nuevos módulos, reutilizando el mismo motor online.

---

## 13. Riesgos técnicos principales

1. **Integración del core de emulación con Godot/C#** — no hay bindings oficiales; requiere FFI/wrapper. Mayor riesgo técnico del proyecto, por eso va en Fase 1.
2. **Mapas de memoria dependientes de versión de ROM** (US/EU/JP) — frágiles, deben versionarse y validarse por checksum de ROM.
3. **Duplicación de Pokémon** — mitigado con transacciones atómicas y bloqueo de estado durante trade, pero requiere pruebas exhaustivas (es el bug más dañino en este tipo de proyectos).
4. **Cheating por modificación de memoria local** — dado que el cliente ejecuta la ROM localmente, un usuario podría alterar su propia RAM. Mitigación: cualquier dato que importe para otros jugadores (inventario, Pokémon, dinero) debe validarse/derivarse en servidor, no confiar ciegamente en lo que el cliente reporta.
5. **Zona gris legal de copyright** — el proyecto no debe alojar ni distribuir ROMs ni assets de Nintendo/Game Freak; el usuario aporta su propia ROM. Aun así, proyectos similares han recibido cease & desist; es un riesgo a tener mapeado, no necesariamente bloqueante.
6. **Escalabilidad de presencia por mapa** — con muchos jugadores en el mismo mapa, el volumen de "player_update" crece; mitigar con áreas de interés (solo difundir a quienes están cerca/mismo mapa) desde el diseño inicial.

---

## 14. Recomendaciones finales

- Empezar por la Fase 1 (prueba de concepto de emulación) antes de invertir tiempo en diseñar el resto al detalle — es lo que puede cambiar decisiones de arquitectura más arriba.
- No implementar UDP/ENet hasta tener evidencia real de que WebSockets es insuficiente.
- Diseñar el mapa de memoria como archivo de configuración por ROM desde el día uno, aunque solo se soporte Esmeralda al inicio — así la Fase 7 (multi-ROM) es una extensión, no una reescritura.
- Priorizar pruebas automatizadas en el módulo de Trade por encima de cualquier otro sistema: es el que más daño puede causar si falla.
- Mantener toda la documentación de protocolo (Common/Protocol) versionada y compartida entre Client y Server para evitar desincronización de mensajes.

---

*Documento base para arquitectura, roadmap y diseño técnico. No incluye código de implementación — pensado como especificación para iniciar el desarrollo por fases.*
