package contracttest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeContract writes a one-schema contract file into a temp CONTRACTS_DIR.
func writeContract(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	schema := `{"thing":{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",
		"properties":{"n":{"type":"number"}},"required":["n"]}}`
	if err := os.WriteFile(filepath.Join(dir, "demo.schema.json"), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CONTRACTS_DIR", dir)
}

func TestCheck_AcceptsAMatchingBody(t *testing.T) {
	writeContract(t)
	if err := Check("demo", "thing", []byte(`{"n":1,"extra":true}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCheck_RejectsAMismatch(t *testing.T) {
	writeContract(t)
	err := Check("demo", "thing", []byte(`{"n":"one"}`))
	if err == nil || !strings.Contains(err.Error(), "demo.schema.json#thing") {
		t.Fatalf("want a schema error naming the contract, got %v", err)
	}
}

func TestCheck_UnknownSchemaNameFails(t *testing.T) {
	writeContract(t)
	if err := Check("demo", "missing", []byte(`{}`)); err == nil {
		t.Fatal("want an error for a schema name the file lacks")
	}
}

func TestCheck_MissingFileFails(t *testing.T) {
	t.Setenv("CONTRACTS_DIR", t.TempDir())
	if err := Check("demo", "thing", []byte(`{}`)); err == nil || !strings.Contains(err.Error(), "CONTRACTS_DIR") {
		t.Fatalf("want an error pointing at CONTRACTS_DIR, got %v", err)
	}
}

func TestDir_DefaultsToTheWebRepoNextToThisOne(t *testing.T) {
	t.Setenv("CONTRACTS_DIR", "")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "contracts" || filepath.Base(filepath.Dir(dir)) != "discipline" {
		t.Fatalf("want …/discipline/contracts, got %s", dir)
	}
}
