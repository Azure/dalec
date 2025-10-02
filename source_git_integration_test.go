package dalec

import (
	"bytes"
	"testing"
)

func TestSourceGitDocIntegration(t *testing.T) {
	// This test verifies that the original panic is fixed by simulating 
	// the code path that would have panicked before the fix
	src := SourceGit{
		URL:    "https://github.com/user/repo", // URL without .git - would panic before fix
		Commit: "main",
	}

	source := Source{
		Git: &src,
	}

	// This simulates what would happen in RPM template generation
	reader := source.Doc("main")
	buf := bytes.NewBuffer(nil)
	_, err := buf.ReadFrom(reader)
	if err != nil {
		t.Errorf("Failed to read source documentation: %v", err)
	}

	output := buf.String()
	if len(output) == 0 {
		t.Error("Expected source documentation output, got empty string")
	}

	// Verify it contains the expected documentation
	if !bytes.Contains(buf.Bytes(), []byte("Generated from a git repository:")) {
		t.Error("Source documentation missing expected content")
	}

	// Verify the URL is preserved in some form
	if !bytes.Contains(buf.Bytes(), []byte("github.com/user/repo")) {
		t.Error("Source documentation missing repository URL")
	}
}