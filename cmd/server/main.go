// Servidor central do VAIJUNTO. Mantem todo o estado de caronas e
// reservas em memoria e atende clientes motorista/passageiro via TCP,
// usando apenas o pacote "net" da biblioteca padrao (nenhum framework de
// RPC ou mensageria).
package main

import (
	"flag"
	"log"
	"net"
	"os"

	"vaijunto/internal/server"
	"vaijunto/internal/store"
)

func main() {
	addr := flag.String("addr", envOr("VAIJUNTO_ADDR", ":9000"), "endereco:porta para escutar (ex: :9000 ou 0.0.0.0:9000)")
	flag.Parse()

	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("falha ao escutar em %s: %v", *addr, err)
	}
	log.Printf("VAIJUNTO servidor ouvindo em %s", *addr)

	st := store.New()
	if err := server.Serve(ln, st); err != nil {
		log.Fatalf("servidor encerrado: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
