package social

import (
	"testing"
)

func TestFriends_RequestAcceptAndList(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	ash := f.createCharacter(t, "fr_ash_"+uniqueSuffix())
	misty := f.createCharacter(t, "fr_mist_"+uniqueSuffix())

	var mistyUsername string
	if err := db.QueryRow(`SELECT username FROM accounts a JOIN characters c ON c.account_id = a.id WHERE c.id = $1`, misty).Scan(&mistyUsername); err != nil {
		t.Fatalf("resolviendo username de misty: %v", err)
	}

	req, err := svc.Request(ash, mistyUsername)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if req.ToCharacterID != misty {
		t.Errorf("ToCharacterID = %q, esperaba %q", req.ToCharacterID, misty)
	}

	// Una segunda solicitud mientras la primera sigue pendiente debe rechazarse.
	if _, err := svc.Request(ash, mistyUsername); err != ErrAlreadyRequested {
		t.Errorf("segunda Request = %v, esperaba ErrAlreadyRequested", err)
	}

	accepterAccountID, err := svc.Accept(misty, req.FromAccountID)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if accepterAccountID != req.ToAccountID {
		t.Errorf("Accept devolvió accountID %q, esperaba %q", accepterAccountID, req.ToAccountID)
	}

	// La amistad debe quedar visible en List() de AMBOS lados (bidireccional).
	ashFriends, err := svc.List(ash)
	if err != nil {
		t.Fatalf("List(ash): %v", err)
	}
	if len(ashFriends) != 1 || ashFriends[0].Username != mistyUsername {
		t.Errorf("List(ash) = %+v, esperaba a misty", ashFriends)
	}

	mistyFriends, err := svc.List(misty)
	if err != nil {
		t.Fatalf("List(misty): %v", err)
	}
	if len(mistyFriends) != 1 {
		t.Errorf("List(misty) = %+v, esperaba 1 amigo", mistyFriends)
	}
}

func TestFriends_SelfRequestRejected(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	ash := f.createCharacter(t, "fr_self_"+uniqueSuffix())
	var username string
	if err := db.QueryRow(`SELECT username FROM accounts a JOIN characters c ON c.account_id = a.id WHERE c.id = $1`, ash).Scan(&username); err != nil {
		t.Fatalf("resolviendo username: %v", err)
	}

	if _, err := svc.Request(ash, username); err != ErrSelfFriend {
		t.Errorf("Request a uno mismo = %v, esperaba ErrSelfFriend", err)
	}
}

func TestFriends_RequestUnknownUser(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	ash := f.createCharacter(t, "fr_unk_"+uniqueSuffix())
	if _, err := svc.Request(ash, "nadie_existe_con_este_username_"+uniqueSuffix()); err != ErrUserNotFound {
		t.Errorf("Request a usuario inexistente = %v, esperaba ErrUserNotFound", err)
	}
}

func TestFriends_DeclineRemovesRequestWithoutFriendship(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	ash := f.createCharacter(t, "fr_da_"+uniqueSuffix())
	misty := f.createCharacter(t, "fr_dm_"+uniqueSuffix())
	var mistyUsername string
	db.QueryRow(`SELECT username FROM accounts a JOIN characters c ON c.account_id = a.id WHERE c.id = $1`, misty).Scan(&mistyUsername)

	req, err := svc.Request(ash, mistyUsername)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if err := svc.Decline(misty, req.FromAccountID); err != nil {
		t.Fatalf("Decline: %v", err)
	}

	friends, err := svc.List(ash)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(friends) != 0 {
		t.Errorf("List(ash) tras Decline = %+v, esperaba lista vacía", friends)
	}

	// Declinar de nuevo (ya no hay solicitud pendiente) debe fallar.
	if err := svc.Decline(misty, req.FromAccountID); err != ErrRequestNotFound {
		t.Errorf("segundo Decline = %v, esperaba ErrRequestNotFound", err)
	}
}

func TestFriends_RemoveDeletesBothDirections(t *testing.T) {
	db := testDB(t)
	f := newFixture(db)
	defer f.cleanup(t)
	svc := NewService(db)

	ash := f.createCharacter(t, "fr_ra_"+uniqueSuffix())
	misty := f.createCharacter(t, "fr_rm_"+uniqueSuffix())
	var mistyUsername string
	db.QueryRow(`SELECT username FROM accounts a JOIN characters c ON c.account_id = a.id WHERE c.id = $1`, misty).Scan(&mistyUsername)

	req, err := svc.Request(ash, mistyUsername)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if _, err := svc.Accept(misty, req.FromAccountID); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	ashAccountID, err := svc.Remove(ash, req.ToAccountID)
	if err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if ashAccountID != req.FromAccountID {
		t.Errorf("Remove devolvió %q, esperaba %q", ashAccountID, req.FromAccountID)
	}

	ashFriends, _ := svc.List(ash)
	mistyFriends, _ := svc.List(misty)
	if len(ashFriends) != 0 || len(mistyFriends) != 0 {
		t.Errorf("tras Remove: List(ash)=%+v List(misty)=%+v, esperaba ambas vacías", ashFriends, mistyFriends)
	}
}
