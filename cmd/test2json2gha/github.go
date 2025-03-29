package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strconv"
	"strings"

	"github.com/google/go-github/v70/github"
	"github.com/pkg/errors"
	"golang.org/x/exp/slog"
	"golang.org/x/oauth2"
)

type annotator interface {
	WriteAnnotations(ctx context.Context, h *resultsHandler, modName string) error
}

type consoleAnnotator struct {
	out io.Writer
}

func (c *consoleAnnotator) WriteAnnotations(_ context.Context, h *resultsHandler, modName string) error {
	buf := &strings.Builder{}

	for _, tr := range h.results {
		if tr.name == "" {
			continue
		}
		if !tr.failed {
			continue
		}

		_, err := tr.output.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "error seeking test output")
		}
		scanner := bufio.NewScanner(tr.output)
		var file, line string

		buf.Reset()
		for scanner.Scan() {
			text := scanner.Text()

			f, l, ok := getTestOutputLoc(text)
			if ok {
				file = f
				line = l
			}

			// Add url-encoded new line to the output
			// This is needed so the annotation is displayed correctly in GitHub
			buf.WriteString(text + "%0A")
		}

		if err := scanner.Err(); err != nil {
			return errors.Wrap(err, "error reading test output")
		}

		pkg := strings.TrimPrefix(tr.pkg, modName)
		pkg = strings.TrimPrefix(pkg, "/")
		file = path.Join(pkg, file)

		group := pkg
		if group != "" && tr.name != "" {
			group += "."
		}
		group += tr.name

		fmt.Fprintln(c.out, "::group::"+group)
		fmt.Fprintf(c.out, "::error file=%s,line=%s::%s\n", file, line, buf)
		fmt.Fprintln(c.out, "::endgroup::")
	}

	fmt.Fprint(c.out, buf.String())

	return nil
}

type checkAnnotator struct {
	gh          *github.Client
	run         *github.CheckRun
	owner, repo string
}

func newAnnotator(ctx context.Context, name string, console io.Writer) annotator {
	tokenVal := os.Getenv("GITHUB_TOKEN")
	if tokenVal == "" {
		slog.Warn("GITHUB_TOKEN not set, using console output")
		return &consoleAnnotator{console}
	}

	repo := os.Getenv("GITHUB_REPOSITORY")
	if repo == "" {
		slog.Warn("GITHUB_REPOSITORY not set, using console output")
		return &consoleAnnotator{console}
	}

	owner, repo, ok := strings.Cut(repo, "/")
	if !ok {
		slog.Warn("GITHUB_REPOSITORY not in owner/repo format, using console output")
		return &consoleAnnotator{console}
	}

	token := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tokenVal})
	gh := github.NewClient(oauth2.NewClient(ctx, token))

	checkRun, _, err := gh.Checks.CreateCheckRun(ctx, owner, repo, github.CreateCheckRunOptions{
		Name:   name,
		Status: github.Ptr("in_progress"),
	})
	if err != nil {
		slog.Error("Error creating check run, falling back to console output", "err", err)
		return &consoleAnnotator{console}
	}

	return &checkAnnotator{
		gh:    gh,
		run:   checkRun,
		owner: owner,
		repo:  repo,
	}
}

func (c *checkAnnotator) WriteAnnotations(ctx context.Context, h *resultsHandler, modName string) error {
	buf := &strings.Builder{}

	var failed bool
	for _, tr := range h.results {
		if tr.name == "" {
			continue
		}
		if !tr.failed {
			continue
		}
		failed = true

		_, err := tr.output.Seek(0, io.SeekStart)
		if err != nil {
			return errors.Wrap(err, "error seeking test output")
		}
		scanner := bufio.NewScanner(tr.output)

		var (
			file string
			line int
		)

		buf.Reset()
		for scanner.Scan() {
			text := scanner.Text()

			f, l, ok := getTestOutputLoc(text)
			if ok {
				file = f
				line = convertLine(l)
			}

			buf.WriteString(text + "\n")
		}

		if err := scanner.Err(); err != nil {
			return errors.Wrap(err, "error reading test output")
		}

		pkg := strings.TrimPrefix(tr.pkg, modName)
		pkg = strings.TrimPrefix(pkg, "/")
		file = path.Join(pkg, file)

		c.run, _, err = c.gh.Checks.UpdateCheckRun(ctx, c.owner, c.repo, c.run.GetID(), github.UpdateCheckRunOptions{
			Output: &github.CheckRunOutput{
				Title: github.Ptr(tr.name),
				Annotations: []*github.CheckRunAnnotation{
					{
						Path:            github.Ptr(file),
						StartLine:       github.Ptr(line),
						AnnotationLevel: github.Ptr("error"),
						Message:         github.Ptr("Test failed"),
						RawDetails:      github.Ptr(buf.String()),
					},
				},
			},
		})
		if err != nil {
			return errors.Wrap(err, "error updating check run")
		}
	}

	var conclusion string
	if failed {
		conclusion = "failure"
	} else {
		conclusion = "success"
	}

	_, _, err := c.gh.Checks.UpdateCheckRun(ctx, c.owner, c.repo, c.run.GetID(), github.UpdateCheckRunOptions{
		Status:     github.Ptr("completed"),
		Conclusion: github.Ptr(conclusion),
	})

	if err != nil {
		return errors.Wrap(err, "error completing check run")
	}
	return err
}

func convertLine(s string) int {
	line, err := strconv.Atoi(s)
	if err != nil {
		slog.Error("Error converting line number to int", "line", s, "error", err)
		return 0
	}
	return line
}

func getTestOutputLoc(s string) (string, string, bool) {
	file, other, ok := strings.Cut(s, ":")
	if !ok {
		return "", "", false
	}
	line, _, ok := strings.Cut(other, ":")
	if !ok {
		return "", "", false
	}

	return strings.TrimSpace(file), line, true
}
