package main

import (
	"fmt"
	"os"

	"github.com/nccapo/stvena/internal/app"
)

func main() {
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "stvena:", err)
		os.Exit(1)
	}
}
