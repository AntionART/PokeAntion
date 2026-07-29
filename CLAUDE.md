# Pokémon Online (Antion)

**Leé `Antion.md` completo antes de tocar código.** Ese es el documento real — arquitectura,
reglas no negociables (nunca adivinar direcciones de RAM, sin sobreingeniería, servidor
autoritativo), metodología de validación, gotchas del entorno de desarrollo, y cómo levantar el
proyecto. Este archivo (`CLAUDE.md`) solo existe porque Claude Code lo carga automáticamente en
cualquier sesión nueva — es un puntero, no un resumen; no asumas que ya sabés lo suficiente sin
haber leído `Antion.md` entero.

Reglas de máxima prioridad, por si solo llegás a leer esto:
1. Nunca adivinar direcciones de memoria RAM — validar siempre en vivo (ver sección 5 de `Antion.md`).
2. Nunca descargar, incluir ni distribuir ninguna ROM, bajo ninguna forma (ni siquiera comprimida).
3. Evaluar arquitectura antes de implementar (¿esto va en el motor, el servidor, el cliente, o `RomLoader`?).
4. Sin sobreingeniería — este es un proyecto personal, no producción a escala.

Ver también `README.md` (estado detallado y verificado de cada feature) y
`RESTAURAR-PROYECTO.md` (cómo levantar todo desde cero).
