package main

import (
	"fmt"

	"github.com/stretchr/testify/assert"
)

func main() {
	msg := "This is a dummy test from module2"
	fmt.Println(msg)
	assert.True(nil, true, msg)
}
