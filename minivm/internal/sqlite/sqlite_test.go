package sqlite

import "testing"

func TestBindingsAreDataAndRollbackWorks(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Exec("CREATE TABLE items(value TEXT)"); err != nil {
		t.Fatal(err)
	}
	value := "quote'; DROP TABLE items; --\x00tail"
	if err := db.Exec("INSERT INTO items VALUES(?)", value); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query("SELECT value FROM items")
	if err != nil || len(rows) != 1 || rows[0][0] != value {
		t.Fatalf("round trip: %#v, %v", rows, err)
	}
	if err := db.Exec("INSERT INTO items VALUES(?)"); err == nil {
		t.Fatal("missing parameter was accepted")
	}
	if err := db.Exec("BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("DELETE FROM items"); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	rows, err = db.Query("SELECT value FROM items")
	if err != nil || len(rows) != 1 {
		t.Fatalf("rollback: %#v, %v", rows, err)
	}
}
