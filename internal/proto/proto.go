// Package proto define o protocolo de aplicacao do VAIJUNTO: um protocolo
// textual, orientado a linha, transportado sobre um socket TCP. Nenhuma
// biblioteca de serializacao, RPC ou middleware de mensagens e usada —
// apenas string/bufio da biblioteca padrao da linguagem.
//
// Formato de uma mensagem:
//
//	COMANDO|campo1|campo2|...|campoN\n
//
// Regras de encapsulamento:
//   - Cada mensagem ocupa exatamente uma linha, terminada por "\n".
//   - Campos sao separados por "|". Um campo nunca contem "|", "\n" ou "\r"
//     (SanitizarCampo rejeita valores com esses caracteres antes de montar
//     a mensagem, e DecodificarLinha os rejeita ao receber).
//   - Quando um campo e uma LISTA (ex.: as cidades de uma rota), os itens
//     sao separados por virgula ",".
//   - Quando um campo representa uma PERNA de itinerario (carona+origem+
//     destino), as subpartes sao separadas por ":"; varias pernas de um
//     mesmo itinerario sao separadas por ";".
//   - Toda linha recebida e validada (numero de campos, tipos numericos)
//     antes de ser interpretada; uma linha malformada gera ERRO|400|...
//     e a conexao continua aberta (nao derruba o cliente por um erro de
//     protocolo).
//   - Respostas que envolvem varias linhas (listagens) terminam sempre
//     com uma linha "FIM" sozinha, sinalizando o fim do bloco ao cliente.
package proto

import (
	"errors"
	"strings"
)

const (
	SepCampo = "|"
	SepLista = ","
	SepPerna = ";"
	SepSub   = ":"

	// FimBloco marca o final de uma resposta com múltiplas linhas.
	FimBloco = "FIM"
)

var ErrCampoInvalido = errors.New("campo contem separador reservado")

// SanitizarCampo garante que um valor fornecido pelo usuario (nome de
// cidade, login, etc.) nao quebre o encapsulamento do protocolo.
func SanitizarCampo(v string) (string, error) {
	if strings.ContainsAny(v, "|\n\r") {
		return "", ErrCampoInvalido
	}
	return v, nil
}

// Codificar monta uma linha de protocolo a partir do comando e dos campos.
func Codificar(comando string, campos ...string) string {
	partes := append([]string{comando}, campos...)
	return strings.Join(partes, SepCampo)
}

// DecodificarLinha separa uma linha recebida em seus campos, apos remover
// o terminador de linha. Retorna a lista de campos (campos[0] e o
// comando).
func DecodificarLinha(linha string) []string {
	linha = strings.TrimRight(linha, "\r\n")
	if linha == "" {
		return nil
	}
	return strings.Split(linha, SepCampo)
}

func JuntarLista(itens []string) string {
	return strings.Join(itens, SepLista)
}

func SepararLista(v string) []string {
	if v == "" {
		return nil
	}
	partes := strings.Split(v, SepLista)
	for i, p := range partes {
		partes[i] = strings.TrimSpace(p)
	}
	return partes
}

// Perna textual: CaronaID:Origem:Destino[:Preco]
func JuntarPerna(campos ...string) string {
	return strings.Join(campos, SepSub)
}

func JuntarPernas(pernas []string) string {
	return strings.Join(pernas, SepPerna)
}

func SepararPernas(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Split(v, SepPerna)
}

func SepararSub(v string) []string {
	return strings.Split(v, SepSub)
}
