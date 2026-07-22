// Package battle es el motor de combate autoritativo del servidor — necesario porque una
// pelea (sobre todo PvP) no la puede resolver el emulador de ningún jugador: cada cliente solo
// tiene cargada SU PROPIA ROM, no puede "ver" el equipo del rival. El motor (cliente) solo
// dibuja lo que este paquete decide; ver memoria de arquitectura de esta sesión.
//
// Fórmulas y tabla de tipos sacadas del código fuente real de pokeemerald
// (Pokemon Esmeralda/pokeemerald-master, src/pokemon.c CalculateBaseDamage +
// src/battle_main.c gTypeEffectiveness), no adivinadas.
package battle

// IDs numéricos de tipo — idénticos a include/constants/pokemon.h de pokeemerald.
const (
	TypeNormal = iota
	TypeFighting
	TypeFlying
	TypePoison
	TypeGround
	TypeRock
	TypeBug
	TypeGhost
	TypeSteel
	TypeMystery
	TypeFire
	TypeWater
	TypeGrass
	TypeElectric
	TypePsychic
	TypeIce
	TypeDragon
	TypeDark
)

type typeMatchup struct {
	Attacker, Defender int
	MultiplierTenths   int // 0=nulo, 5=poco efectivo, 10=normal (no aparece en la tabla), 20=súper efectivo
}

// Tabla dispersa: solo las combinaciones NO normales están listadas (igual que
// gTypeEffectiveness real) — cualquier combinación ausente vale 10 (x1, normal).
var effectivenessTable = []typeMatchup{
	{TypeNormal, TypeRock, 5}, {TypeNormal, TypeGhost, 0}, {TypeNormal, TypeSteel, 5},
	{TypeFire, TypeFire, 5}, {TypeFire, TypeWater, 5}, {TypeFire, TypeGrass, 20},
	{TypeFire, TypeIce, 20}, {TypeFire, TypeBug, 20}, {TypeFire, TypeRock, 5},
	{TypeFire, TypeDragon, 5}, {TypeFire, TypeSteel, 20},
	{TypeWater, TypeFire, 20}, {TypeWater, TypeWater, 5}, {TypeWater, TypeGrass, 5},
	{TypeWater, TypeGround, 20}, {TypeWater, TypeRock, 20}, {TypeWater, TypeDragon, 5},
	{TypeElectric, TypeWater, 20}, {TypeElectric, TypeElectric, 5}, {TypeElectric, TypeGrass, 5},
	{TypeElectric, TypeGround, 0}, {TypeElectric, TypeFlying, 20}, {TypeElectric, TypeDragon, 5},
	{TypeGrass, TypeFire, 5}, {TypeGrass, TypeWater, 20}, {TypeGrass, TypeGrass, 5},
	{TypeGrass, TypePoison, 5}, {TypeGrass, TypeGround, 20}, {TypeGrass, TypeFlying, 5},
	{TypeGrass, TypeBug, 5}, {TypeGrass, TypeRock, 20}, {TypeGrass, TypeDragon, 5}, {TypeGrass, TypeSteel, 5},
	{TypeIce, TypeWater, 5}, {TypeIce, TypeGrass, 20}, {TypeIce, TypeIce, 5},
	{TypeIce, TypeGround, 20}, {TypeIce, TypeFlying, 20}, {TypeIce, TypeDragon, 20},
	{TypeIce, TypeSteel, 5}, {TypeIce, TypeFire, 5},
	{TypeFighting, TypeNormal, 20}, {TypeFighting, TypeIce, 20}, {TypeFighting, TypePoison, 5},
	{TypeFighting, TypeFlying, 5}, {TypeFighting, TypePsychic, 5}, {TypeFighting, TypeBug, 5},
	{TypeFighting, TypeRock, 20}, {TypeFighting, TypeDark, 20}, {TypeFighting, TypeSteel, 20},
	{TypeFighting, TypeGhost, 0},
	{TypePoison, TypeGrass, 20}, {TypePoison, TypePoison, 5}, {TypePoison, TypeGround, 5},
	{TypePoison, TypeRock, 5}, {TypePoison, TypeGhost, 5}, {TypePoison, TypeSteel, 0},
	{TypeGround, TypeFire, 20}, {TypeGround, TypeElectric, 20}, {TypeGround, TypeGrass, 5},
	{TypeGround, TypePoison, 20}, {TypeGround, TypeFlying, 0}, {TypeGround, TypeBug, 5},
	{TypeGround, TypeRock, 20}, {TypeGround, TypeSteel, 20},
	{TypeFlying, TypeElectric, 5}, {TypeFlying, TypeGrass, 20}, {TypeFlying, TypeFighting, 20},
	{TypeFlying, TypeBug, 20}, {TypeFlying, TypeRock, 5}, {TypeFlying, TypeSteel, 5},
	{TypePsychic, TypeFighting, 20}, {TypePsychic, TypePoison, 20}, {TypePsychic, TypePsychic, 5},
	{TypePsychic, TypeDark, 0}, {TypePsychic, TypeSteel, 5},
	{TypeBug, TypeFire, 5}, {TypeBug, TypeGrass, 20}, {TypeBug, TypeFighting, 5},
	{TypeBug, TypePoison, 5}, {TypeBug, TypeFlying, 5}, {TypeBug, TypePsychic, 20},
	{TypeBug, TypeGhost, 5}, {TypeBug, TypeDark, 20}, {TypeBug, TypeSteel, 5},
	{TypeRock, TypeFire, 20}, {TypeRock, TypeIce, 20}, {TypeRock, TypeFighting, 5},
	{TypeRock, TypeGround, 5}, {TypeRock, TypeFlying, 20}, {TypeRock, TypeBug, 20}, {TypeRock, TypeSteel, 5},
	{TypeGhost, TypeNormal, 0}, {TypeGhost, TypePsychic, 20}, {TypeGhost, TypeDark, 5},
	{TypeGhost, TypeSteel, 5}, {TypeGhost, TypeGhost, 20},
	{TypeDragon, TypeDragon, 20}, {TypeDragon, TypeSteel, 5},
	{TypeDark, TypeFighting, 5}, {TypeDark, TypePsychic, 20}, {TypeDark, TypeGhost, 20},
	{TypeDark, TypeDark, 5}, {TypeDark, TypeSteel, 5},
	{TypeSteel, TypeFire, 5}, {TypeSteel, TypeWater, 5}, {TypeSteel, TypeElectric, 5},
	{TypeSteel, TypeIce, 20}, {TypeSteel, TypeRock, 20}, {TypeSteel, TypeSteel, 5},
}

// Effectiveness devuelve el multiplicador de daño (1 = normal, 0.5, 2, 0) del tipo atacante
// contra hasta dos tipos defensores (defenderType2 = defenderType1 si el defensor es de un solo
// tipo) — se multiplican entre sí, igual que el juego real con Pokémon de doble tipo.
func Effectiveness(attackerType, defenderType1, defenderType2 int) float64 {
	mult := effectivenessSingle(attackerType, defenderType1)
	if defenderType2 != defenderType1 {
		mult *= effectivenessSingle(attackerType, defenderType2)
	}
	return mult
}

func effectivenessSingle(attackerType, defenderType int) float64 {
	for _, m := range effectivenessTable {
		if m.Attacker == attackerType && m.Defender == defenderType {
			return float64(m.MultiplierTenths) / 10
		}
	}
	return 1.0
}
