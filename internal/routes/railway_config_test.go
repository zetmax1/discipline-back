package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"testing"
)

// railway.json at the repo root is Railway's config-as-code; it overrides the dashboard.
type railwayConfig struct {
	Build struct {
		Builder string `json:"builder"`
	} `json:"build"`
	Deploy struct {
		StartCommand     string   `json:"startCommand"`
		HealthcheckPath  string   `json:"healthcheckPath"`
		PreDeployCommand []string `json:"preDeployCommand"`
	} `json:"deploy"`
}

func readRailwayConfig(t *testing.T) railwayConfig {
	t.Helper()
	raw, err := os.ReadFile("../../railway.json")
	if err != nil {
		t.Fatalf("railway.json: %v", err)
	}
	var cfg railwayConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("railway.json: %v", err)
	}
	return cfg
}

func TestRailwayConfig_HealthcheckPathIsTheHealthRoute(t *testing.T) {
	cfg := readRailwayConfig(t)
	r := newTestRouter(false)
	SetupHealth(r, map[string]HealthCheck{"database": ok, "redis": ok})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, cfg.Deploy.HealthcheckPath, nil))
	if cfg.Deploy.HealthcheckPath != "/health" || w.Code != http.StatusOK {
		t.Fatalf("healthcheckPath %q answered %d; want /health answering 200", cfg.Deploy.HealthcheckPath, w.Code)
	}
}

// Railway runs a Dockerfile service's start command in exec form (no shell), so it
// must be the absolute path of the binary the Dockerfile builds.
func TestRailwayConfig_StartCommandRunsTheDockerfileBinary(t *testing.T) {
	cfg := readRailwayConfig(t)
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`go build .*-o (\S+) \./cmd/server`).FindSubmatch(dockerfile)
	if m == nil {
		t.Fatal("Dockerfile: no `go build -o <binary> ./cmd/server` line")
	}
	if cfg.Build.Builder != "DOCKERFILE" || cfg.Deploy.StartCommand != string(m[1]) {
		t.Fatalf("builder %q, startCommand %q; want DOCKERFILE and %q", cfg.Build.Builder, cfg.Deploy.StartCommand, m[1])
	}
}

// Railway runs the pre-deploy command in the new image, once, before the new version takes
// traffic, so the schema is migrated by the same build that will serve it (the server refuses
// to start on an old schema). Like the start command it runs without a shell, so it is the
// absolute path of a binary the Dockerfile builds and keeps in the runtime stage.
func TestRailwayConfig_PreDeployCommandMigratesWithTheDockerfileBinary(t *testing.T) {
	cfg := readRailwayConfig(t)
	dockerfile, err := os.ReadFile("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`go build .*-o (\S+) \./cmd/migrate`).FindSubmatch(dockerfile)
	if m == nil {
		t.Fatal("Dockerfile: no `go build -o <binary> ./cmd/migrate` line")
	}
	binary := string(m[1])
	want := []string{binary, "up"}
	if len(cfg.Deploy.PreDeployCommand) != len(want) || cfg.Deploy.PreDeployCommand[0] != want[0] ||
		cfg.Deploy.PreDeployCommand[1] != want[1] {
		t.Fatalf("preDeployCommand %v; want %v", cfg.Deploy.PreDeployCommand, want)
	}
	if !regexp.MustCompile(`COPY --from=builder ` + regexp.QuoteMeta(binary) + `\b`).Match(dockerfile) {
		t.Fatalf("Dockerfile: the runtime stage does not copy %s from the builder", binary)
	}
}
