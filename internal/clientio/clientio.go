// Package clientio concentra a parte de rede comum aos dois clientes
// (motorista e passageiro): abrir a conexao TCP e falar o protocolo de
// texto do VAIJUNTO (ver internal/proto).
package clientio

import (
	"bufio"
	"fmt"
	"net"
	"time"

	"vaijunto/internal/proto"
)

type Conexao struct {
	conn   net.Conn
	reader *bufio.Reader
	writer *bufio.Writer
}

func Conectar(addr string) (*Conexao, error) {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	return &Conexao{conn: conn, reader: bufio.NewReader(conn), writer: bufio.NewWriter(conn)}, nil
}

func (c *Conexao) Fechar() error {
	return c.conn.Close()
}

// DefinirPrazoLeitura limita por quanto tempo a proxima leitura (LerLinha
// ou LerBloco) pode bloquear esperando resposta do servidor. Usado por
// clientes que nao podem travar indefinidamente (ex.: o gateway HTTP da
// interface web, que precisa responder a requisicao do navegador).
func (c *Conexao) DefinirPrazoLeitura(d time.Duration) error {
	return c.conn.SetReadDeadline(time.Now().Add(d))
}

func (c *Conexao) Enviar(comando string, campos ...string) error {
	if _, err := c.writer.WriteString(proto.Codificar(comando, campos...) + "\n"); err != nil {
		return err
	}
	return c.writer.Flush()
}

// LerLinha le e decodifica uma unica linha de resposta.
func (c *Conexao) LerLinha() ([]string, error) {
	linha, err := c.reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	campos := proto.DecodificarLinha(linha)
	if len(campos) == 0 {
		return c.LerLinha()
	}
	return campos, nil
}

// LerBloco le linhas ate encontrar o marcador FIM, devolvendo cada linha
// (ja decodificada) que veio antes dele. Usado para respostas de listagem
// (CARONA, TRECHO, ITINERARIO, RESERVA, ...).
func (c *Conexao) LerBloco() ([][]string, error) {
	var linhas [][]string
	for {
		campos, err := c.LerLinha()
		if err != nil {
			return nil, err
		}
		if campos[0] == proto.FimBloco {
			return linhas, nil
		}
		linhas = append(linhas, campos)
	}
}

// EhErro reporta se a linha de resposta e um ERRO e, nesse caso, monta uma
// mensagem legivel.
func EhErro(campos []string) (bool, error) {
	if len(campos) > 0 && campos[0] == "ERRO" {
		codigo, msg := "?", "erro desconhecido"
		if len(campos) > 1 {
			codigo = campos[1]
		}
		if len(campos) > 2 {
			msg = campos[2]
		}
		return true, fmt.Errorf("servidor recusou (%s): %s", codigo, msg)
	}
	return false, nil
}
