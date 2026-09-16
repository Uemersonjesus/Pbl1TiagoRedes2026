// Package store implementa o estado central do servidor VAIJUNTO: usuários,
// caronas e reservas, com todo o controle de concorrência necessário para
// que múltiplos motoristas e passageiros operem ao mesmo tempo sem
// vender o mesmo assento duas vezes e sem confirmar itinerários pela metade.
package store

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"vaijunto/internal/model"
)

var (
	ErrNaoAutorizado    = errors.New("nao autorizado")
	ErrNaoEncontrado    = errors.New("nao encontrado")
	ErrDadosInvalidos   = errors.New("dados invalidos")
	ErrTrechoIndisp     = errors.New("trecho indisponivel")
	ErrCredenciais      = errors.New("credenciais invalidas")
	ErrReservaCancelada = errors.New("reserva ja cancelada")
)

type Store struct {
	// mu protege a existencia/estrutura dos mapas (inserir/remover
	// usuarios, caronas e reservas), NAO a ocupacao de assentos de uma
	// carona especifica — essa e protegida pelo mutex individual de
	// cada *model.Carona, o que permite que buscas e reservas em
	// caronas diferentes prossigam em paralelo.
	mu       sync.RWMutex
	usuarios map[string]string // login -> senha
	caronas  map[string]*model.Carona
	reservas map[string]*model.Reserva

	seqCarona  int64
	seqReserva int64
}

func New() *Store {
	return &Store{
		usuarios: make(map[string]string),
		caronas:  make(map[string]*model.Carona),
		reservas: make(map[string]*model.Reserva),
	}
}

// Autenticar faz um cadastro automático no primeiro login (simplificação
// deliberada: o foco do trabalho é o protocolo de caronas/reservas, não um
// fluxo de cadastro). Em usos seguintes, a senha deve bater com a
// cadastrada.
func (s *Store) Autenticar(login, senha string) error {
	if login == "" || senha == "" {
		return ErrDadosInvalidos
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	senhaAtual, existe := s.usuarios[login]
	if !existe {
		s.usuarios[login] = senha
		return nil
	}
	if senhaAtual != senha {
		return ErrCredenciais
	}
	return nil
}

func (s *Store) novoCaronaID() string {
	n := atomic.AddInt64(&s.seqCarona, 1)
	return fmt.Sprintf("C%d", n)
}

func (s *Store) novaReservaID() string {
	n := atomic.AddInt64(&s.seqReserva, 1)
	return fmt.Sprintf("R%d", n)
}

func (s *Store) PublicarCarona(motoristaID string, rota []string, data, hora string, assentos int, preco float64) (*model.Carona, error) {
	if len(rota) < 2 || assentos <= 0 || preco < 0 || data == "" || hora == "" {
		return nil, ErrDadosInvalidos
	}
	c := model.NovaCarona(s.novoCaronaID(), motoristaID, rota, data, hora, assentos, preco)
	s.mu.Lock()
	s.caronas[c.ID] = c
	s.mu.Unlock()
	return c, nil
}

func (s *Store) ListarCaronasMotorista(motoristaID string) []*model.Carona {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Carona
	for _, c := range s.caronas {
		if c.MotoristaID == motoristaID {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) BuscarCarona(id string) (*model.Carona, error) {
	s.mu.RLock()
	c, ok := s.caronas[id]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNaoEncontrado
	}
	return c, nil
}

func (s *Store) CancelarCarona(motoristaID, caronaID string) error {
	c, err := s.BuscarCarona(caronaID)
	if err != nil {
		return err
	}
	if c.MotoristaID != motoristaID {
		return ErrNaoAutorizado
	}
	c.Lock()
	c.Cancelada = true
	c.Unlock()

	// Cancela em cascata as reservas dependentes: como o servidor nao
	// envia notificacoes assincronas, o passageiro descobre o
	// cancelamento na proxima consulta (MINHAS_RESERVAS).
	s.mu.RLock()
	afetadas := []*model.Reserva{}
	for _, r := range s.reservas {
		if r.Cancelada {
			continue
		}
		for _, p := range r.Pernas {
			if p.CaronaID == caronaID {
				afetadas = append(afetadas, r)
				break
			}
		}
	}
	s.mu.RUnlock()
	for _, r := range afetadas {
		s.CancelarReserva(r.PassageiroID, r.ID)
	}
	return nil
}

// PernaPedido identifica uma perna de itinerario pelo par origem/destino
// dentro de uma carona especifica, tal como devolvido por Buscar.
type PernaPedido struct {
	CaronaID string
	Origem   string
	Destino  string
}

// Buscar monta os itinerarios possiveis entre origem e destino numa data.
// Constroi um grafo cujas arestas sao trechos (continuos, dentro de uma
// mesma carona) com pelo menos um assento livre, e faz uma busca em
// profundidade limitada (no maximo 3 pernas) por caminhos simples,
// permitindo combinar pernas de motoristas diferentes. As opcoes sao
// ordenadas por preco total e, em caso de empate, pelo numero de pernas
// (itinerarios diretos sao preferidos a baldeacoes).
func (s *Store) Buscar(origem, destino, data string) []model.Itinerario {
	type aresta struct {
		caronaID        string
		origem, destino string
		preco           float64
	}
	s.mu.RLock()
	todas := make([]*model.Carona, 0, len(s.caronas))
	for _, c := range s.caronas {
		todas = append(todas, c)
	}
	s.mu.RUnlock()

	porOrigem := map[string][]aresta{}
	for _, c := range todas {
		if c.Data != data {
			continue
		}
		c.Lock()
		if c.Cancelada {
			c.Unlock()
			continue
		}
		for i := 0; i < len(c.Rota); i++ {
			for j := i + 1; j < len(c.Rota); j++ {
				if c.AssentosLivresIntervalo(i, j) > 0 {
					a := aresta{
						caronaID: c.ID,
						origem:   c.Rota[i],
						destino:  c.Rota[j],
						preco:    c.PrecoTrecho * float64(j-i),
					}
					porOrigem[a.origem] = append(porOrigem[a.origem], a)
				}
			}
		}
		c.Unlock()
	}

	const maxPernas = 3
	var resultados []model.Itinerario
	var caminho []model.Perna
	visitados := map[string]bool{origem: true}

	var dfs func(atual string, precoAcum float64)
	dfs = func(atual string, precoAcum float64) {
		if atual == destino && len(caminho) > 0 {
			cp := make([]model.Perna, len(caminho))
			copy(cp, caminho)
			resultados = append(resultados, model.Itinerario{Pernas: cp, PrecoTotal: precoAcum})
		}
		if len(caminho) >= maxPernas {
			return
		}
		for _, a := range porOrigem[atual] {
			if visitados[a.destino] {
				continue
			}
			visitados[a.destino] = true
			caminho = append(caminho, model.Perna{CaronaID: a.caronaID, Origem: a.origem, Destino: a.destino, Preco: a.preco})
			dfs(a.destino, precoAcum+a.preco)
			caminho = caminho[:len(caminho)-1]
			visitados[a.destino] = false
		}
	}
	dfs(origem, 0)

	sort.Slice(resultados, func(i, j int) bool {
		if resultados[i].PrecoTotal != resultados[j].PrecoTotal {
			return resultados[i].PrecoTotal < resultados[j].PrecoTotal
		}
		return len(resultados[i].Pernas) < len(resultados[j].Pernas)
	})
	const maxResultados = 10
	if len(resultados) > maxResultados {
		resultados = resultados[:maxResultados]
	}
	return resultados
}

// Reservar confirma atomicamente um itinerario composto por uma ou mais
// pernas: ou todas as pernas sao reservadas, ou nenhuma e. As caronas
// envolvidas sao travadas em ordem crescente de ID (nunca na ordem em que
// aparecem no pedido do cliente), garantindo que dois passageiros
// disputando os mesmos trechos em ordens diferentes nunca se bloqueiem
// mutuamente (deadlock).
func (s *Store) Reservar(passageiroID string, pedido []PernaPedido) (*model.Reserva, error) {
	if len(pedido) == 0 {
		return nil, ErrDadosInvalidos
	}

	idsUnicos := map[string]bool{}
	for _, p := range pedido {
		idsUnicos[p.CaronaID] = true
	}
	ids := make([]string, 0, len(idsUnicos))
	for id := range idsUnicos {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	caronas := make(map[string]*model.Carona, len(ids))
	for _, id := range ids {
		c, err := s.BuscarCarona(id)
		if err != nil {
			return nil, fmt.Errorf("carona %s: %w", id, ErrNaoEncontrado)
		}
		caronas[id] = c
	}

	for _, id := range ids {
		caronas[id].Lock()
	}
	defer func() {
		for i := len(ids) - 1; i >= 0; i-- {
			caronas[ids[i]].Unlock()
		}
	}()

	// Fase de validacao: verifica TODAS as pernas antes de gravar
	// qualquer ocupacao real. Um overlay local (por carona) evita que
	// duas pernas do MESMO pedido, na mesma carona, colidam no mesmo
	// assento sem que isso ainda esteja refletido em c.Ocupacao. Se
	// qualquer perna falhar, retornamos sem ter tocado o estado real —
	// nao ha necessidade de desfazer nada, o que elimina o risco de
	// assento ficar preso por uma reserva parcial.
	overlay := make(map[string][][]bool, len(ids))
	for _, id := range ids {
		c := caronas[id]
		rows := make([][]bool, len(c.Ocupacao))
		for i := range rows {
			rows[i] = make([]bool, c.TotalAssentos)
		}
		overlay[id] = rows
	}

	pernas := make([]model.PernaReserva, 0, len(pedido))
	for _, p := range pedido {
		c := caronas[p.CaronaID]
		if c.Cancelada {
			return nil, fmt.Errorf("carona %s cancelada: %w", p.CaronaID, ErrTrechoIndisp)
		}
		iOrigem, ok1 := c.IndiceCidade(p.Origem)
		iDestino, ok2 := c.IndiceCidade(p.Destino)
		if !ok1 || !ok2 || iOrigem >= iDestino {
			return nil, fmt.Errorf("trecho %s->%s invalido na carona %s: %w", p.Origem, p.Destino, p.CaronaID, ErrDadosInvalidos)
		}
		ov := overlay[p.CaronaID]
		assento := -1
		for a := 0; a < c.TotalAssentos; a++ {
			livre := true
			for i := iOrigem; i < iDestino; i++ {
				if c.Ocupacao[i][a] != "" || ov[i][a] {
					livre = false
					break
				}
			}
			if livre {
				assento = a
				break
			}
		}
		if assento == -1 {
			return nil, fmt.Errorf("sem assento livre em %s (%s->%s): %w", p.CaronaID, p.Origem, p.Destino, ErrTrechoIndisp)
		}
		for i := iOrigem; i < iDestino; i++ {
			ov[i][assento] = true
		}
		pernas = append(pernas, model.PernaReserva{
			CaronaID: p.CaronaID,
			Origem:   p.Origem,
			Destino:  p.Destino,
			IdxIni:   iOrigem,
			IdxFim:   iDestino,
			Assento:  assento,
			Preco:    c.PrecoTrecho * float64(iDestino-iOrigem),
		})
	}

	// Fase de efetivacao: todas as pernas foram validadas, agora sim
	// gravamos a ocupacao real. As travas das caronas seguem seguradas
	// desde o inicio da funcao, entao nenhum outro goroutine pode ter
	// alterado o estado entre a validacao e a efetivacao.
	for _, p := range pernas {
		caronas[p.CaronaID].Reservar(p.IdxIni, p.IdxFim, p.Assento, passageiroID)
	}

	reserva := &model.Reserva{ID: s.novaReservaID(), PassageiroID: passageiroID, Pernas: pernas}
	s.mu.Lock()
	s.reservas[reserva.ID] = reserva
	s.mu.Unlock()
	return reserva, nil
}

func (s *Store) MinhasReservas(passageiroID string) []*model.Reserva {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Reserva
	for _, r := range s.reservas {
		if r.PassageiroID == passageiroID {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (s *Store) CancelarReserva(passageiroID, reservaID string) error {
	s.mu.RLock()
	r, ok := s.reservas[reservaID]
	s.mu.RUnlock()
	if !ok {
		return ErrNaoEncontrado
	}
	if r.PassageiroID != passageiroID {
		return ErrNaoAutorizado
	}

	ids := make([]string, 0, len(r.Pernas))
	seen := map[string]bool{}
	for _, p := range r.Pernas {
		if !seen[p.CaronaID] {
			seen[p.CaronaID] = true
			ids = append(ids, p.CaronaID)
		}
	}
	sort.Strings(ids)

	caronas := make(map[string]*model.Carona, len(ids))
	for _, id := range ids {
		c, err := s.BuscarCarona(id)
		if err == nil {
			caronas[id] = c
		}
	}
	for _, id := range ids {
		caronas[id].Lock()
	}
	defer func() {
		for i := len(ids) - 1; i >= 0; i-- {
			caronas[ids[i]].Unlock()
		}
	}()

	s.mu.Lock()
	if r.Cancelada {
		s.mu.Unlock()
		return ErrReservaCancelada
	}
	r.Cancelada = true
	s.mu.Unlock()

	for _, p := range r.Pernas {
		if c, ok := caronas[p.CaronaID]; ok {
			c.Liberar(p.IdxIni, p.IdxFim, p.Assento)
		}
	}
	return nil
}

// DetalheCarona devolve, para cada trecho da rota, o login do passageiro
// que ocupa cada assento (ou "" se livre). Usado pelo cliente motorista
// para acompanhar os passageiros confirmados por trecho.
func (s *Store) DetalheCarona(motoristaID, caronaID string) (*model.Carona, [][]string, error) {
	c, err := s.BuscarCarona(caronaID)
	if err != nil {
		return nil, nil, err
	}
	if c.MotoristaID != motoristaID {
		return nil, nil, ErrNaoAutorizado
	}
	c.Lock()
	defer c.Unlock()
	snapshot := make([][]string, len(c.Ocupacao))
	for i, linha := range c.Ocupacao {
		snapshot[i] = append([]string(nil), linha...)
	}
	return c, snapshot, nil
}
