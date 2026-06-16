// Command gtd-server serves the guided-GTD web UI and JSON API over a
// todo.txt store. It binds to localhost by default; on the t480 it sits behind
// `tailscale serve`, which terminates TLS and is the tailnet auth boundary.
package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/kevinywlui/nix-config/apps/gtd/internal/todotxt"
)

func main() {
	addr := flag.String("addr", envOr("GTD_ADDR", "127.0.0.1:8730"), "listen address")
	dir := flag.String("dir", envOr("GTD_DIR", "."), "todo.txt data directory")
	flag.Parse()

	store, err := todotxt.New(*dir)
	if err != nil {
		log.Fatalf("gtd: opening data dir %q: %v", *dir, err)
	}

	srv, err := newServer(store)
	if err != nil {
		log.Fatalf("gtd: %v", err)
	}

	log.Printf("gtd-server listening on %s, data in %s", *addr, *dir)
	if err := http.ListenAndServe(*addr, srv); err != nil {
		log.Fatalf("gtd: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
