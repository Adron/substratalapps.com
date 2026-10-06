// Command migrate applies pending migrations from migrations/.
//
//	go run ./cmd/migrate                          # local Postgres (DATABASE_URL)
//	DATABASE_BACKEND=dataapi go run ./cmd/migrate # Aurora via the Data API (deploy.yml)
//	go run ./cmd/migrate -new add_widgets         # create the next numbered file
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/Adron/substratalapps.com/internal/db/migrate"
	"github.com/Adron/substratalapps.com/internal/platform/config"
	"github.com/Adron/substratalapps.com/internal/platform/wire"
	"github.com/Adron/substratalapps.com/migrations"
)

func main() {
	newName := flag.String("new", "", "create the next numbered migration file with this name")
	flag.Parse()

	all, err := migrate.Load(migrations.FS)
	if err != nil {
		log.Fatal(err)
	}
	if *newName != "" {
		next := 1
		if len(all) > 0 {
			if _, err := fmt.Sscanf(all[len(all)-1].Version, "%d", &next); err != nil {
				log.Fatal(err)
			}
			next++
		}
		name := filepath.Join("migrations", fmt.Sprintf("%04d_%s.sql", next, *newName))
		body := fmt.Sprintf("-- %04d_%s\n--\n-- Expand-only unless this is a planned contract step; see README →\n-- Migrations: expand, then contract.\n", next, *newName)
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("created", name)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	d, err := wire.Database(ctx, cfg)
	if err != nil {
		log.Fatal(err)
	}
	defer d.Close()
	done, err := migrate.Up(ctx, d, all)
	for _, n := range done {
		fmt.Println("applied", n)
	}
	if err != nil {
		log.Fatal(err)
	}
	if len(done) == 0 {
		fmt.Println("up to date")
	}
}
