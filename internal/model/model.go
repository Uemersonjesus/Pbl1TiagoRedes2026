// Package model define as estruturas de dados centrais do VAIJUNTO:
// caronas, trechos (segmentos entre duas cidades adjacentes da rota) e reservas.
package model

import "sync"

// Carona representa uma oferta de viagem publicada por um motorista.
// A disponibilidade de assentos é controlada por trecho, não pela carona
// como um todo: Ocupacao[i][a] guarda o login do passageiro (ou "" se livre)
// que ocupa o assento "a" no trecho "i" (entre Rota[i] e Rota[i+1]).
type Carona struct {
	ID            string
	MotoristaID   string
	Rota          []string
	Data          string
	Hora          string
	TotalAssentos int
	PrecoTrecho   float64
	Cancelada     bool

	mu       sync.Mutex
	Ocupacao [][]string
}

func NovaCarona(id, motoristaID string, rota []string, data, hora string, assentos int, preco float64) *Carona {
	nTrechos := len(rota) - 1
	ocupacao := make([][]string, nTrechos)
	for i := range ocupacao {
		ocupacao[i] = make([]string, assentos)
	}
	return &Carona{
		ID:            id,
		MotoristaID:   motoristaID,
		Rota:          rota,
		Data:          data,
		Hora:          hora,
		TotalAssentos: assentos,
		PrecoTrecho:   preco,
		Ocupacao:      ocupacao,
	}
}

// Lock/Unlock expõem o mutex da carona para que o Store possa adquirir
// travas de várias caronas em uma ordem global consistente (por ID),
// evitando deadlocks ao confirmar itinerários com trechos de motoristas
// diferentes (ver Store.Reservar).
func (c *Carona) Lock()   { c.mu.Lock() }
func (c *Carona) Unlock() { c.mu.Unlock() }

// IndiceCidade retorna a primeira posição de "cidade" na rota.
func (c *Carona) IndiceCidade(cidade string) (int, bool) {
	for i, cid := range c.Rota {
		if cid == cidade {
			return i, true
		}
	}
	return 0, false
}

// AssentosLivresTrecho conta quantos assentos estão livres no trecho idx.
// Deve ser chamado com a trava da carona já adquirida.
func (c *Carona) AssentosLivresTrecho(idx int) int {
	livres := 0
	for _, ocupante := range c.Ocupacao[idx] {
		if ocupante == "" {
			livres++
		}
	}
	return livres
}

// AssentosLivresIntervalo conta, para o intervalo [idxIni, idxFim), o maior
// número de assentos que permanecem simultaneamente livres em TODOS os
// trechos do intervalo (é o que importa para uma perna que atravessa mais
// de um trecho da mesma carona: precisa ser o MESMO assento do início ao fim).
func (c *Carona) AssentosLivresIntervalo(idxIni, idxFim int) int {
	livres := 0
	for a := 0; a < c.TotalAssentos; a++ {
		livre := true
		for i := idxIni; i < idxFim; i++ {
			if c.Ocupacao[i][a] != "" {
				livre = false
				break
			}
		}
		if livre {
			livres++
		}
	}
	return livres
}

// EncontrarAssentoLivre procura um número de assento livre em todos os
// trechos do intervalo [idxIni, idxFim). Deve ser chamado com a trava
// da carona já adquirida.
func (c *Carona) EncontrarAssentoLivre(idxIni, idxFim int) (int, bool) {
	for a := 0; a < c.TotalAssentos; a++ {
		livre := true
		for i := idxIni; i < idxFim; i++ {
			if c.Ocupacao[i][a] != "" {
				livre = false
				break
			}
		}
		if livre {
			return a, true
		}
	}
	return 0, false
}

// Reservar marca o assento "a" como ocupado por "passageiroID" em todos os
// trechos do intervalo [idxIni, idxFim). Deve ser chamado com a trava
// da carona já adquirida, após confirmar disponibilidade.
func (c *Carona) Reservar(idxIni, idxFim, a int, passageiroID string) {
	for i := idxIni; i < idxFim; i++ {
		c.Ocupacao[i][a] = passageiroID
	}
}

// Liberar desfaz uma reserva, devolvendo o assento ao estado livre.
func (c *Carona) Liberar(idxIni, idxFim, a int) {
	for i := idxIni; i < idxFim; i++ {
		c.Ocupacao[i][a] = ""
	}
}

// PernaReserva é um trecho contínuo, dentro de uma única carona, que compõe
// um itinerário reservado por um passageiro (uma reserva pode ter várias
// pernas, cada uma em uma carona/motorista diferente).
type PernaReserva struct {
	CaronaID string
	Origem   string
	Destino  string
	IdxIni   int
	IdxFim   int
	Assento  int
	Preco    float64
}

// Reserva agrupa uma ou mais pernas confirmadas atomicamente para um
// passageiro. Ou todas as pernas foram reservadas, ou a reserva não existe.
type Reserva struct {
	ID           string
	PassageiroID string
	Pernas       []PernaReserva
	Cancelada    bool
}

func (r *Reserva) PrecoTotal() float64 {
	total := 0.0
	for _, p := range r.Pernas {
		total += p.Preco
	}
	return total
}

// Perna é um segmento de itinerário retornado numa busca (ainda não
// reservado — a disponibilidade é revalidada no momento da reserva).
type Perna struct {
	CaronaID string
	Origem   string
	Destino  string
	Preco    float64
}

// Itinerario é uma combinação ordenada de pernas (possivelmente de
// motoristas diferentes) que leva da origem ao destino pedidos.
type Itinerario struct {
	Pernas     []Perna
	PrecoTotal float64
}
