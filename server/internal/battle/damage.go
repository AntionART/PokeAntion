package battle

import "math"

// Combatant es lo mínimo que el cálculo de daño necesita saber de un Pokémon en combate —
// deliberadamente no es pokemon.Pokemon (ese es el registro persistido; acá solo lo que entra
// en la fórmula), para no atar este paquete a cómo se guarda en la base de datos.
type Combatant struct {
	Level               int
	Attack, Defense     int
	SpAttack, SpDefense int
	Speed               int
	Type1, Type2        int // Type2 == Type1 si el Pokémon es de un solo tipo
}

// CalculateDamage reproduce la fórmula real de pokeemerald (CalculateBaseDamage en
// src/pokemon.c), sin los casos especiales de objetos/habilidades (no implementados todavía:
// ni items ni abilities existen en este motor). randRoll es el factor aleatorio 0.85-1.00 que
// aplica el juego real (el llamador lo genera; separado para que sea testeable de forma
// determinística). Devuelve al menos 1 si el golpe conecta y no es tipo ??? contra un tipo
// inmune (0 en ese caso, igual que el juego real).
func CalculateDamage(attacker Combatant, defender Combatant, move Move, isCrit bool, randRoll float64) int {
	typeEff := Effectiveness(move.Type, defender.Type1, defender.Type2)
	if typeEff == 0 || move.Power == 0 {
		return 0
	}

	var attack, defense int
	if IsPhysical(move.Type) {
		attack, defense = attacker.Attack, defender.Defense
	} else {
		attack, defense = attacker.SpAttack, defender.SpDefense
	}

	// Núcleo exacto de CalculateBaseDamage: damage = attack; damage *= power;
	// damage *= (2*level/5 + 2); damage /= defense; damage /= 50 — todo con división entera,
	// igual que el juego real (el orden de las divisiones importa para el redondeo).
	damage := attack
	damage = damage * move.Power
	damage = damage * (2*attacker.Level/5 + 2)
	damage = damage / defense
	damage = damage / 50

	stab := 1.0
	if move.Type == attacker.Type1 || move.Type == attacker.Type2 {
		stab = 1.5
	}
	critMult := 1.0
	if isCrit {
		critMult = 2.0
	}

	final := math.Floor(float64(damage) * stab * typeEff * critMult * randRoll)
	if final < 1 {
		final = 1
	}
	return int(final)
}
