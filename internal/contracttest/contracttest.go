// Package contracttest checks API responses against the JSON Schemas the web app
// generates from its zod contracts (discipline/contracts/<file>.schema.json).
// Each contract file maps a response name to its schema.
//
// The contracts are read from CONTRACTS_DIR, or from ../discipline/contracts next
// to this repo. A missing file fails the test: it never skips.
package contracttest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Validate fails the test unless body matches the schema `name` in contracts/<file>.schema.json.
func Validate(t testing.TB, file, name string, body []byte) {
	t.Helper()
	if err := Check(file, name, body); err != nil {
		t.Fatal(err)
	}
}

// Check reports whether body matches the schema `name` in contracts/<file>.schema.json.
func Check(file, name string, body []byte) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	where := file + ".schema.json#" + name
	raw, err := fs.ReadFile(os.DirFS(dir), file+".schema.json")
	if err != nil {
		return fmt.Errorf("contract %s not found in %s (check out discipline/ next to discipline-back/ or set CONTRACTS_DIR): %w", where, dir, err)
	}
	var schemas map[string]json.RawMessage
	if err := json.Unmarshal(raw, &schemas); err != nil {
		return fmt.Errorf("contract %s: %w", where, err)
	}
	schemaJSON, ok := schemas[name]
	if !ok {
		return fmt.Errorf("contract %s: no schema named %q", where, name)
	}
	schemaDoc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaJSON))
	if err != nil {
		return fmt.Errorf("contract %s: %w", where, err)
	}
	url := "https://contracts.invalid/" + file + "/" + name + ".json"
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, schemaDoc); err != nil {
		return fmt.Errorf("contract %s: %w", where, err)
	}
	schema, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("contract %s: %w", where, err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("response for %s is not JSON: %w", where, err)
	}
	if err := schema.Validate(doc); err != nil {
		return fmt.Errorf("response does not match %s: %w", where, err)
	}
	return nil
}

// Dir returns CONTRACTS_DIR, or the web repo's contracts/ next to this module's root.
func Dir() (string, error) {
	if dir := os.Getenv("CONTRACTS_DIR"); dir != "" {
		return dir, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return filepath.Join(filepath.Dir(dir), "discipline", "contracts"), nil
		}
		if filepath.Dir(dir) == dir {
			return "", errors.New("contracttest: go.mod not found above " + wd + "; set CONTRACTS_DIR")
		}
	}
}
