package dalec

import (
	"bytes"
	"testing"
)

func TestSourceGitDocWithoutDotGit(t *testing.T) {
	// This test reproduces the issue where git URLs without .git extension cause a panic
	src := SourceGit{
		URL:    "https://github.com/user/repo", // URL without .git
		Commit: "abc123",
	}

	// This should not panic and should provide a meaningful error message
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("doc() panicked with git URL without .git: %v", r)
		}
	}()

	buf := bytes.NewBuffer(nil)
	src.doc(buf, "test-source")
	
	// If we reach here, the doc function didn't panic
	output := buf.String()
	if output == "" {
		t.Error("doc() produced empty output")
	}
}

func TestSourceGitDocWithDotGit(t *testing.T) {
	// This test verifies that URLs with .git work correctly
	src := SourceGit{
		URL:    "https://github.com/user/repo.git", // URL with .git
		Commit: "abc123",
	}

	buf := bytes.NewBuffer(nil)
	src.doc(buf, "test-source")
	
	output := buf.String()
	if output == "" {
		t.Error("doc() produced empty output")
	}
	
	// Check that the output contains expected content
	if !bytes.Contains(buf.Bytes(), []byte("Generated from a git repository:")) {
		t.Error("doc() output missing expected text")
	}
}