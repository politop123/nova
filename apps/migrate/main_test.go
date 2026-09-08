package main

import (
	"os"
	"reflect"
	"testing"
)

func TestMigrationFileNames(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"202609080002_next.sql", "README.md", "202609080001_first.sql"} {
		if err := os.WriteFile(dir+"/"+name, []byte("-- test"), 0o600); err != nil {
			t.Fatalf("write test file: %v", err)
		}
	}
	if err := os.Mkdir(dir+"/nested.sql", 0o700); err != nil {
		t.Fatalf("make test dir: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read test dir: %v", err)
	}

	got := migrationFileNames(entries)
	want := []string{"202609080001_first.sql", "202609080002_next.sql"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migrationFileNames() = %#v, want %#v", got, want)
	}
}
