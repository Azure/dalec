package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

func main() {
	args := os.Args[1:]

	var (
		stdin, stdout, stderr int
	)

	if p := os.Getenv("STDIN_FILE"); p != "" {
		f, err := os.Open(p)
		if err != nil {
			panic(fmt.Errorf("%q: %w", p, err))
		}
		stdin = int(f.Fd())
		os.Unsetenv("STDIN_FILE")
	}

	if p := os.Getenv("STDOUT_FILE"); p != "" {
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			panic(fmt.Errorf("%q: %w", p, err))
		}
		stdout = int(f.Fd())
		os.Unsetenv("STDOUT_FILE")
	}

	if p := os.Getenv("STDERR_FILE"); p != "" {
		f, err := os.OpenFile(p, os.O_WRONLY, 0)
		if err != nil {
			panic(fmt.Errorf("%q: %w", p, err))
		}
		stderr = int(f.Fd())
		os.Unsetenv("STDERR_FILE")
	}

	if stdin != 0 {
		if err := unix.Dup2(stdin, 0); err != nil {
			panic(err)
		}
	}

	if stdout != 0 {
		if err := unix.Dup2(stdout, 1); err != nil {
			panic(err)
		}
	}

	if stderr != 0 {
		if err := unix.Dup2(stderr, 2); err != nil {
			panic(err)
		}
	}

	cmd, err := exec.LookPath(args[0])
	if err != nil {
		panic(err)
	}

	if err := unix.Exec(cmd, args, os.Environ()); err != nil {
		panic(fmt.Errorf("%q: %w", strings.Join(args, " "), err))
	}
}
