package main

import (
	"os"

	"github.com/gianfrancopiana/vivero/internal/vivero"
)

func main() {
	os.Exit(vivero.Run(os.Args[1:], os.Stdout, os.Stderr))
}
