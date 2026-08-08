// Command migrate applies pending database migrations and exits. Useful in a
// deployment pipeline where the API server runs with SPP_AUTO_MIGRATE=false.
package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/sovanna2011/sugarproductionplanning/backend/internal/store/postgres"
	"github.com/sovanna2011/sugarproductionplanning/backend/migrations"
)

func main() {
	dsn := os.Getenv("SPP_DATABASE_URL")
	if dsn == "" {
		log.Fatal("SPP_DATABASE_URL is required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	store, err := postgres.New(ctx, dsn)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}
	defer store.Close()

	files, err := migrations.Load()
	if err != nil {
		log.Fatalf("load migrations: %v", err)
	}
	if err := store.Migrate(ctx, files); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	log.Printf("migrations applied: %d files checked", len(files))
}
