//go:build debug

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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

	tracerPid := "0"
	for tracerPid == "0" {
		b, err := os.Open("/proc/self/status")
		if err != nil {
			return err
		}

		s := bufio.NewScanner(b)
		for s.Scan() {
			start, end, ok := strings.Cut(s.Text(), ":\t")
			if !ok {
				continue
			}

			if start != "TracerPid" {
				continue
			}

			tracerPid = end
			break
		}
	}

	return nil
}
