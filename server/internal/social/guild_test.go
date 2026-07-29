package social

import "testing"

func TestGuild_CreateInviteAcceptAndMembers(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_ash_"+uniqueSuffix())
	misty := f.createCharacter(t, "gd_mist_"+uniqueSuffix())

	guildName := "Guild_" + uniqueSuffix()
	guildID, err := svc.Create(ash, guildName)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := svc.Invite(ash, misty); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, guildID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	members, err := svc.Members(guildID)
	if err != nil {
		t.Fatalf("Members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("Members = %+v, esperaba 2", members)
	}

	name, err := svc.NameOf(guildID)
	if err != nil || name != guildName {
		t.Errorf("NameOf = (%q, %v), esperaba %q", name, err, guildName)
	}
	leader, err := svc.LeaderOf(guildID)
	if err != nil || leader != ash {
		t.Errorf("LeaderOf = (%q, %v), esperaba %q", leader, err, ash)
	}
}

func TestGuild_DuplicateNameRejected(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_a2_"+uniqueSuffix())
	misty := f.createCharacter(t, "gd_m2_"+uniqueSuffix())

	guildName := "Dup_" + uniqueSuffix()
	if _, err := svc.Create(ash, guildName); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(misty, guildName); err != ErrGuildNameTaken {
		t.Errorf("Create con nombre repetido = %v, esperaba ErrGuildNameTaken", err)
	}
}

func TestGuild_OnlyLeaderCanInviteOrKick(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_a3_"+uniqueSuffix())
	misty := f.createCharacter(t, "gd_m3_"+uniqueSuffix())
	brock := f.createCharacter(t, "gd_b3_"+uniqueSuffix())

	guildID, err := svc.Create(ash, "Leadr_"+uniqueSuffix())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Invite(ash, misty); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, guildID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	if _, err := svc.Invite(misty, brock); err != ErrNotGuildLeader {
		t.Errorf("Invite de un no-líder = %v, esperaba ErrNotGuildLeader", err)
	}
	if _, err := svc.Kick(misty, ash); err != ErrNotGuildLeader {
		t.Errorf("Kick de un no-líder = %v, esperaba ErrNotGuildLeader", err)
	}

	if _, err := svc.Kick(ash, misty); err != nil {
		t.Fatalf("Kick: %v", err)
	}
	members, _ := svc.Members(guildID)
	if len(members) != 1 {
		t.Errorf("Members tras Kick = %+v, esperaba solo ash", members)
	}
}

func TestGuild_CannotKickSelf(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_a4_"+uniqueSuffix())
	guildID, err := svc.Create(ash, "Slf_"+uniqueSuffix())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Kick(ash, ash); err != ErrCannotKickSelf {
		t.Errorf("Kick de uno mismo = %v, esperaba ErrCannotKickSelf", err)
	}
	// evitar dejar el gremio colgado para el cleanup de la fixture
	svc.Leave(ash)
	_ = guildID
}

func TestGuild_LeaveTransfersLeadershipAndDisbandsWhenEmpty(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_a5_"+uniqueSuffix())
	misty := f.createCharacter(t, "gd_m5_"+uniqueSuffix())

	guildID, err := svc.Create(ash, "Xfer_"+uniqueSuffix())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Invite(ash, misty); err != nil {
		t.Fatalf("Invite: %v", err)
	}
	if err := svc.Accept(misty, guildID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	_, disbanded, err := svc.Leave(ash)
	if err != nil {
		t.Fatalf("Leave(ash): %v", err)
	}
	if disbanded {
		t.Fatalf("Leave del líder con un miembro restante disolvió el gremio, no debería")
	}
	newLeader, err := svc.LeaderOf(guildID)
	if err != nil || newLeader != misty {
		t.Errorf("LeaderOf tras Leave = (%q, %v), esperaba %q", newLeader, err, misty)
	}

	_, disbanded, err = svc.Leave(misty)
	if err != nil {
		t.Fatalf("Leave(misty): %v", err)
	}
	if !disbanded {
		t.Errorf("Leave del último miembro no marcó disbanded=true")
	}
	var count int
	db.QueryRow(`SELECT count(*) FROM guilds WHERE id = $1`, guildID).Scan(&count)
	if count != 0 {
		t.Errorf("guilds todavía tiene la fila tras disolverse, count=%d", count)
	}
}

func TestGuild_AlreadyInGuildRejectsSecondCreate(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewGuildService(db)

	ash := f.createCharacter(t, "gd_a6_"+uniqueSuffix())
	if _, err := svc.Create(ash, "First_"+uniqueSuffix()); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := svc.Create(ash, "Second_"+uniqueSuffix()); err != ErrAlreadyInGuild {
		t.Errorf("segundo Create = %v, esperaba ErrAlreadyInGuild", err)
	}
	svc.Leave(ash)
}
