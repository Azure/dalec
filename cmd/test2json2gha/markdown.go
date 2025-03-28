package main

import (
	"fmt"
	"io"
)

func mdBold(v string) string {
	return "**" + v + "**"
}

func mdPreformat(s string) string {
	return fmt.Sprintf("\n```\n%s```\n", s)
}

type nopWriteCloser struct {
	io.Writer
}

func (c *nopWriteCloser) Close() error { return nil }
