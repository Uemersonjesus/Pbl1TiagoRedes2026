// Este arquivo fala o protocolo textual do VAIJUNTO (pacotes
// internal/clientio e internal/proto, os mesmos usados pelos clientes de
// terminal) para traduzir cada acao da interface web em uma requisicao
// TCP ao servidor central. Nenhuma logica de negocio mora aqui: apenas
// montagem/leitura de mensagens, igual aos clientes cmd/motorista e
// cmd/passageiro.
package main

import (
	"net/http"
	"strconv"
	"time"

	"vaijunto/internal/clientio"
	"vaijunto/internal/proto"
)

// prazoOperacao limita quanto tempo o gateway espera por uma resposta do
// servidor VAIJUNTO antes de desistir e responder ao navegador com erro,
// em vez de deixar a requisicao HTTP pendurada indefinidamente.
const prazoOperacao = 15 * time.Second

type apiErro struct {
	Status   int
	Mensagem string
}

func (e *apiErro) Error() string { return e.Mensagem }

func erroConexao(status int, err error) *apiErro {
	return &apiErro{Status: status, Mensagem: err.Error()}
}

// erroServidor converte uma linha ERRO|codigo|mensagem do protocolo
// VAIJUNTO num apiErro. Devolve nil quando a linha nao e um erro.
func erroServidor(campos []string) *apiErro {
	if len(campos) == 0 || campos[0] != "ERRO" {
		return nil
	}
	codigo, msg := "500", "erro desconhecido"
	if len(campos) > 1 {
		codigo = campos[1]
	}
	if len(campos) > 2 {
		msg = campos[2]
	}
	status, err := strconv.Atoi(codigo)
	if err != nil || !statusConhecido(status) {
		status = http.StatusInternalServerError
	}
	return &apiErro{Status: status, Mensagem: msg}
}

// statusConhecido restringe os codigos que o gateway repassa diretamente
// como status HTTP aos que o servidor VAIJUNTO realmente emite (ver
// README, secao 3.3) — qualquer outro valor cai em 500.
func statusConhecido(s int) bool {
	switch s {
	case 400, 401, 403, 404, 409, 500:
		return true
	}
	return false
}

// conectarELogar abre uma conexao TCP nova com o servidor VAIJUNTO e
// autentica com login/senha. O chamador e responsavel por chamar
// encerrar(conn) quando terminar.
func conectarELogar(addr, login, senha string) (*clientio.Conexao, *apiErro) {
	conn, err := clientio.Conectar(addr)
	if err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		conn.Fechar()
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.Enviar("LOGIN", login, senha); err != nil {
		conn.Fechar()
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	resp, err := conn.LerLinha()
	if err != nil {
		conn.Fechar()
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}
	if ae := erroServidor(resp); ae != nil {
		conn.Fechar()
		return nil, ae
	}
	return conn, nil
}

// encerrar despede-se do servidor educadamente (SAIR) e fecha o socket.
// Chamado sempre via defer logo apos conectarELogar ter sucesso.
func encerrar(conn *clientio.Conexao) {
	_ = conn.Enviar("SAIR")
	conn.Fechar()
}

type caronaResumo struct {
	ID            string   `json:"id"`
	Rota          []string `json:"rota"`
	Data          string   `json:"data"`
	Hora          string   `json:"hora"`
	AssentosTotal int      `json:"assentosTotal"`
	Preco         string   `json:"preco"`
	Cancelada     bool     `json:"cancelada"`
}

type trechoOcupacao struct {
	Idx     int    `json:"idx"`
	Origem  string `json:"origem"`
	Destino string `json:"destino"`
	Assento int    `json:"assento"`
	Status  string `json:"status"`
}

type pernaItinerario struct {
	CaronaID string `json:"caronaId"`
	Origem   string `json:"origem"`
	Destino  string `json:"destino"`
	Preco    string `json:"preco"`
}

type itinerario struct {
	Idx        int               `json:"idx"`
	PrecoTotal string            `json:"precoTotal"`
	Pernas     []pernaItinerario `json:"pernas"`
}

type pernaReserva struct {
	CaronaID string `json:"caronaId"`
	Origem   string `json:"origem"`
	Destino  string `json:"destino"`
	Assento  int    `json:"assento"`
	Preco    string `json:"preco"`
}

type reservaResumo struct {
	ID         string         `json:"id"`
	Status     string         `json:"status"`
	PrecoTotal string         `json:"precoTotal"`
	Pernas     []pernaReserva `json:"pernas"`
}

func backendLogin(addr, login, senha string) *apiErro {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return ae
	}
	encerrar(conn)
	return nil
}

func backendPublicar(addr, login, senha string, rota []string, data, hora string, assentos int, preco float64) (string, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return "", ae
	}
	defer encerrar(conn)

	precoTxt := strconv.FormatFloat(preco, 'f', 2, 64)
	if err := conn.Enviar("PUBLICAR", proto.JuntarLista(rota), data, hora, strconv.Itoa(assentos), precoTxt); err != nil {
		return "", erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return "", erroConexao(http.StatusBadGateway, err)
	}
	resp, err := conn.LerLinha()
	if err != nil {
		return "", erroConexao(http.StatusGatewayTimeout, err)
	}
	if ae := erroServidor(resp); ae != nil {
		return "", ae
	}
	if len(resp) < 3 {
		return "", &apiErro{Status: http.StatusBadGateway, Mensagem: "resposta inesperada do servidor"}
	}
	return resp[2], nil
}

func backendListarCaronas(addr, login, senha string) ([]caronaResumo, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return nil, ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("LISTAR_CARONAS"); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}

	caronas := make([]caronaResumo, 0, len(linhas))
	for _, c := range linhas {
		// CARONA|id|rota|data|hora|assentosTotal|preco|cancelada
		if len(c) < 8 {
			continue
		}
		assentos, _ := strconv.Atoi(c[5])
		caronas = append(caronas, caronaResumo{
			ID:            c[1],
			Rota:          proto.SepararLista(c[2]),
			Data:          c[3],
			Hora:          c[4],
			AssentosTotal: assentos,
			Preco:         c[6],
			Cancelada:     c[7] == "1",
		})
	}
	return caronas, nil
}

func backendDetalheCarona(addr, login, senha, caronaID string) ([]trechoOcupacao, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return nil, ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("DETALHE_CARONA", caronaID); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}
	if len(linhas) > 0 {
		if ae := erroServidor(linhas[0]); ae != nil {
			return nil, ae
		}
	}

	trechos := make([]trechoOcupacao, 0, len(linhas))
	for _, t := range linhas {
		// TRECHO|idx|origem|destino|assento|status
		if len(t) < 6 {
			continue
		}
		idx, _ := strconv.Atoi(t[1])
		assento, _ := strconv.Atoi(t[4])
		trechos = append(trechos, trechoOcupacao{Idx: idx, Origem: t[2], Destino: t[3], Assento: assento, Status: t[5]})
	}
	return trechos, nil
}

func backendCancelarCarona(addr, login, senha, caronaID string) *apiErro {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("CANCELAR_CARONA", caronaID); err != nil {
		return erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return erroConexao(http.StatusBadGateway, err)
	}
	resp, err := conn.LerLinha()
	if err != nil {
		return erroConexao(http.StatusGatewayTimeout, err)
	}
	return erroServidor(resp)
}

func backendBuscar(addr, login, senha, origem, destino, data string) ([]itinerario, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return nil, ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("BUSCAR", origem, destino, data); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}

	itinerarios := make([]itinerario, 0, len(linhas))
	for _, it := range linhas {
		// ITINERARIO|idx|precoTotal|numPernas|pernas(carona:origem:destino:preco;...)
		if len(it) < 5 {
			continue
		}
		idx, _ := strconv.Atoi(it[1])
		var pernas []pernaItinerario
		for _, p := range proto.SepararPernas(it[4]) {
			sub := proto.SepararSub(p)
			if len(sub) < 4 {
				continue
			}
			pernas = append(pernas, pernaItinerario{CaronaID: sub[0], Origem: sub[1], Destino: sub[2], Preco: sub[3]})
		}
		itinerarios = append(itinerarios, itinerario{Idx: idx, PrecoTotal: it[2], Pernas: pernas})
	}
	return itinerarios, nil
}

type pernaPedido struct {
	CaronaID string
	Origem   string
	Destino  string
}

func backendReservar(addr, login, senha string, pedido []pernaPedido) (*reservaResumo, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return nil, ae
	}
	defer encerrar(conn)

	pernasTxt := make([]string, len(pedido))
	for i, p := range pedido {
		pernasTxt[i] = proto.JuntarPerna(p.CaronaID, p.Origem, p.Destino)
	}
	if err := conn.Enviar("RESERVAR", proto.JuntarPernas(pernasTxt)); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	resp, err := conn.LerLinha()
	if err != nil {
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}
	if ae := erroServidor(resp); ae != nil {
		return nil, ae
	}
	// OK|RESERVA|id|precoTotal|pernas(carona:origem:destino:assento:preco;...)
	if len(resp) < 5 {
		return nil, &apiErro{Status: http.StatusBadGateway, Mensagem: "resposta inesperada do servidor"}
	}
	var pernas []pernaReserva
	for _, p := range proto.SepararPernas(resp[4]) {
		sub := proto.SepararSub(p)
		if len(sub) < 5 {
			continue
		}
		assento, _ := strconv.Atoi(sub[3])
		pernas = append(pernas, pernaReserva{CaronaID: sub[0], Origem: sub[1], Destino: sub[2], Assento: assento, Preco: sub[4]})
	}
	return &reservaResumo{ID: resp[2], Status: "ATIVA", PrecoTotal: resp[3], Pernas: pernas}, nil
}

func backendMinhasReservas(addr, login, senha string) ([]reservaResumo, *apiErro) {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return nil, ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("MINHAS_RESERVAS"); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return nil, erroConexao(http.StatusBadGateway, err)
	}
	linhas, err := conn.LerBloco()
	if err != nil {
		return nil, erroConexao(http.StatusGatewayTimeout, err)
	}

	reservas := make([]reservaResumo, 0, len(linhas))
	for _, r := range linhas {
		// RESERVA|id|status|precoTotal|pernas
		if len(r) < 5 {
			continue
		}
		var pernas []pernaReserva
		for _, p := range proto.SepararPernas(r[4]) {
			sub := proto.SepararSub(p)
			if len(sub) < 5 {
				continue
			}
			assento, _ := strconv.Atoi(sub[3])
			pernas = append(pernas, pernaReserva{CaronaID: sub[0], Origem: sub[1], Destino: sub[2], Assento: assento, Preco: sub[4]})
		}
		reservas = append(reservas, reservaResumo{ID: r[1], Status: r[2], PrecoTotal: r[3], Pernas: pernas})
	}
	return reservas, nil
}

func backendCancelarReserva(addr, login, senha, reservaID string) *apiErro {
	conn, ae := conectarELogar(addr, login, senha)
	if ae != nil {
		return ae
	}
	defer encerrar(conn)

	if err := conn.Enviar("CANCELAR_RESERVA", reservaID); err != nil {
		return erroConexao(http.StatusBadGateway, err)
	}
	if err := conn.DefinirPrazoLeitura(prazoOperacao); err != nil {
		return erroConexao(http.StatusBadGateway, err)
	}
	resp, err := conn.LerLinha()
	if err != nil {
		return erroConexao(http.StatusGatewayTimeout, err)
	}
	return erroServidor(resp)
}
