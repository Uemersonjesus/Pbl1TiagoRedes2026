// Package server implementa o servidor central do VAIJUNTO: aceita
// conexoes TCP, uma goroutine por cliente, e despacha cada linha recebida
// para o Store, que concentra toda a logica de concorrencia.
package server

import (
	"bufio"
	"errors"
	"log"
	"net"
	"strconv"
	"time"

	"vaijunto/internal/proto"
	"vaijunto/internal/store"
)

// idleTimeout fecha conexoes sem trafego ha muito tempo. Isso evita que um
// cliente encerrado abruptamente (sem fechar o socket de forma limpa, ex.:
// queda de rede) deixe a goroutine do servidor presa para sempre — e o
// motivo pelo qual a reserva e feita em UMA unica requisicao atomica
// (Store.Reservar): nao existe um "assento em espera" que dependa de uma
// segunda mensagem do cliente para ser liberado.
const idleTimeout = 10 * time.Minute

type sessao struct {
	conn        net.Conn
	writer      *bufio.Writer
	login       string
	autenticado bool
}

func (s *sessao) enviar(comando string, campos ...string) error {
	if _, err := s.writer.WriteString(proto.Codificar(comando, campos...) + "\n"); err != nil {
		return err
	}
	return s.writer.Flush()
}

func (s *sessao) erro(codigo, mensagem string) error {
	return s.enviar("ERRO", codigo, mensagem)
}

// Serve aceita conexoes no listener ate que ocorra um erro (ex.: listener
// fechado pelo chamador) e trata cada uma em sua propria goroutine.
func Serve(ln net.Listener, st *store.Store) error {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go handleConn(conn, st)
	}
}

func handleConn(conn net.Conn, st *store.Store) {
	defer conn.Close()
	reader := bufio.NewReaderSize(conn, 4096)
	sess := &sessao{conn: conn, writer: bufio.NewWriter(conn)}

	for {
		if err := conn.SetReadDeadline(time.Now().Add(idleTimeout)); err != nil {
			return
		}
		linha, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		campos := proto.DecodificarLinha(linha)
		if len(campos) == 0 {
			continue
		}
		if !processar(st, sess, campos) {
			return
		}
	}
}

// processar despacha uma linha ja decodificada. Retorna false quando a
// conexao deve ser encerrada (comando SAIR ou falha irrecuperavel de
// escrita).
func processar(st *store.Store, sess *sessao, campos []string) bool {
	var err error
	switch campos[0] {

	case "LOGIN":
		err = cmdLogin(st, sess, campos)

	case "PUBLICAR":
		err = exigirAuth(sess, func() error { return cmdPublicar(st, sess, campos) })

	case "LISTAR_CARONAS":
		err = exigirAuth(sess, func() error { return cmdListarCaronas(st, sess) })

	case "DETALHE_CARONA":
		err = exigirAuth(sess, func() error { return cmdDetalheCarona(st, sess, campos) })

	case "CANCELAR_CARONA":
		err = exigirAuth(sess, func() error { return cmdCancelarCarona(st, sess, campos) })

	case "BUSCAR":
		err = exigirAuth(sess, func() error { return cmdBuscar(st, sess, campos) })

	case "RESERVAR":
		err = exigirAuth(sess, func() error { return cmdReservar(st, sess, campos) })

	case "MINHAS_RESERVAS":
		err = exigirAuth(sess, func() error { return cmdMinhasReservas(st, sess) })

	case "CANCELAR_RESERVA":
		err = exigirAuth(sess, func() error { return cmdCancelarReserva(st, sess, campos) })

	case "SAIR":
		_ = sess.enviar("OK", "SAIR")
		return false

	default:
		err = sess.erro("400", "comando desconhecido: "+campos[0])
	}

	return err == nil
}

func exigirAuth(sess *sessao, fn func() error) error {
	if !sess.autenticado {
		return sess.erro("401", "nao autenticado, envie LOGIN primeiro")
	}
	return fn()
}

func cmdLogin(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 3 {
		return sess.erro("400", "uso: LOGIN|login|senha")
	}
	login, err := proto.SanitizarCampo(campos[1])
	if err != nil || login == "" {
		return sess.erro("400", "login invalido")
	}
	if err := st.Autenticar(login, campos[2]); err != nil {
		codigo, msg := traduzirErro(err)
		return sess.erro(codigo, msg)
	}
	sess.login = login
	sess.autenticado = true
	return sess.enviar("OK", "LOGIN", login)
}

func cmdPublicar(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 6 {
		return sess.erro("400", "uso: PUBLICAR|cidade1,cidade2,...|data|hora|assentos|preco")
	}
	rota := proto.SepararLista(campos[1])
	assentos, e1 := strconv.Atoi(campos[4])
	preco, e2 := strconv.ParseFloat(campos[5], 64)
	if e1 != nil || e2 != nil {
		return sess.erro("400", "assentos/preco invalidos")
	}
	carona, err := st.PublicarCarona(sess.login, rota, campos[2], campos[3], assentos, preco)
	if err != nil {
		codigo, msg := traduzirErro(err)
		return sess.erro(codigo, msg)
	}
	return sess.enviar("OK", "CARONA", carona.ID)
}

func cmdListarCaronas(st *store.Store, sess *sessao) error {
	for _, c := range st.ListarCaronasMotorista(sess.login) {
		cancelada := "0"
		if c.Cancelada {
			cancelada = "1"
		}
		if err := sess.enviar("CARONA", c.ID, proto.JuntarLista(c.Rota), c.Data, c.Hora,
			strconv.Itoa(c.TotalAssentos), formatarPreco(c.PrecoTrecho), cancelada); err != nil {
			return err
		}
	}
	return sess.enviar(proto.FimBloco)
}

func cmdDetalheCarona(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 2 {
		// DETALHE_CARONA e uma resposta em bloco (varias linhas TRECHO + FIM);
		// mesmo no caminho de erro o bloco precisa ser fechado com FIM, senao
		// o cliente (que usa LerBloco) fica esperando para sempre a proxima
		// linha, interpretando a resposta do comando seguinte como se fosse
		// parte deste bloco.
		if err := sess.erro("400", "uso: DETALHE_CARONA|caronaId"); err != nil {
			return err
		}
		return sess.enviar(proto.FimBloco)
	}
	carona, ocupacao, err := st.DetalheCarona(sess.login, campos[1])
	if err != nil {
		codigo, msg := traduzirErro(err)
		if err := sess.erro(codigo, msg); err != nil {
			return err
		}
		return sess.enviar(proto.FimBloco)
	}
	for i := 0; i < len(carona.Rota)-1; i++ {
		for a := 0; a < carona.TotalAssentos; a++ {
			status := ocupacao[i][a]
			if status == "" {
				status = "LIVRE"
			}
			if err := sess.enviar("TRECHO", strconv.Itoa(i), carona.Rota[i], carona.Rota[i+1],
				strconv.Itoa(a+1), status); err != nil {
				return err
			}
		}
	}
	return sess.enviar(proto.FimBloco)
}

func cmdCancelarCarona(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 2 {
		return sess.erro("400", "uso: CANCELAR_CARONA|caronaId")
	}
	if err := st.CancelarCarona(sess.login, campos[1]); err != nil {
		codigo, msg := traduzirErro(err)
		return sess.erro(codigo, msg)
	}
	return sess.enviar("OK", "CANCELAR_CARONA")
}

func cmdBuscar(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 4 {
		return sess.erro("400", "uso: BUSCAR|origem|destino|data")
	}
	itinerarios := st.Buscar(campos[1], campos[2], campos[3])
	for idx, it := range itinerarios {
		pernasTxt := make([]string, len(it.Pernas))
		for i, p := range it.Pernas {
			pernasTxt[i] = proto.JuntarPerna(p.CaronaID, p.Origem, p.Destino, formatarPreco(p.Preco))
		}
		if err := sess.enviar("ITINERARIO", strconv.Itoa(idx), formatarPreco(it.PrecoTotal),
			strconv.Itoa(len(it.Pernas)), proto.JuntarPernas(pernasTxt)); err != nil {
			return err
		}
	}
	return sess.enviar(proto.FimBloco)
}

func cmdReservar(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 2 {
		return sess.erro("400", "uso: RESERVAR|carona:origem:destino;carona2:origem2:destino2;...")
	}
	brutas := proto.SepararPernas(campos[1])
	if len(brutas) == 0 {
		return sess.erro("400", "itinerario vazio")
	}
	pedido := make([]store.PernaPedido, 0, len(brutas))
	for _, b := range brutas {
		sub := proto.SepararSub(b)
		if len(sub) < 3 {
			return sess.erro("400", "perna malformada: "+b)
		}
		pedido = append(pedido, store.PernaPedido{CaronaID: sub[0], Origem: sub[1], Destino: sub[2]})
	}
	reserva, err := st.Reservar(sess.login, pedido)
	if err != nil {
		codigo, msg := traduzirErro(err)
		return sess.erro(codigo, msg)
	}
	pernasTxt := make([]string, len(reserva.Pernas))
	for i, p := range reserva.Pernas {
		pernasTxt[i] = proto.JuntarPerna(p.CaronaID, p.Origem, p.Destino, strconv.Itoa(p.Assento+1), formatarPreco(p.Preco))
	}
	return sess.enviar("OK", "RESERVA", reserva.ID, formatarPreco(reserva.PrecoTotal()), proto.JuntarPernas(pernasTxt))
}

func cmdMinhasReservas(st *store.Store, sess *sessao) error {
	for _, r := range st.MinhasReservas(sess.login) {
		status := "ATIVA"
		if r.Cancelada {
			status = "CANCELADA"
		}
		pernasTxt := make([]string, len(r.Pernas))
		for i, p := range r.Pernas {
			pernasTxt[i] = proto.JuntarPerna(p.CaronaID, p.Origem, p.Destino, strconv.Itoa(p.Assento+1), formatarPreco(p.Preco))
		}
		if err := sess.enviar("RESERVA", r.ID, status, formatarPreco(r.PrecoTotal()), proto.JuntarPernas(pernasTxt)); err != nil {
			return err
		}
	}
	return sess.enviar(proto.FimBloco)
}

func cmdCancelarReserva(st *store.Store, sess *sessao, campos []string) error {
	if len(campos) != 2 {
		return sess.erro("400", "uso: CANCELAR_RESERVA|reservaId")
	}
	if err := st.CancelarReserva(sess.login, campos[1]); err != nil {
		codigo, msg := traduzirErro(err)
		return sess.erro(codigo, msg)
	}
	return sess.enviar("OK", "CANCELAR_RESERVA")
}

func traduzirErro(err error) (codigo, mensagem string) {
	switch {
	case errors.Is(err, store.ErrNaoAutorizado):
		return "403", "nao autorizado"
	case errors.Is(err, store.ErrNaoEncontrado):
		return "404", "nao encontrado"
	case errors.Is(err, store.ErrDadosInvalidos):
		return "400", "dados invalidos"
	case errors.Is(err, store.ErrTrechoIndisp):
		return "409", err.Error()
	case errors.Is(err, store.ErrCredenciais):
		return "401", "credenciais invalidas"
	case errors.Is(err, store.ErrReservaCancelada):
		return "409", "reserva ja cancelada"
	default:
		log.Printf("erro interno: %v", err)
		return "500", "erro interno"
	}
}

func formatarPreco(v float64) string {
	return strconv.FormatFloat(v, 'f', 2, 64)
}
