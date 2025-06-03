package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	githttp "github.com/AaronO/go-git-http"
	"github.com/AaronO/go-git-http/auth"
)

const usage = `Usage:  host [dirctory] [ip] [port]
`

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	fmt.Println("HELLO")

	args := os.Args
	if len(args) < 4 {
		return fmt.Errorf(usage)
	}

	repo := args[1]
	addr := "0.0.0.0"
	port := args[3]
	gitHandler := githttp.New(repo)
	authr := auth.Authenticator(func(ai auth.AuthInfo) (bool, error) {
		if ai.Push {
			return false, nil
		}

		if ai.Username == "x-access-token" && ai.Password == "value" {
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
