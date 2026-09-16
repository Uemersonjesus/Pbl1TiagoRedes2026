// Cliente motorista: aplicacao de terminal que permite autenticar-se,
// publicar uma carona (rota, data, assentos, preco por trecho), consultar
// as caronas publicadas com os passageiros confirmados por trecho, e
// cancelar uma carona.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"vaijunto/internal/clientio"
)

func main() {
	addr := flag.String("addr", envOr("VAIJUNTO_ADDR", "localhost:9000"), "endereco:porta do servidor")
	flag.Parse()

	entrada := bufio.NewReader(os.Stdin)

	fmt.Printf("Conectando ao servidor VAIJUNTO em %s...\n", *addr)
	conn, err := clientio.Conectar(*addr)
	if err != nil {
		fmt.Println("erro ao conectar:", err)
		os.Exit(1)
	}
	defer conn.Fechar()

	if !autenticar(conn, entrada) {
		os.Exit(1)
	}

	for {
		fmt.Println("\n=== VAIJUNTO - Cliente Motorista ===")
		fmt.Println("1) Publicar carona")
		fmt.Println("2) Listar minhas caronas")
		fmt.Println("3) Ver passageiros de uma carona (por trecho)")
		fmt.Println("4) Cancelar carona")
		fmt.Println("0) Sair")
		fmt.Print("> ")
		opcao := lerLinha(entrada)

		switch opcao {
		case "1":
			publicarCarona(conn, entrada)
		case "2":
			listarCaronas(conn)
		case "3":
			detalheCarona(conn, entrada)
		case "4":
			cancelarCarona(conn, entrada)
		case "0":
			conn.Enviar("SAIR")
			return
		default:
			fmt.Println("opcao invalida")
		}
	}
}

func autenticar(conn *clientio.Conexao, entrada *bufio.Reader) bool {
	fmt.Print("login: ")
	login := lerLinha(entrada)
	fmt.Print("senha: ")
	senha := lerLinha(entrada)
	if err := conn.Enviar("LOGIN", login, senha); err != nil {
		fmt.Println("erro de conexao:", err)
		return false
	}
	resp, err := conn.LerLinha()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return false
	}
	if ehErro, e := clientio.EhErro(resp); ehErro {
		fmt.Println(e)
		return false
	}
	fmt.Printf("autenticado como %s\n", login)
	return true
}

func publicarCarona(conn *clientio.Conexao, entrada *bufio.Reader) {
	fmt.Print("rota (cidades separadas por virgula, em ordem): ")
	rota := lerLinha(entrada)
	fmt.Print("data (AAAA-MM-DD): ")
	data := lerLinha(entrada)
	fmt.Print("hora (HH:MM): ")
	hora := lerLinha(entrada)
	fmt.Print("quantidade de assentos: ")
	assentos := lerLinha(entrada)
	fmt.Print("preco por trecho: ")
	preco := lerLinha(entrada)

	if err := conn.Enviar("PUBLICAR", rota, data, hora, assentos, preco); err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	resp, err := conn.LerLinha()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	if ehErro, e := clientio.EhErro(resp); ehErro {
		fmt.Println(e)
		return
	}
	fmt.Printf("carona publicada com sucesso, id=%s\n", resp[2])
}

func listarCaronas(conn *clientio.Conexao) {
	if err := conn.Enviar("LISTAR_CARONAS"); err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	if len(linhas) == 0 {
		fmt.Println("nenhuma carona publicada ainda")
		return
	}
	for _, c := range linhas {
		// CARONA|id|rota|data|hora|assentosTotal|preco|cancelada
		status := "ativa"
		if len(c) > 7 && c[7] == "1" {
			status = "CANCELADA"
		}
		fmt.Printf("- %s | rota: %s | %s %s | assentos: %s | preco/trecho: %s | %s\n",
			c[1], strings.ReplaceAll(c[2], ",", " -> "), c[3], c[4], c[5], c[6], status)
	}
}

func detalheCarona(conn *clientio.Conexao, entrada *bufio.Reader) {
	fmt.Print("id da carona: ")
	id := lerLinha(entrada)
	if err := conn.Enviar("DETALHE_CARONA", id); err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	if len(linhas) > 0 {
		if ehErro, e := clientio.EhErro(linhas[0]); ehErro {
			fmt.Println(e)
			return
		}
	}
	for _, t := range linhas {
		// TRECHO|idx|origem|destino|assento|status
		fmt.Printf("trecho %s: %s -> %s | assento %s: %s\n", t[1], t[2], t[3], t[4], t[5])
	}
}

func cancelarCarona(conn *clientio.Conexao, entrada *bufio.Reader) {
	fmt.Print("id da carona a cancelar: ")
	id := lerLinha(entrada)
	if err := conn.Enviar("CANCELAR_CARONA", id); err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	resp, err := conn.LerLinha()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	if ehErro, e := clientio.EhErro(resp); ehErro {
		fmt.Println(e)
		return
	}
	fmt.Println("carona cancelada")
}

func lerLinha(r *bufio.Reader) string {
	linha, _ := r.ReadString('\n')
	return strings.TrimSpace(linha)
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
