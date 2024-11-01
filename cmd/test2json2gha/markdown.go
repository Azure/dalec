package main

import (
	"fmt"
	"io"
	"strings"
)

func mdBold(v string) string {
	return "**" + v + "**"
}

func mdDetails(s string) string {
	return fmt.Sprintf("\n<details>\n%s\n</details>\n", s)
}

func mdSummary(s string) string {
	return "<summary>" + s + "</summary>\n"
}

func mdPreformat(s string) string {
	return fmt.Sprintf("\n```\n%s\n```\n", s)
}

type nopWriteCloser struct {
	io.Writer
}

func mdLog(head string, content fmt.Stringer) string {
	sb := &strings.Builder{}
	sb.WriteString(mdSummary(head))
	sb.WriteString(mdPreformat(content.String()))
	return mdDetails(sb.String())
}
