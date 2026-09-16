# VAIJUNTO — Sistema de Caronas Compartilhadas

Implementação em Go (apenas biblioteca padrão — sem frameworks de RPC,
mensageria ou serialização de terceiros) do Problema 1 (TEC502): um
servidor central que mantém o estado de caronas e reservas, um cliente
motorista e um cliente passageiro, comunicando-se por um protocolo de
aplicação próprio sobre sockets TCP nativos.

## 1. Arquitetura

```
┌────────────────┐        TCP / protocolo VAIJUNTO        ┌──────────────────┐
│ Cliente         │ ───────────────────────────────────▶  │                  │
│ Motorista (CLI) │ ◀───────────────────────────────────  │ Servidor Central │
└────────────────┘                                        │  (estado em      │
┌────────────────┐        TCP / protocolo VAIJUNTO        │   memória)       │
│ Cliente         │ ───────────────────────────────────▶  │                  │
│ Passageiro (CLI)│ ◀───────────────────────────────────  │                  │
└────────────────┘                                        └──────────────────┘
```

- **Servidor** (`cmd/server`): escuta uma porta TCP, aceita uma goroutine
  por conexão e delega toda a lógica de negócio para `internal/store`, que
  concentra o estado (usuários, caronas, reservas) e o controle de
  concorrência. Não há réplica nem banco de dados externo — um único
  processo, um único ponto de verdade, como exigido pelo enunciado.
- **Cliente motorista** (`cmd/motorista`): CLI que autentica, publica
  carona, lista suas caronas, mostra os passageiros confirmados por
  trecho e cancela caronas.
- **Cliente passageiro** (`cmd/passageiro`): CLI que autentica, busca
  itinerários, confirma reserva, lista e cancela suas reservas.

### Modelo de dados

- **Carona**: motorista, `Rota` (sequência ordenada de cidades), data,
  hora, total de assentos, preço por trecho.
- **Trecho**: segmento entre duas cidades **adjacentes** da rota
  (`Rota[i] -> Rota[i+1]`). A disponibilidade é controlada por trecho: uma
  matriz `Ocupacao[trecho][assento] = login do passageiro` (ou vazio).
  Uma perna de viagem que atravessa vários trechos de uma mesma carona
  (ex.: embarcar na 1ª cidade e desembarcar na 4ª) precisa do **mesmo**
  número de assento livre em todos os trechos intermediários — é
  fisicamente o mesmo banco do carro.
- **Reserva**: um passageiro e uma lista de "pernas" (`carona`, `origem`,
  `destino`, `assento`, `preço`). Uma reserva pode ter várias pernas de
  motoristas diferentes (itinerário com baldeação).

## 2. Comunicação

- Transporte: **TCP** (via `net.Listen`/`net.Dial` da biblioteca padrão).
  TCP foi escolhido porque o protocolo exige confiabilidade e ordem de
  entrega (uma reserva não pode chegar corrompida ou fora de ordem; UDP
  exigiria reimplementar isso manualmente, sem benefício aqui).
- Cada cliente mantém **uma conexão TCP persistente** por sessão; a
  autenticação (`LOGIN`) é feita uma vez por conexão, e o servidor guarda
  o login associado àquele socket (não há tokens trafegando a cada
  comando).
- O servidor trata cada conexão em uma goroutine dedicada
  (`internal/server/server.go`, `handleConn`). A entrada e a saída de um
  cliente são tratadas assim:
  - **Entrada**: `ln.Accept()` bloqueia até uma nova conexão chegar; cada
    uma dispara uma goroutine nova, sem limite artificial (o runtime do Go
    multiplexa as goroutines sobre um pool de threads do SO).
  - **Saída limpa**: comando `SAIR`, respondido com `OK|SAIR` antes de
    fechar o socket.
  - **Saída abrupta**: qualquer erro de leitura (conexão resetada, EOF,
    timeout) encerra a goroutine e fecha o socket (`defer conn.Close()`).
    Como cada reserva é **atômica dentro de uma única requisição**, não
    existe "estado pendente" amarrado à conexão que precise ser desfeito
    quando ela cai — não há vazamento de assentos bloqueados.

## 3. Protocolo de Aplicação (API Remota)

Protocolo **próprio, textual, orientado a linha**, implementado em
`internal/proto`. Nenhuma biblioteca de serialização/RPC é usada — apenas
`strings`/`bufio` do próprio Go.

### 3.1 Formato das mensagens

```
COMANDO|campo1|campo2|...|campoN\n
```

- Cada mensagem ocupa **uma linha**, terminada por `\n`.
- Campos são separados por `|`. Um campo nunca pode conter `|`, `\n` ou
  `\r` — isso é validado tanto ao montar (`SanitizarCampo`) quanto ao
  decodificar mensagens.
- Listas (ex.: cidades de uma rota) usam `,` como separador.
- Uma "perna" de itinerário (carona + origem + destino [+ preço/assento])
  usa `:` entre subcampos; várias pernas de um mesmo itinerário são
  separadas por `;`.
- Respostas de listagem (várias linhas) sempre terminam com uma linha
  `FIM` sozinha, sinalizando o fim do bloco.
- **Encapsulamento/validação**: todo comando recebido é validado (número
  de campos, tipos numéricos com `strconv`) antes de ser interpretado. Uma
  linha malformada gera `ERRO|400|<mensagem>` e a conexão **continua
  aberta** — o servidor nunca derruba um cliente por um erro de protocolo,
  apenas descarta a mensagem inválida.

### 3.2 Fluxo de conexão e autenticação

1. Cliente abre TCP para `servidor:porta`.
2. Cliente envia `LOGIN|login|senha`.
   - Primeiro login de um usuário faz cadastro automático (simplificação
     deliberada: o foco do protótipo é a coordenação de caronas, não um
     fluxo de cadastro/CRUD de usuários).
   - Servidor responde `OK|LOGIN|<login>` ou `ERRO|401|credenciais invalidas`.
3. Todos os comandos seguintes nessa conexão usam implicitamente esse
   login (sem token, pois a sessão está amarrada ao socket TCP).
4. Cliente encerra com `SAIR` (resposta `OK|SAIR`) ou apenas fecha o
   socket.

### 3.3 Operações disponíveis

| Comando (cliente → servidor) | Campos | Resposta de sucesso | Papel |
|---|---|---|---|
| `LOGIN` | `login,senha` | `OK\|LOGIN\|login` | ambos |
| `PUBLICAR` | `rota(csv),data,hora,assentos,preco` | `OK\|CARONA\|id` | motorista |
| `LISTAR_CARONAS` | — | linhas `CARONA\|...` + `FIM` | motorista |
| `DETALHE_CARONA` | `caronaId` | linhas `TRECHO\|...` + `FIM` | motorista |
| `CANCELAR_CARONA` | `caronaId` | `OK\|CANCELAR_CARONA` | motorista |
| `BUSCAR` | `origem,destino,data` | linhas `ITINERARIO\|...` + `FIM` | passageiro |
| `RESERVAR` | `pernas` (carona:origem:destino;...) | `OK\|RESERVA\|id\|precoTotal\|pernas` | passageiro |
| `MINHAS_RESERVAS` | — | linhas `RESERVA\|...` + `FIM` | passageiro |
| `CANCELAR_RESERVA` | `reservaId` | `OK\|CANCELAR_RESERVA` | passageiro |
| `SAIR` | — | `OK\|SAIR` (fecha conexão) | ambos |

Qualquer erro é `ERRO|<codigo>|<mensagem>`, com códigos inspirados em
HTTP: `400` dados inválidos, `401` não autenticado/credenciais inválidas,
`403` não autorizado (ex.: motorista tentando ver carona de outro),
`404` não encontrado, `409` conflito (trecho indisponível, reserva já
cancelada), `500` erro interno.

### 3.4 Exemplos concretos de mensagens trocadas

Publicar uma carona:

```
C→S: LOGIN|joaomotorista|1234
S→C: OK|LOGIN|joaomotorista
C→S: PUBLICAR|Salvador,Feira de Santana,Vitoria da Conquista|2026-10-01|08:00|3|40.00
S→C: OK|CARONA|C1
```

Buscar e reservar um itinerário com baldeação (exemplo do enunciado:
Salvador → Vitória da Conquista sem carona direta):

```
C→S: BUSCAR|Salvador|Vitoria da Conquista|2026-10-01
S→C: ITINERARIO|0|80.00|2|C1:Salvador:Feira de Santana:40.00;C2:Feira de Santana:Vitoria da Conquista:40.00
S→C: FIM
C→S: RESERVAR|C1:Salvador:Feira de Santana;C2:Feira de Santana:Vitoria da Conquista
S→C: OK|RESERVA|R7|80.00|C1:Salvador:Feira de Santana:1:40.00;C2:Feira de Santana:Vitoria da Conquista:1:40.00
```

Mensagem malformada (encapsulamento/validação):

```
C→S: RESERVAR
S→C: ERRO|400|uso: RESERVAR|carona:origem:destino;carona2:origem2:destino2;...
```

## 4. Busca de Itinerários

Implementada em `Store.Buscar` (`internal/store/store.go`):

1. Para cada carona ativa na data pedida, todo **par de cidades i<j da
   rota** com pelo menos um assento livre em comum vira uma aresta
   `origem → destino` no grafo de busca (isso já cobre embarcar/desembarcar
   em qualquer par de cidades de uma mesma rota, inclusive pulando cidades
   intermediárias).
2. Uma busca em profundidade (DFS), limitada a no máximo 3 pernas e sem
   repetir cidade (caminho simples), combina arestas de **caronas
   diferentes** para montar itinerários com baldeação quando não existe
   trecho direto.
3. Os resultados são ordenados por **preço total crescente** e, em caso de
   empate, por **número de pernas crescente** (itinerários diretos são
   preferidos a baldeações de mesmo preço).

## 5. Concorrência

- **Uma goroutine por conexão** — o modelo de concorrência do próprio Go
  (multiplexação M:N sobre threads do SO) já cumpre o papel de um "thread
  pool", sem código extra: milhares de conexões leves são viáveis sem
  esgotar threads do sistema operacional.
- **Duas granularidades de trava**:
  - `Store.mu` (`sync.RWMutex`): protege apenas a *existência* dos
    registros (inserir/remover caronas e reservas dos mapas). Buscas usam
    `RLock` (leitura concorrente livre).
  - Cada `model.Carona` tem seu **próprio `sync.Mutex`**, protegendo a
    matriz de ocupação de assentos daquela carona especificamente. Isso
    permite que reservas em caronas diferentes prossigam **em paralelo**,
    sem contenção global.

## 6. Atomicidade da Reserva

`Store.Reservar` garante que um itinerário com várias pernas (de
motoristas possivelmente diferentes) seja confirmado por inteiro ou não
seja confirmado de forma alguma:

1. **Ordem global de travas**: todas as caronas envolvidas no pedido são
   travadas **em ordem crescente de ID**, nunca na ordem em que aparecem
   no pedido do cliente. Dois passageiros disputando os mesmos trechos em
   ordens diferentes (ex.: passageiro A pede carona `C2` depois `C1`,
   passageiro B pede `C1` depois `C2`) sempre adquirem as travas na mesma
   sequência `C1, C2` — **elimina deadlock por construção** (é a técnica
   clássica de "lock ordering").
2. **Validação em duas fases**: com todas as travas seguras, o servidor
   primeiro **verifica** a disponibilidade de todas as pernas (usando um
   overlay local em memória, para o caso de duas pernas do mesmo pedido
   caírem na mesma carona) sem alterar nada. Só depois de confirmar que
   **todas** as pernas têm assento livre é que a ocupação real é gravada.
3. **Falha em qualquer perna aborta tudo**: se qualquer trecho não tiver
   mais assento livre (por exemplo, o último trecho do itinerário foi
   vendido para outro passageiro entre a busca e a confirmação), a função
   retorna erro `409` **sem ter gravado nada** — não há necessidade de
   "desfazer" porque nada foi escrito até a fase de efetivação.
4. As travas são liberadas (`defer`) assim que a função retorna, sucesso
   ou erro.

## 7. Interação com os clientes

- **Cliente motorista**: menu de terminal para publicar carona (rota,
  data, hora, assentos, preço/trecho), listar suas caronas, consultar
  passageiros confirmados por trecho (comando `DETALHE_CARONA`, que expõe
  a matriz de ocupação assento a assento) e cancelar uma carona (cancela
  em cascata as reservas dependentes).
- **Cliente passageiro**: busca por origem/destino/data, exibe as opções
  numeradas com preço e pernas, permite reservar pelo índice exibido,
  consultar (`MINHAS_RESERVAS`) e cancelar reservas.

## 8. Confiabilidade

- **Cliente encerrado abruptamente**: como cada `RESERVAR` é processado e
  confirmado (ou rejeitado) numa única ida-e-volta, não existe "reserva
  pendente" que dependa de uma segunda mensagem do cliente — se a conexão
  cair a meio de uma operação de leitura/escrita, o pior caso é o cliente
  não receber a confirmação de algo que já foi (ou não foi) decidido
  atomicamente no servidor; nunca um assento fica "meio reservado".
- **Assentos permanentemente bloqueados**: não existe fase de "hold"
  (reserva provisória) no protocolo — por isso não há como um assento
  ficar preso por uma reserva iniciada e nunca concluída.
- **Timeouts de socket**: cada conexão tem um `SetReadDeadline` renovado a
  cada linha lida (`idleTimeout` = 10 min em `internal/server/server.go`);
  conexões ociosas (cliente travado, processo morto sem fechar o socket)
  são encerradas pelo servidor, liberando a goroutine.
- **Tratamento de exceções nos sockets**: qualquer erro de leitura/escrita
  (reset de conexão, timeout, EOF) apenas encerra aquela goroutine — nunca
  derruba o processo do servidor nem afeta outras conexões.

## 9. Testes automatizados

- `internal/store/store_test.go` — testes de unidade sobre a lógica pura:
  - `TestReservaConcorrenteMesmoTrecho`: 50 goroutines disputando 5
    assentos do mesmo trecho ao mesmo tempo; verifica que **exatamente 5**
    reservas são aceitas e que nenhum assento é concedido duas vezes.
  - `TestReservaAtomicaItinerarioComTransbordo`: esgota o segundo trecho de
    um itinerário de duas pernas e confirma que a primeira perna **não**
    fica reservada (atomicidade "tudo ou nada").
  - `TestBuscaCombinaCaronasDiferentes`: reproduz o exemplo do enunciado
    (Salvador → Vitória da Conquista combinando dois motoristas).
  - `TestCancelarReservaLiberaAssento`: cancelamento libera o assento para
    reuso.
- `internal/server/server_test.go` — teste de **integração via socket TCP
  real** (sobe o servidor numa porta efêmera e conecta clientes de
  verdade, não chama funções internas diretamente):
  - `TestConcorrenciaMultiplosClientesSockets`: publica uma carona com 10
    assentos e dispara **100 clientes TCP concorrentes** tentando reservar
    o mesmo trecho; verifica que exatamente 10 são confirmados e reporta o
    tempo total/médio por cliente (medição de desempenho sob carga).

Rodar os testes:

```bash
go test ./... -v
```

(a flag `-race` do Go é recomendada sempre que houver um compilador C
disponível no ambiente: `go test ./... -race`.)

## 10. Emulação com Docker

Cada componente vira uma imagem mínima (build multi-stage, binário
estático, sem dependência de runtime além da libc do Alpine):

- `Dockerfile.server`
- `Dockerfile.motorista`
- `Dockerfile.passageiro`

### 10.1 Teste rápido na mesma máquina

```bash
docker compose up --build -d server
docker compose run --rm motorista     # menu interativo do motorista
docker compose run --rm passageiro    # menu interativo do passageiro
```

Aqui o Docker Compose cria uma rede virtual própria e os containers se
enxergam pelo nome do serviço (`server:9000`) — não é o cenário de
"múltiplas máquinas", mas serve para validar tudo antes de ir ao
laboratório.

### 10.2 Emulação realista em múltiplos computadores (dois PCs em casa, VPN, ou laboratório LARSID/LADICA)

O ponto chave de conectividade entre containers em **máquinas distintas**
é: um container só é alcançável de fora se a porta do container for
**publicada (`-p`) na interface de rede real do host**, e o outro lado
precisa discar para o **IP daquele host** (não para o nome do container,
que só existe dentro da rede virtual do Docker daquela máquina).

**No PC/máquina do servidor** (descubra o IP com `ip addr` no Linux do
laboratório, ou `ipconfig` no Windows de casa):

```bash
docker build -f Dockerfile.server -t vaijunto-server .
docker run --rm -p 9000:9000 vaijunto-server
```

**No PC/máquina do motorista** (troque `192.168.x.x` pelo IP real do
servidor — na rede de casa, o IP da LAN/roteador; via VPN, o IP atribuído
pela VPN; no laboratório, o IP da outra máquina do LARSID/LADICA):

```bash
docker build -f Dockerfile.motorista -t vaijunto-motorista .
docker run --rm -it -e VAIJUNTO_ADDR=192.168.x.x:9000 vaijunto-motorista
```

**No PC/máquina do passageiro** (pode ser uma terceira máquina, ou a
mesma do motorista em outro terminal):

```bash
docker build -f Dockerfile.passageiro -t vaijunto-passageiro .
docker run --rm -it -e VAIJUNTO_ADDR=192.168.x.x:9000 vaijunto-passageiro
```

Isso é exatamente o cenário pedido: **dois computadores de casa** fazendo
o papel de duas máquinas do laboratório (um roda o servidor, o outro roda
os clientes apontando para o IP do primeiro), ou o mesmo teste feito por
**VPN** entre duas redes diferentes — o protocolo não faz nenhuma
suposição sobre a topologia de rede além de "um endereço IP:porta TCP
alcançável", então funciona igual nos dois casos e no laboratório Linux.

**Vantagens da abordagem com containers** (para o relatório, item
"Emulação"): builds reprodutíveis e idênticos em Windows/Linux/Mac;
isolamento de dependências (o laboratório não precisa ter Go instalado,
só Docker); é trivial subir várias instâncias de clientes na mesma
máquina para simular vários usuários; e o mesmo par de comandos
`docker build` / `docker run` funciona tanto em casa quanto no
LARSID/LADICA, sem alterar uma linha de código — só o `VAIJUNTO_ADDR`.

## 11. Execução sem Docker (desenvolvimento local)

```bash
go run ./cmd/server -addr :9000
go run ./cmd/motorista -addr localhost:9000
go run ./cmd/passageiro -addr localhost:9000
```

## 12. Simplificações assumidas (documentar no relatório)

- Autenticação faz cadastro automático no primeiro login (sem fluxo de
  cadastro separado) — o foco do trabalho é a coordenação de caronas.
- Preço por trecho é único por carona (não varia por par de cidades).
- Busca de itinerários limita-se a 3 pernas e não considera compatibilidade
  de horário entre baldeações (apenas a data).
- Sem persistência em disco: o estado vive em memória do processo do
  servidor (reinício do servidor zera o estado), o que está de acordo com
  a restrição de não usar SGBD ou serviço externo de coordenação.

## 13. Interface Web (HTML)

Além dos clientes de terminal, `cmd/webui` implementa um **gateway HTTP**
que serve uma interface web (HTML/CSS/JS estáticos, sem framework nem
build step) para quem preferir não usar a linha de comando. Ele não
duplica nenhuma lógica de negócio: para cada ação da página, abre uma
conexão TCP curta com o servidor central e fala exatamente o mesmo
protocolo `internal/proto` dos clientes `cmd/motorista`/`cmd/passageiro`
(via `internal/clientio`), depois traduz a resposta para JSON. O estado
de caronas/reservas continua existindo só no servidor central; o gateway
guarda em memória apenas a associação entre o cookie de sessão do
navegador e a credencial usada para reabrir a conexão a cada requisição
(não há tokens no protocolo — ver seção 2).

Rodar localmente (com o servidor já de pé):

```bash
go run ./cmd/server -addr :9000
go run ./cmd/webui -addr localhost:9000 -listen :8080
```

Abra `http://localhost:8080` no navegador. As mesmas variáveis de
ambiente dos clientes de terminal se aplicam (`VAIJUNTO_ADDR` para o
endereço do servidor); `WEBUI_ADDR` controla em que endereço a própria
interface web escuta (padrão `:8080`).

Com Docker Compose: `docker compose up --build -d server webui` e acesse
`http://localhost:8080` (ver `Dockerfile.webui`).

Qualquer login/senha usado na interface web cadastra o usuário
automaticamente no primeiro acesso, exatamente como nos clientes de
terminal (mesma simplificação da seção 12) — e uma mesma conta pode usar
tanto as ações de motorista quanto as de passageiro (o servidor não
amarra um login a um papel fixo).

**Nota de correção**: ao implementar o gateway foi encontrado e corrigido
um bug pré-existente em `DETALHE_CARONA` (`internal/server/server.go`):
quando o comando falhava (carona inexistente ou de outro motorista), o
servidor enviava só a linha `ERRO`, sem o `FIM` que fecha o bloco de
resposta — qualquer cliente que lê essa resposta como bloco (`LerBloco`,
usada pelos dois clientes de terminal e pelo gateway) ficava esperando
para sempre a linha seguinte, e a próxima resposta do servidor era lida
como se ainda pertencesse a esse bloco, dessincronizando a conexão. Os
testes automatizados (seção 9) não cobriam esse caminho de erro. A
correção garante que o bloco sempre termine com `FIM`, inclusive nos
casos de erro.
