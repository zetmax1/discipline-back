package main

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"testing"
)

// pinnedFrom matches "FROM image:version@sha256:<64 hex>" with an explicit version tag.
var pinnedFrom = regexp.MustCompile(`^FROM\s+[a-z0-9./-]+:[0-9][A-Za-z0-9._-]*@sha256:[a-f0-9]{64}(\s+AS\s+\S+)?$`)

func TestDockerfile_PinnedImagesAndNonRootUser(t *testing.T) {
	f, err := os.Open("../../Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var froms []string
	lastUser := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "FROM "):
			froms = append(froms, line)
			lastUser = "" // USER resets per stage
		case strings.HasPrefix(line, "USER "):
			lastUser = strings.TrimSpace(strings.TrimPrefix(line, "USER "))
		}
	}
	if len(froms) == 0 {
		t.Fatal("no FROM lines found")
	}
	for _, from := range froms {
		if !pinnedFrom.MatchString(from) || strings.Contains(from, ":latest") {
			t.Errorf("base image must be pinned by version and digest: %q", from)
		}
	}
	user := strings.SplitN(lastUser, ":", 2)[0]
	if user == "" || user == "root" || user == "0" {
		t.Errorf("final stage must run as a non-root USER, got %q", lastUser)
	}
}
