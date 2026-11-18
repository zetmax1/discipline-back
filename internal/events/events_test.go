package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"discipline-backend/internal/contracttest"
)

func TestNamesMatchTheWebContract(t *testing.T) {
	dir, err := contracttest.Dir()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "events.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		EventName struct {
			Enum []string `json:"enum"`
		} `json:"event_name"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var ours []string
	for _, n := range All {
		ours = append(ours, string(n))
	}
	slices.Sort(ours)
	slices.Sort(doc.EventName.Enum)
	if !slices.Equal(ours, doc.EventName.Enum) {
		t.Fatalf("events.All and contracts/events.schema.json differ:\napi: %v\nweb: %v", ours, doc.EventName.Enum)
	}
}
