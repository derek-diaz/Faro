package main

import (
	"fmt"
	"os"

	"github.com/derek/faro/internal/devicecatalog"
)

func main() {
	if len(os.Args) != 3 || os.Args[1] != "validate" {
		fmt.Fprintln(os.Stderr, "usage: faro-device-catalog validate PATH")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		fmt.Fprintf(os.Stderr, "read catalog: %v\n", err)
		os.Exit(1)
	}
	catalog, err := devicecatalog.Parse(data)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid catalog: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("valid device catalog %s (%d definitions)\n", catalog.CatalogVersion, len(catalog.Definitions))
}
