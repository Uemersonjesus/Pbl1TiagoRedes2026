package server

import (
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"vaijunto/internal/clientio"
	"vaijunto/internal/proto"
	"vaijunto/internal/store"
)

// subirServidorTeste sobe um servidor VAIJUNTO real em uma porta efemera
// da maquina local e devolve o endereco para os clientes de teste se
// conectarem via socket TCP de verdade (nao chama funcoes do Store
// diretamente).
func subirServidorTeste(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	st := store.New()
	go Serve(ln, st)
	t.Cleanup(func() { ln.Close() })
	return ln.Addr().String()
}

func publicarCaronaTeste(t *testing.T, addr string, assentos int) string {
	t.Helper()
	conn, err := clientio.Conectar(addr)
	if err != nil {
		t.Fatalf("conectar: %v", err)
	}
	defer conn.Fechar()

	conn.Enviar("LOGIN", "motorista1", "senha")
	if _, err := conn.LerLinha(); err != nil {
		t.Fatalf("login: %v", err)
	}
	conn.Enviar("PUBLICAR", "Salvador,Feira de Santana", "2026-10-01", "08:00", strconv.Itoa(assentos), "50.00")
	resp, err := conn.LerLinha()
	if err != nil {
		t.Fatalf("publicar: %v", err)
	}
	if ehErro, e := clientio.EhErro(resp); ehErro {
		t.Fatalf("publicar recusado: %v", e)
	}
	return resp[2] // id da carona
}

// TestConcorrenciaMultiplosClientesSockets sobe o servidor real, publica
// uma carona com N assentos e dispara, via sockets TCP independentes
// (um por "cliente"), muito mais de N passageiros tentando reservar o
// mesmo trecho ao mesmo tempo. Verifica que exatamente N reservas sao
// aceitas e mede o tempo total sob carga.
func TestConcorrenciaMultiplosClientesSockets(t *testing.T) {
	addr := subirServidorTeste(t)
	const assentos = 10
	caronaID := publicarCaronaTeste(t, addr, assentos)

	const nClientes = 100
	var wg sync.WaitGroup
	resultados := make([]bool, nClientes)

	inicio := time.Now()
	for i := 0; i < nClientes; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn, err := clientio.Conectar(addr)
			if err != nil {
				t.Errorf("cliente %d: conectar: %v", i, err)
				return
			}
			defer conn.Fechar()

			login := "passageiro" + strconv.Itoa(i)
			conn.Enviar("LOGIN", login, "senha")
			if _, err := conn.LerLinha(); err != nil {
				t.Errorf("cliente %d: login: %v", i, err)
				return
			}

			perna := proto.JuntarPerna(caronaID, "Salvador", "Feira de Santana")
			conn.Enviar("RESERVAR", perna)
			resp, err := conn.LerLinha()
			if err != nil {
				t.Errorf("cliente %d: reservar: %v", i, err)
				return
			}
			resultados[i] = resp[0] == "OK"
		}(i)
	}
	wg.Wait()
	duracao := time.Since(inicio)

	sucesso := 0
	for _, ok := range resultados {
		if ok {
			sucesso++
		}
	}
	if sucesso != assentos {
		t.Fatalf("esperava exatamente %d reservas confirmadas de %d clientes, obteve %d", assentos, nClientes, sucesso)
	}
	t.Logf("sob %d clientes simultaneos, %d reservas confirmadas em %s (media %s/cliente)",
		nClientes, sucesso, duracao, duracao/nClientes)
}
