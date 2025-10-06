package dalec

import (
	"bytes"
	"strings"
	"testing"
)

func TestSourceGitDocWithoutDotGit(t *testing.T) {
	// This test verifies that git URLs without .git extension work correctly
	src := SourceGit{
		URL:    "https://github.com/user/repo", // URL without .git
		Commit: "abc123",
	}

	buf := bytes.NewBuffer(nil)
	src.doc(buf, "test-source")
	
	output := buf.String()
	if output == "" {
		t.Error("doc() produced empty output")
	}
	
	// Check that the output contains expected content
	if !strings.Contains(output, "Generated from a git repository:") {
		t.Error("doc() output missing expected text")
	}
	
	// Check that the original URL is used as the remote
	if !strings.Contains(output, "https://github.com/user/repo") {
		t.Error("doc() output should contain the original URL when parsing fails")
	}
	
	// Check that the commit is included
	if !strings.Contains(output, "abc123") {
		t.Error("doc() output missing commit reference")
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
	if !strings.Contains(output, "Generated from a git repository:") {
		t.Error("doc() output missing expected text")
	}
	
	// Check that some form of the URL is present (either original or parsed remote)
	if !strings.Contains(output, "github.com/user/repo") {
		t.Error("doc() output should contain repository information")
	}
	
	// Check that the commit is included
	if !strings.Contains(output, "abc123") {
		t.Error("doc() output missing commit reference")
	}
}

func TestSourceGitDocEdgeCases(t *testing.T) {
	tests := []struct {
		name   string
		url    string
		commit string
	}{
		{
			name:   "SSH without .git",
			url:    "git@github.com:user/repo",
			commit: "def456",
		},
		{
			name:   "SSH with .git",
			url:    "git@github.com:user/repo.git",
			commit: "ghi789",
		},
		{
			name:   "HTTPS with port without .git",
			url:    "https://example.com:8080/user/repo",
			commit: "jkl012",
		},
		{
			name:   "HTTPS with port with .git",
			url:    "https://example.com:8080/user/repo.git",
			commit: "mno345",
		},
		{
			name:   "Non-standard format",
			url:    "some-unusual-git-url-format",
			commit: "pqr678",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := SourceGit{
				URL:    tt.url,
				Commit: tt.commit,
			}

			buf := bytes.NewBuffer(nil)
			src.doc(buf, "test-source")
			
			output := buf.String()
			if output == "" {
				t.Error("doc() produced empty output")
			}
			
			// Check that the output contains expected content
			if !strings.Contains(output, "Generated from a git repository:") {
				t.Errorf("doc() output missing expected text, got: %s", output)
			}
			
			// Check that some form of the URL is present in the remote line
			if !strings.Contains(output, "Remote:") {
				t.Error("doc() output missing Remote line")
			}
			
			// Check that the commit is included
			if !strings.Contains(output, tt.commit) {
				t.Errorf("doc() output missing commit reference %s, got: %s", tt.commit, output)
			}
		})
	}
}