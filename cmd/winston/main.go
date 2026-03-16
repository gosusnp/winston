// Copyright 2026 Jimmy Ma
// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/gosusnp/winston/internal/store"
)

func main() {
	dbPath := os.Getenv("WINSTON_DB_PATH")
	if dbPath == "" {
		dbPath = "/data/winston.db"
	}

	fmt.Printf("Winston starting with DB at %s\n", dbPath)

	s, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("Failed to open store: %v", err)
	}
	defer func() {
		if err := s.Close(); err != nil {
			log.Printf("Error closing store: %v", err)
		}
	}()

	fmt.Println("Store initialized successfully")
}
