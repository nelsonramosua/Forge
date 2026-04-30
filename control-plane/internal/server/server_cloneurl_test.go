package server

import (
	"testing"

	"forge/control-plane/internal/config"
)

func TestRepoCloneURLAllowsLocalAbsolutePath(t *testing.T) {
	s := &Server{cfg: config.Config{AllowLocalRepos: true}}
	repoURL, err := s.repoCloneURL(githubPushPayload{
		Repository: struct {
			CloneURL string `json:"clone_url"`
			HTMLURL  string `json:"html_url"`
			URL      string `json:"url"`
			FullName string `json:"full_name"`
		}{
			CloneURL: "/tmp/example-repo",
			HTMLURL:  "",
			URL:      "",
			FullName: "local/smokeapp",
		},
	})
	if err != nil {
		t.Fatalf("repoCloneURL returned error: %v", err)
	}
	if repoURL != "/tmp/example-repo" {
		t.Fatalf("repoCloneURL = %q, want %q", repoURL, "/tmp/example-repo")
	}
}

func TestRepoCloneURLRejectsLocalPathsOutsideTemp(t *testing.T) {
	s := &Server{cfg: config.Config{AllowLocalRepos: true}}
	_, err := s.repoCloneURL(githubPushPayload{
		Repository: struct {
			CloneURL string `json:"clone_url"`
			HTMLURL  string `json:"html_url"`
			URL      string `json:"url"`
			FullName string `json:"full_name"`
		}{
			CloneURL: "file:///etc/passwd",
			HTMLURL:  "",
			URL:      "",
			FullName: "local/example",
		},
	})
	if err == nil {
		t.Fatal("expected repoCloneURL to reject paths outside the temp directory")
	}
}
