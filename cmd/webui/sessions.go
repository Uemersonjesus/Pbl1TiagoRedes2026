package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// O protocolo VAIJUNTO nao usa tokens: a autenticacao fica amarrada a UMA
// conexao TCP (ver README, secao 2). Como o gateway HTTP fala com o
// servidor por conexoes curtas (uma por requisicao, ver backend.go), ele
// guarda aqui, em memoria, a credencial associada ao cookie de sessao do
// navegador, para nao precisar pedir login/senha a cada clique na
// interface. Cada requisicao HTTP autenticada reabre uma conexao TCP
// nova e reenvia LOGIN nela — isso elimina qualquer necessidade de
// serializar acesso concorrente a um socket compartilhado.
const (
	cookieNome     = "vaijunto_sid"
	sessaoValidade = 12 * time.Hour
)

type sessaoWeb struct {
	login  string
	senha  string
	expira time.Time
}

type sessionStore struct {
	mu   sync.RWMutex
	dado map[string]sessaoWeb
}

func novoSessionStore() *sessionStore {
	return &sessionStore{dado: make(map[string]sessaoWeb)}
}

func novoID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// criar registra uma nova sessao e devolve o id a ser guardado no cookie.
// Aproveita a chamada para descartar sessoes expiradas (evita crescimento
// ilimitado do mapa sem precisar de uma goroutine de limpeza separada).
func (s *sessionStore) criar(login, senha string) (string, error) {
	id, err := novoID()
	if err != nil {
		return "", err
	}
	agora := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.dado {
		if agora.After(v.expira) {
			delete(s.dado, k)
		}
	}
	s.dado[id] = sessaoWeb{login: login, senha: senha, expira: agora.Add(sessaoValidade)}
	return id, nil
}

func (s *sessionStore) obter(id string) (sessaoWeb, bool) {
	if id == "" {
		return sessaoWeb{}, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.dado[id]
	if !ok || time.Now().After(v.expira) {
		return sessaoWeb{}, false
	}
	return v, true
}

func (s *sessionStore) remover(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.dado, id)
}

func lerCookieSessao(r *http.Request) string {
	c, err := r.Cookie(cookieNome)
	if err != nil {
		return ""
	}
	return c.Value
}

func definirCookieSessao(w http.ResponseWriter, id string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieNome,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(sessaoValidade.Seconds()),
	})
}

func limparCookieSessao(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieNome,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}
