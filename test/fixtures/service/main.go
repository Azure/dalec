package main

import (
	"fmt"
	"net/http"
)

func main() {
	var mux = http.NewServeMux()
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		fmt.Fprintln(w, "Phony Service")
	}))

	if err := http.ListenAndServe(":8080", mux); err != nil {
		panic(err)
	}
}
