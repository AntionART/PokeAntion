package social

import "testing"

func TestParty_InviteAcceptAndMembers(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_ash_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_mist_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, partyID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	members, err := svc.Members(partyID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("Members = %+v, esperaba 2", members)
	}
	var leaderCount int
	for _, m := range members {
		if m.IsLeader {
			leaderCount++
			if m.CharacterID != ash {
				t.Errorf("líder = %q, esperaba %q (ash)", m.CharacterID, ash)
			}
		}
	}
	if leaderCount != 1 {
		t.Errorf("cantidad de líderes = %d, esperaba 1", leaderCount)
	}
}

func TestParty_AlreadyInPartyRejectsInvite(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_a2_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_m2_"+uniqueSuffix())
	brock := f.createCharacter(t, "pt_b2_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, partyID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// brock invita a misty, que ya está en un grupo.
	if _, err := svc.Invite(brock, misty); err != ErrAlreadyInParty {
		t.Errorf("Invite a alguien ya en un grupo = %v, esperaba ErrAlreadyInParty", err)
	}
}

func TestParty_OnlyLeaderCanInvite(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_a3_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_m3_"+uniqueSuffix())
	brock := f.createCharacter(t, "pt_b3_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, partyID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	// misty (miembro, no líder) intenta invitar a brock.
	if _, err := svc.Invite(misty, brock); err != ErrNotPartyLeader {
		t.Errorf("Invite de un no-líder = %v, esperaba ErrNotPartyLeader", err)
	}
}

func TestParty_LeaveTransfersLeadership(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_a4_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_m4_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, partyID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	leftPartyID, disbanded, err := svc.Leave(ash)
	if err != nil {
		t.Fatalf("Leave: %v", err)
	}
	if disbanded {
		t.Fatalf("Leave del líder con un miembro restante disolvió el grupo, no debería")
	}
	if leftPartyID != partyID {
		t.Errorf("Leave devolvió partyID %q, esperaba %q", leftPartyID, partyID)
	}

	members, err := svc.Members(partyID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 || members[0].CharacterID != misty || !members[0].IsLeader {
		t.Errorf("tras Leave del líder, Members = %+v, esperaba solo misty como líder", members)
	}
}

func TestParty_LeaveLastMemberDisbands(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_a5_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_m5_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, partyID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if _, _, err := svc.Leave(misty); err != nil {
		t.Fatalf("Leave(misty): %v", err)
	}
	_, disbanded, err := svc.Leave(ash)
	if err != nil {
		t.Fatalf("Leave(ash): %v", err)
	}
	if !disbanded {
		t.Errorf("Leave del último miembro no marcó disbanded=true")
	}

	var count int
	if err := db.QueryRow(`SELECT count(*) FROM party_groups WHERE id = $1`, partyID).Scan(&count); err != nil {
		t.Fatalf("consultando party_groups: %v", err)
	}
	if count != 0 {
		t.Errorf("party_groups todavía tiene la fila tras disolverse, count=%d", count)
	}
}

func TestParty_DeclineDoesNotJoin(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewPartyService(db)

	ash := f.createCharacter(t, "pt_a6_"+uniqueSuffix())
	misty := f.createCharacter(t, "pt_m6_"+uniqueSuffix())

	partyID, err := svc.Invite(ash, misty)
	if err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Decline(misty, partyID); err != nil {
		t.Fatalf("Decline: %v", err)
	}

	members, err := svc.Members(partyID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 1 {
		t.Errorf("Members tras Decline = %+v, esperaba solo ash", members)
	}

	// La invitación ya se consumió: aceptarla ahora debe fallar.
	if err := svc.Accept(misty, partyID); err != ErrInviteNotFound {
		t.Errorf("Accept tras Decline = %v, esperaba ErrInviteNotFound", err)
	}

	// cleanup: disolver el grupo para no dejar leader_char_id colgado.
	svc.Leave(ash)
}
