//go:build !debug

package main

import "context"

func waitForDebug(ctx context.Context) error {
	return nil
}
