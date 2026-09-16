// Gateway HTTP para o VAIJUNTO: serve uma interface web (HTML/CSS/JS
// estaticos, embutidos no binario) e expoe uma API JSON que traduz cada
// acao do navegador para o protocolo textual do VAIJUNTO sobre TCP (ver
// internal/proto e internal/clientio — os mesmos pacotes usados pelos
// clientes de terminal cmd/motorista e cmd/passageiro).
//
// Este processo nao guarda estado de negocio nenhum: caronas, reservas e
// autenticacao continuam vivendo inteiramente no servidor central
// (cmd/server). O gateway apenas mantem, em memoria, a associacao entre
// o cookie de sessao do navegador e a credencial usada para reabrir uma
// conexao TCP por requisicao (ver sessions.go).
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"time"
)

//go:embed static
var staticFiles embed.FS

func main() {
	backendAddr := flag.String("addr", envOr("VAIJUNTO_ADDR", "localhost:9000"), "endereco:porta do servidor VAIJUNTO")
	listenAddr := flag.String("listen", envOr("WEBUI_ADDR", ":8080"), "endereco:porta para servir a interface web")
	flag.Parse()

	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("falha ao preparar arquivos estaticos: %v", err)
	}

	srv := &servidor{backendAddr: *backendAddr, sessoes: novoSessionStore()}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))

	mux.HandleFunc("/api/me", srv.handleMe)
	mux.HandleFunc("/api/login", somentePost(srv.handleLogin))
	mux.HandleFunc("/api/logout", somentePost(srv.handleLogout))

	mux.HandleFunc("/api/motorista/publicar", somentePost(srv.handlePublicar))
	mux.HandleFunc("/api/motorista/caronas", somentePost(srv.handleListarCaronas))
	mux.HandleFunc("/api/motorista/carona/detalhe", somentePost(srv.handleDetalheCarona))
	mux.HandleFunc("/api/motorista/carona/cancelar", somentePost(srv.handleCancelarCarona))

	mux.HandleFunc("/api/passageiro/buscar", somentePost(srv.handleBuscar))
	mux.HandleFunc("/api/passageiro/reservar", somentePost(srv.handleReservar))
	mux.HandleFunc("/api/passageiro/reservas", somentePost(srv.handleMinhasReservas))
	mux.HandleFunc("/api/passageiro/reserva/cancelar", somentePost(srv.handleCancelarReserva))

	httpSrv := &http.Server{
		Addr:              *listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("interface web VAIJUNTO ouvindo em %s (servidor VAIJUNTO em %s)", *listenAddr, *backendAddr)
	if err := httpSrv.ListenAndServe(); err != nil {
		log.Fatalf("gateway web encerrado: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
