// Command admin is the one-time, non-API admin tool (NFR → Authentication
// → Bootstrapping): it creates the first superadmin directly against the
// database, which the public API can't do by design.
//
//	go run ./cmd/admin bootstrap -email you@example.com   # password from ADMIN_PASSWORD or stdin
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/Adron/substratalapps.com/internal/platform/wire"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "bootstrap" {
		fmt.Fprintln(os.Stderr, "usage: admin bootstrap -email <address>")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("bootstrap", flag.ExitOnError)
	email := fs.String("email", "", "the superadmin's email")
	_ = fs.Parse(os.Args[2:])
	if *email == "" {
		log.Fatal("admin: -email is required")
	}
	password := os.Getenv("ADMIN_PASSWORD")
	if password == "" {
		fmt.Fprint(os.Stderr, "password (12+ characters): ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		password = strings.TrimSpace(line)
	}
	ctx := context.Background()
	base, err := wire.NewBase(ctx)
	if err != nil {
		log.Fatal(err)
	}
	id, err := base.Server(nil).BootstrapSuperadmin(ctx, *email, password)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("superadmin", id, *email)
}
