package battle

import (
	"fmt"
	"math/rand"
	"testing"
)

// Sanity check manual: Torchic (Fire, Scratch/Growl) vs Mudkip (Water, Tackle/Growl) a nivel 5,
// varios turnos hasta que uno caiga — confirma que el motor da resultados jugables (daño > 0,
// alguien gana, sin panics) contra datos reales, no solo que compila.
func TestSimulatedBattle(t *testing.T) {
	torchic := &Fighter{
		Combatant: Combatant{Level: 5, Attack: 15, Defense: 9, SpAttack: 16, SpDefense: 10, Speed: 9, Type1: TypeFire, Type2: TypeFire},
		CurrentHP: 19, MaxHP: 19, Moves: [4]int{MoveScratch, MoveGrowl, 0, 0}, PP: [4]int{35, 40, 0, 0},
	}
	mudkip := &Fighter{
		Combatant: Combatant{Level: 5, Attack: 17, Defense: 12, SpAttack: 12, SpDefense: 12, Speed: 7, Type1: TypeWater, Type2: TypeWater},
		CurrentHP: 20, MaxHP: 20, Moves: [4]int{MoveTackle, MoveGrowl, 0, 0}, PP: [4]int{35, 40, 0, 0},
	}

	rng := rand.New(rand.NewSource(1))
	fighters := [2]*Fighter{torchic, mudkip}
	turn := 0
	for torchic.CurrentHP > 0 && mudkip.CurrentHP > 0 && turn < 50 {
		turn++
		events := ResolveTurn(fighters, [2]Action{{MoveSlot: 0}, {MoveSlot: 0}}, rng)
		for _, e := range events {
			fmt.Printf("turno %d: %+v\n", turn, e)
		}
	}
	fmt.Printf("resultado: torchic hp=%d/%d, mudkip hp=%d/%d, turnos=%d\n",
		torchic.CurrentHP, torchic.MaxHP, mudkip.CurrentHP, mudkip.MaxHP, turn)

	if turn >= 50 {
		t.Fatal("la batalla no terminó en 50 turnos, algo está mal en la resolución de daño")
	}
	if torchic.CurrentHP > 0 && mudkip.CurrentHP > 0 {
		t.Fatal("terminó el loop pero ninguno cayó")
	}
}
