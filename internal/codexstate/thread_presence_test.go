package codexstate

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestExistingThreadIDsBatchesSelectedKeys(t *testing.T) {
	home := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(home, "state_5.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE threads (id TEXT PRIMARY KEY); INSERT INTO threads VALUES ('id-0'), ('id-100'), ('unselected')"); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := range 205 {
		ids = append(ids, fmt.Sprintf("id-%d", i))
	}
	found, err := ExistingThreadIDs(context.Background(), home, ids)
	if err != nil || len(found) != 2 || !found["id-0"] || !found["id-100"] {
		t.Fatalf("found=%v error=%v", found, err)
	}
}

func TestExistingThreadIDsMissingIndexFailsClosed(t *testing.T) {
	home := t.TempDir()
	if _, err := ExistingThreadIDs(context.Background(), home, []string{"selected"}); err == nil {
		t.Fatal("missing index must not prove absence")
	}
	if _, err := os.Stat(filepath.Join(home, "state_5.sqlite")); !os.IsNotExist(err) {
		t.Fatalf("read-only verification created a database: %v", err)
	}
}
