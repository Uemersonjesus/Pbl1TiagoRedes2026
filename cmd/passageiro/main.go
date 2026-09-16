// Cliente passageiro: aplicacao de terminal que permite autenticar-se,
// buscar itinerarios entre origem/destino numa data, confirmar a reserva
// de um itinerario (um ou mais trechos, possivelmente de motoristas
// diferentes), e consultar ou cancelar reservas.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"vaijunto/internal/clientio"
	"vaijunto/internal/proto"
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

	// itinerarios da ultima busca, para permitir reservar pelo indice
	// exibido ao usuario sem precisar redigitar cidades/carona.
	var ultimaBusca [][]string

	for {
		fmt.Println("\n=== VAIJUNTO - Cliente Passageiro ===")
		fmt.Println("1) Buscar itinerarios")
		fmt.Println("2) Reservar itinerario (da ultima busca)")
		fmt.Println("3) Minhas reservas")
		fmt.Println("4) Cancelar reserva")
		fmt.Println("0) Sair")
		fmt.Print("> ")
		opcao := lerLinha(entrada)

		switch opcao {
		case "1":
			ultimaBusca = buscar(conn, entrada)
		case "2":
			reservar(conn, entrada, ultimaBusca)
		case "3":
			minhasReservas(conn)
		case "4":
			cancelarReserva(conn, entrada)
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

// buscar pede origem/destino/data, exibe os itinerarios encontrados e
// devolve as linhas ITINERARIO recebidas (para uso posterior por
// reservar).
func buscar(conn *clientio.Conexao, entrada *bufio.Reader) [][]string {
	fmt.Print("cidade de origem: ")
	origem := lerLinha(entrada)
	fmt.Print("cidade de destino: ")
	destino := lerLinha(entrada)
	fmt.Print("data (AAAA-MM-DD): ")
	data := lerLinha(entrada)

	if err := conn.Enviar("BUSCAR", origem, destino, data); err != nil {
		fmt.Println("erro de conexao:", err)
		return nil
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return nil
	}
	if len(linhas) == 0 {
		fmt.Println("nenhum itinerario encontrado")
		return nil
	}
	for _, it := range linhas {
		// ITINERARIO|idx|precoTotal|numPernas|pernas(carona:origem:destino:preco;...)
		fmt.Printf("[%s] preco total: R$%s | %s trecho(s)\n", it[1], it[2], it[3])
		for _, p := range proto.SepararPernas(it[4]) {
			sub := proto.SepararSub(p)
			fmt.Printf("      %s -> %s (carona %s, R$%s)\n", sub[1], sub[2], sub[0], sub[3])
		}
	}
	return linhas
}

func reservar(conn *clientio.Conexao, entrada *bufio.Reader, ultimaBusca [][]string) {
	if len(ultimaBusca) == 0 {
		fmt.Println("faca uma busca primeiro (opcao 1)")
		return
	}
	fmt.Print("indice do itinerario a reservar: ")
	idx := lerLinha(entrada)

	var escolhido []string
	for _, it := range ultimaBusca {
		if it[1] == idx {
			escolhido = it
			break
		}
	}
	if escolhido == nil {
		fmt.Println("indice invalido")
		return
	}

	// Reenvia ao servidor apenas carona:origem:destino de cada perna; o
	// preco e a disponibilidade sao SEMPRE revalidados no momento da
	// reserva (o preco visto na busca pode estar desatualizado se, por
	// exemplo, a carona foi cancelada nesse meio-tempo).
	var pernas []string
	for _, p := range proto.SepararPernas(escolhido[4]) {
		sub := proto.SepararSub(p)
		pernas = append(pernas, proto.JuntarPerna(sub[0], sub[1], sub[2]))
	}

	if err := conn.Enviar("RESERVAR", proto.JuntarPernas(pernas)); err != nil {
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
	fmt.Printf("reserva confirmada! id=%s preco total=R$%s\n", resp[2], resp[3])
}

func minhasReservas(conn *clientio.Conexao) {
	if err := conn.Enviar("MINHAS_RESERVAS"); err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		fmt.Println("erro de conexao:", err)
		return
	}
	if len(linhas) == 0 {
		fmt.Println("nenhuma reserva encontrada")
		return
	}
	for _, r := range linhas {
		// RESERVA|id|status|precoTotal|pernas
		fmt.Printf("- %s | %s | R$%s\n", r[1], r[2], r[3])
		for _, p := range proto.SepararPernas(r[4]) {
			sub := proto.SepararSub(p)
			fmt.Printf("      %s -> %s (carona %s, assento %s, R$%s)\n", sub[1], sub[2], sub[0], sub[3], sub[4])
		}
	}
}

func cancelarReserva(conn *clientio.Conexao, entrada *bufio.Reader) {
	fmt.Print("id da reserva a cancelar: ")
	id := lerLinha(entrada)
	if err := conn.Enviar("CANCELAR_RESERVA", id); err != nil {
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
	fmt.Println("reserva cancelada")
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
