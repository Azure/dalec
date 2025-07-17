package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	githttp "github.com/AaronO/go-git-http"
	"github.com/AaronO/go-git-http/auth"
	"github.com/Azure/dalec/test/cmd/git_repo/passwd"
)

var errUsage = errors.New(`usage:  host [directory] [ip] [port]`)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()

	args := os.Args
	if len(args) < 4 {
		return errUsage
	}

	repo := args[1]
	addr := args[2]
	port := args[3]
	gitHandler := githttp.New(repo)
	authr := auth.Authenticator(func(ai auth.AuthInfo) (bool, error) {
		if ai.Push {
			return false, nil
		}

		if ai.Username == "x-access-token" && ai.Password == passwd.Password {
			return true, nil
		}

		return false, nil
	})

	s := http.Server{Addr: fmt.Sprintf("%s:%s", addr, port)}
	http.Handle("/", authr(gitHandler))
	defer func() {
		if err := s.Shutdown(ctx); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}()

	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Printf("unexpected server error: %s\n", err)
	}

	return nil
}
