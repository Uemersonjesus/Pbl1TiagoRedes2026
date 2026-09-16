package store

import (
	"strconv"
	"sync"
	"testing"
)

// TestReservaConcorrenteMesmoTrecho sobe N passageiros disputando, ao
// mesmo tempo, os mesmos "assentosTotal" assentos de um unico trecho.
// A regra de negocio exige que EXATAMENTE "assentosTotal" reservas
// tenham sucesso e nenhum assento seja concedido duas vezes.
func TestReservaConcorrenteMesmoTrecho(t *testing.T) {
	s := New()
	carona, err := s.PublicarCarona("motorista1", []string{"Salvador", "Feira de Santana"}, "2026-10-01", "08:00", 5, 50)
	if err != nil {
		t.Fatalf("publicar carona: %v", err)
	}

	const nPassageiros = 50
	var wg sync.WaitGroup
	sucesso := make([]bool, nPassageiros)
	assentoDe := make([]int, nPassageiros)

	for i := 0; i < nPassageiros; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			passageiro := "p" + strconv.Itoa(i)
			r, err := s.Reservar(passageiro, []PernaPedido{{CaronaID: carona.ID, Origem: "Salvador", Destino: "Feira de Santana"}})
			if err == nil {
				sucesso[i] = true
				assentoDe[i] = r.Pernas[0].Assento
			}
		}(i)
	}
	wg.Wait()

	totalSucesso := 0
	vistos := map[int]bool{}
	for i := 0; i < nPassageiros; i++ {
		if sucesso[i] {
			totalSucesso++
			if vistos[assentoDe[i]] {
				t.Fatalf("assento %d foi concedido a mais de um passageiro", assentoDe[i])
			}
			vistos[assentoDe[i]] = true
		}
	}
	if totalSucesso != 5 {
		t.Fatalf("esperava exatamente 5 reservas confirmadas, obteve %d", totalSucesso)
	}
	if carona.AssentosLivresTrecho(0) != 0 {
		t.Fatalf("esperava 0 assentos livres apos vender todos, obteve %d", carona.AssentosLivresTrecho(0))
	}
}

// TestReservaAtomicaItinerarioComTransbordo garante que, quando um
// itinerario tem duas pernas (motoristas diferentes) e a segunda perna
// fica indisponivel, NENHUMA das duas pernas e reservada (atomicidade).
func TestReservaAtomicaItinerarioComTransbordo(t *testing.T) {
	s := New()
	c1, _ := s.PublicarCarona("motoristaA", []string{"Salvador", "Feira de Santana"}, "2026-10-01", "08:00", 1, 40)
	c2, _ := s.PublicarCarona("motoristaB", []string{"Feira de Santana", "Vitoria da Conquista"}, "2026-10-01", "10:00", 1, 60)

	// Esgota o segundo trecho antes da tentativa de itinerario completo.
	if _, err := s.Reservar("outroPassageiro", []PernaPedido{{CaronaID: c2.ID, Origem: "Feira de Santana", Destino: "Vitoria da Conquista"}}); err != nil {
		t.Fatalf("reserva de preenchimento falhou: %v", err)
	}

	_, err := s.Reservar("joao", []PernaPedido{
		{CaronaID: c1.ID, Origem: "Salvador", Destino: "Feira de Santana"},
		{CaronaID: c2.ID, Origem: "Feira de Santana", Destino: "Vitoria da Conquista"},
	})
	if err == nil {
		t.Fatalf("esperava falha por trecho indisponivel, mas a reserva foi aceita")
	}

	if c1.AssentosLivresTrecho(0) != 1 {
		t.Fatalf("primeira perna nao deveria ter sido reservada (reserva deve ser atomica), assentos livres=%d", c1.AssentosLivresTrecho(0))
	}
}

// TestBuscaCombinaCaronasDiferentes reproduz o exemplo do enunciado:
// Salvador -> Feira de Santana com o motorista A, e Feira de Santana ->
// Vitoria da Conquista com o motorista B, formando um itinerario com
// baldeacao mesmo sem nenhuma carona direta Salvador -> Vitoria da
// Conquista.
func TestBuscaCombinaCaronasDiferentes(t *testing.T) {
	s := New()
	s.PublicarCarona("motoristaA", []string{"Salvador", "Feira de Santana"}, "2026-10-01", "08:00", 3, 40)
	s.PublicarCarona("motoristaB", []string{"Feira de Santana", "Vitoria da Conquista"}, "2026-10-01", "10:00", 3, 60)

	itinerarios := s.Buscar("Salvador", "Vitoria da Conquista", "2026-10-01")
	if len(itinerarios) == 0 {
		t.Fatalf("esperava encontrar ao menos um itinerario combinando as duas caronas")
	}
	if len(itinerarios[0].Pernas) != 2 {
		t.Fatalf("esperava itinerario com 2 pernas, obteve %d", len(itinerarios[0].Pernas))
	}
	if itinerarios[0].PrecoTotal != 100 {
		t.Fatalf("esperava preco total 100, obteve %.2f", itinerarios[0].PrecoTotal)
	}
}

// TestCancelarReservaLiberaAssento garante que cancelar uma reserva
// devolve o assento para uso, evitando bloqueio permanente.
func TestCancelarReservaLiberaAssento(t *testing.T) {
	s := New()
	c, _ := s.PublicarCarona("motorista1", []string{"A", "B"}, "2026-10-01", "08:00", 1, 10)

	r, err := s.Reservar("joao", []PernaPedido{{CaronaID: c.ID, Origem: "A", Destino: "B"}})
	if err != nil {
		t.Fatalf("reserva inicial falhou: %v", err)
	}
	if c.AssentosLivresTrecho(0) != 0 {
		t.Fatalf("assento deveria estar ocupado")
	}
	if err := s.CancelarReserva("joao", r.ID); err != nil {
		t.Fatalf("cancelar reserva: %v", err)
	}
	if c.AssentosLivresTrecho(0) != 1 {
		t.Fatalf("assento deveria ter sido liberado apos cancelamento")
	}

	if _, err := s.Reservar("maria", []PernaPedido{{CaronaID: c.ID, Origem: "A", Destino: "B"}}); err != nil {
		t.Fatalf("nova reserva apos liberacao deveria ter sucesso: %v", err)
	}
}
