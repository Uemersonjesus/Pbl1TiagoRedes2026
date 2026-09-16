package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

type servidor struct {
	backendAddr string
	sessoes     *sessionStore
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func respondErro(w http.ResponseWriter, status int, mensagem string) {
	respondJSON(w, status, map[string]string{"erro": mensagem})
}

func respondAPIErro(w http.ResponseWriter, ae *apiErro) {
	respondErro(w, ae.Status, ae.Mensagem)
}

// decodeJSON le e decodifica o corpo da requisicao; escreve a resposta de
// erro (400) e devolve false quando o corpo esta ausente ou malformado.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	if r.Body == nil {
		respondErro(w, http.StatusBadRequest, "corpo da requisicao ausente")
		return false
	}
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		respondErro(w, http.StatusBadRequest, "JSON invalido: "+err.Error())
		return false
	}
	return true
}

// Os caracteres abaixo tem significado especial no protocolo VAIJUNTO
// (ver internal/proto): "|" separa campos de uma mensagem (em qualquer
// campo), "," separa itens de uma lista (ex.: cidades da rota), e ":"/";"
// separam as subpartes e os itens de uma "perna" de itinerario. O
// servidor so valida isso para o campo "login" (ver server.go,
// cmdLogin) — os demais campos livres (nomes de cidade, ids, horarios)
// nao sao validados pelo protocolo em si, entao o gateway valida aqui,
// na borda HTTP, antes de montar a linha de protocolo, usando a regra
// certa para cada nivel de composicao (ex.: "hora" precisa poder conter
// ":", entao so pode ser validada como campo simples).
const separadoresCampo = "|\n\r"

// campoValido cobre qualquer campo simples de nivel superior (data, hora,
// ids, nomes de cidade usados sozinhos): so o separador de campo pode
// corrompe-lo.
func campoValido(v string) bool {
	return v != "" && !strings.ContainsAny(v, separadoresCampo)
}

func camposValidos(vs ...string) bool {
	for _, v := range vs {
		if !campoValido(v) {
			return false
		}
	}
	return true
}

// campoDeLista valida um item que sera juntado com "," (ex.: uma cidade
// dentro da rota de PUBLICAR): alem do separador de campo, uma virgula
// quebraria a lista em itens a mais.
func campoDeLista(v string) bool {
	return campoValido(v) && !strings.Contains(v, ",")
}

// campoDePerna valida um valor que sera montado numa "perna" de
// itinerario (caronaId:origem:destino, varias pernas separadas por ";",
// usado em RESERVAR): alem do separador de campo, ":" e ";" quebrariam a
// subdivisao da perna.
func campoDePerna(v string) bool {
	return campoValido(v) && !strings.ContainsAny(v, ":;")
}

// exigirSessao busca a sessao web associada ao cookie da requisicao. Se
// nao houver sessao valida, responde 401 e devolve ok=false.
func (s *servidor) exigirSessao(w http.ResponseWriter, r *http.Request) (sessaoWeb, bool) {
	sess, ok := s.sessoes.obter(lerCookieSessao(r))
	if !ok {
		respondErro(w, http.StatusUnauthorized, "sessao invalida ou expirada, faca login novamente")
		return sessaoWeb{}, false
	}
	return sess, true
}

func (s *servidor) handleMe(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"login": sess.login})
}

func (s *servidor) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Login string `json:"login"`
		Senha string `json:"senha"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Login == "" || body.Senha == "" {
		respondErro(w, http.StatusBadRequest, "login e senha sao obrigatorios")
		return
	}
	if !campoValido(body.Login) {
		respondErro(w, http.StatusBadRequest, "login contem caracteres invalidos")
		return
	}
	if ae := backendLogin(s.backendAddr, body.Login, body.Senha); ae != nil {
		respondAPIErro(w, ae)
		return
	}
	id, err := s.sessoes.criar(body.Login, body.Senha)
	if err != nil {
		respondErro(w, http.StatusInternalServerError, "falha ao criar sessao")
		return
	}
	definirCookieSessao(w, id)
	respondJSON(w, http.StatusOK, map[string]string{"login": body.Login})
}

func (s *servidor) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.sessoes.remover(lerCookieSessao(r))
	limparCookieSessao(w)
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *servidor) handlePublicar(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		Rota     []string `json:"rota"`
		Data     string   `json:"data"`
		Hora     string   `json:"hora"`
		Assentos int      `json:"assentos"`
		Preco    float64  `json:"preco"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Rota) < 2 {
		respondErro(w, http.StatusBadRequest, "informe pelo menos duas cidades na rota")
		return
	}
	for _, cidade := range body.Rota {
		if !campoDeLista(cidade) {
			respondErro(w, http.StatusBadRequest, "nome de cidade invalido: "+cidade)
			return
		}
	}
	if !camposValidos(body.Data, body.Hora) {
		respondErro(w, http.StatusBadRequest, "data/hora invalidas")
		return
	}
	id, ae := backendPublicar(s.backendAddr, sess.login, sess.senha, body.Rota, body.Data, body.Hora, body.Assentos, body.Preco)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"id": id})
}

func (s *servidor) handleListarCaronas(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	caronas, ae := backendListarCaronas(s.backendAddr, sess.login, sess.senha)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"caronas": caronas})
}

func (s *servidor) handleDetalheCarona(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		CaronaID string `json:"caronaId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !campoValido(body.CaronaID) {
		respondErro(w, http.StatusBadRequest, "caronaId invalido")
		return
	}
	trechos, ae := backendDetalheCarona(s.backendAddr, sess.login, sess.senha, body.CaronaID)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"trechos": trechos})
}

func (s *servidor) handleCancelarCarona(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		CaronaID string `json:"caronaId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !campoValido(body.CaronaID) {
		respondErro(w, http.StatusBadRequest, "caronaId invalido")
		return
	}
	if ae := backendCancelarCarona(s.backendAddr, sess.login, sess.senha, body.CaronaID); ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *servidor) handleBuscar(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		Origem  string `json:"origem"`
		Destino string `json:"destino"`
		Data    string `json:"data"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !camposValidos(body.Origem, body.Destino, body.Data) {
		respondErro(w, http.StatusBadRequest, "origem, destino e data sao obrigatorios")
		return
	}
	itinerarios, ae := backendBuscar(s.backendAddr, sess.login, sess.senha, body.Origem, body.Destino, body.Data)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"itinerarios": itinerarios})
}

func (s *servidor) handleReservar(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		Pernas []struct {
			CaronaID string `json:"caronaId"`
			Origem   string `json:"origem"`
			Destino  string `json:"destino"`
		} `json:"pernas"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if len(body.Pernas) == 0 {
		respondErro(w, http.StatusBadRequest, "informe ao menos uma perna do itinerario")
		return
	}
	pedido := make([]pernaPedido, 0, len(body.Pernas))
	for _, p := range body.Pernas {
		if !campoDePerna(p.CaronaID) || !campoDePerna(p.Origem) || !campoDePerna(p.Destino) {
			respondErro(w, http.StatusBadRequest, "perna de itinerario invalida")
			return
		}
		pedido = append(pedido, pernaPedido{CaronaID: p.CaronaID, Origem: p.Origem, Destino: p.Destino})
	}
	reserva, ae := backendReservar(s.backendAddr, sess.login, sess.senha, pedido)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, reserva)
}

func (s *servidor) handleMinhasReservas(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	reservas, ae := backendMinhasReservas(s.backendAddr, sess.login, sess.senha)
	if ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"reservas": reservas})
}

func (s *servidor) handleCancelarReserva(w http.ResponseWriter, r *http.Request) {
	sess, ok := s.exigirSessao(w, r)
	if !ok {
		return
	}
	var body struct {
		ReservaID string `json:"reservaId"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if !campoValido(body.ReservaID) {
		respondErro(w, http.StatusBadRequest, "reservaId invalido")
		return
	}
	if ae := backendCancelarReserva(s.backendAddr, sess.login, sess.senha, body.ReservaID); ae != nil {
		respondAPIErro(w, ae)
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// somentePost rejeita qualquer metodo HTTP diferente de POST com 405,
// evitando que um handler que so faz sentido para POST seja acionado por
// engano via GET (ex.: um crawler ou o proprio navegador prefetchando).
func somentePost(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			respondErro(w, http.StatusMethodNotAllowed, "metodo nao permitido")
			return
		}
		h(w, r)
	}
}
