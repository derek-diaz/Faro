package main

import (
	"fmt"
	"log"
	"os"

	"github.com/derek/faro/internal/devicecatalog"
)

func main() {
	stderr := log.New(os.Stderr, "", 0)
	if len(os.Args) != 3 || os.Args[1] != "validate" {
		stderr.Println("usage: faro-device-catalog validate PATH")
		os.Exit(2)
	}

	data, err := os.ReadFile(os.Args[2])
	if err != nil {
		stderr.Printf("read catalog: %v", err)
		os.Exit(1)
	}
	catalog, err := devicecatalog.Parse(data)
	if err != nil {
		stderr.Printf("invalid catalog: %v", err)
		os.Exit(1)
	}
	if _, err := fmt.Printf("valid device catalog %s (%d definitions)\n", catalog.CatalogVersion, len(catalog.Definitions)); err != nil {
		stderr.Printf("write validation result: %v", err)
		os.Exit(1)
	}
}
