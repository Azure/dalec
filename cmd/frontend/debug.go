//go:build debug

package main

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"os/signal"
	"syscall"
)

func waitForDebug(ctx context.Context) error {
	pid := fmt.Sprintf("%d", syscall.Getpid())
	cmd := exec.Command(
		"dlv",
		"attach",
		"--api-version=2",
		"--headless",
		"--listen=unix:/dlv.sock",
		"--allow-non-terminal-interactive",
		pid,
	)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return err
	}

	go func() {
		if err := cmd.Wait(); err != nil {
			panic(err)
		}
	}()

	s, cancel := signal.NotifyContext(ctx, syscall.SIGCONT)
	defer cancel()
	<-s.Done()

	return nil
}
