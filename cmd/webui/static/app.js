// Interface web do VAIJUNTO. JavaScript puro (sem framework/bundler),
// falando com a API JSON exposta pelo gateway (cmd/webui) via fetch().
// Toda entrada dinamica e inserida no DOM como texto (textContent / o
// helper `h`), nunca via innerHTML com dados do servidor, para nao abrir
// espaco a XSS a partir de nomes de cidade ou ids digitados por outros
// usuarios do sistema.
(function () {
  "use strict";

  // ---------- utilidades ----------

  function h(tag, attrs, ...filhos) {
    const el = document.createElement(tag);
    for (const [chave, valor] of Object.entries(attrs || {})) {
      if (chave === "class") el.className = valor;
      else if (chave.startsWith("on") && typeof valor === "function") {
        el.addEventListener(chave.slice(2), valor);
      } else if (valor !== false && valor !== null && valor !== undefined) {
        el.setAttribute(chave, valor === true ? "" : String(valor));
      }
    }
    for (const filho of filhos) {
      if (filho === null || filho === undefined) continue;
      el.append(filho.nodeType ? filho : document.createTextNode(String(filho)));
    }
    return el;
  }

  function limpar(el) {
    while (el.firstChild) el.removeChild(el.firstChild);
  }

  async function chamarAPI(caminho, corpo) {
    const opcoes = {
      method: corpo === undefined ? "GET" : "POST",
      credentials: "same-origin",
    };
    if (corpo !== undefined) {
      opcoes.headers = { "Content-Type": "application/json" };
      opcoes.body = JSON.stringify(corpo);
    }
    const resp = await fetch(caminho, opcoes);
    let dados = null;
    try {
      dados = await resp.json();
    } catch (e) {
      dados = null;
    }
    if (!resp.ok) {
      const msg = dados && dados.erro ? dados.erro : `erro HTTP ${resp.status}`;
      const erro = new Error(msg);
      erro.status = resp.status;
      throw erro;
    }
    return dados;
  }

  function formatarRota(rota) {
    return (rota || []).join(" -> ");
  }

  // ---------- referencias de elementos ----------

  const telaLogin = document.getElementById("telaLogin");
  const telaApp = document.getElementById("telaApp");
  const quemEstaLogado = document.getElementById("quemEstaLogado");
  const loginAtualEl = document.getElementById("loginAtual");

  const formLogin = document.getElementById("formLogin");
  const erroLogin = document.getElementById("erroLogin");
  const btnSair = document.getElementById("btnSair");

  const abas = document.querySelectorAll(".aba");
  const painelMotorista = document.getElementById("painelMotorista");
  const painelPassageiro = document.getElementById("painelPassageiro");

  const formPublicar = document.getElementById("formPublicar");
  const msgPublicar = document.getElementById("msgPublicar");
  const listaCaronas = document.getElementById("listaCaronas");
  const btnAtualizarCaronas = document.getElementById("btnAtualizarCaronas");

  const formBuscar = document.getElementById("formBuscar");
  const msgBuscar = document.getElementById("msgBuscar");
  const listaItinerarios = document.getElementById("listaItinerarios");
  const listaReservas = document.getElementById("listaReservas");
  const btnAtualizarReservas = document.getElementById("btnAtualizarReservas");

  // ---------- troca de tela login <-> app ----------

  function mostrarApp(login) {
    telaLogin.classList.add("hidden");
    telaApp.classList.remove("hidden");
    quemEstaLogado.classList.remove("hidden");
    loginAtualEl.textContent = login;
    carregarCaronas();
    carregarReservas();
  }

  function mostrarLogin() {
    telaApp.classList.add("hidden");
    quemEstaLogado.classList.add("hidden");
    telaLogin.classList.remove("hidden");
  }

  async function verificarSessao() {
    try {
      const dados = await chamarAPI("/api/me");
      mostrarApp(dados.login);
    } catch (e) {
      mostrarLogin();
    }
  }

  formLogin.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    erroLogin.classList.add("hidden");
    const login = document.getElementById("loginLogin").value.trim();
    const senha = document.getElementById("loginSenha").value;
    try {
      const dados = await chamarAPI("/api/login", { login, senha });
      formLogin.reset();
      mostrarApp(dados.login);
    } catch (e) {
      erroLogin.textContent = e.message;
      erroLogin.classList.remove("hidden");
    }
  });

  btnSair.addEventListener("click", async () => {
    try {
      await chamarAPI("/api/logout", {});
    } catch (e) {
      // mesmo se a chamada falhar, seguimos e limpamos a tela local
    }
    mostrarLogin();
  });

  // ---------- abas ----------

  abas.forEach((botao) => {
    botao.addEventListener("click", () => {
      abas.forEach((b) => b.classList.remove("ativa"));
      botao.classList.add("ativa");
      const alvo = botao.dataset.aba;
      painelMotorista.classList.toggle("hidden", alvo !== "motorista");
      painelPassageiro.classList.toggle("hidden", alvo !== "passageiro");
    });
  });

  // ---------- motorista: publicar carona ----------

  formPublicar.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    msgPublicar.classList.add("hidden");

    const rota = document
      .getElementById("pubRota")
      .value.split(",")
      .map((c) => c.trim())
      .filter((c) => c.length > 0);
    const data = document.getElementById("pubData").value;
    const hora = document.getElementById("pubHora").value;
    const assentos = parseInt(document.getElementById("pubAssentos").value, 10);
    const preco = parseFloat(document.getElementById("pubPreco").value);

    if (rota.length < 2) {
      msgPublicar.textContent = "informe pelo menos duas cidades na rota, separadas por virgula";
      msgPublicar.className = "mensagem-erro";
      return;
    }

    try {
      const resp = await chamarAPI("/api/motorista/publicar", { rota, data, hora, assentos, preco });
      msgPublicar.textContent = `carona publicada com sucesso (id ${resp.id})`;
      msgPublicar.className = "mensagem";
      formPublicar.reset();
      document.getElementById("pubAssentos").value = 1;
      document.getElementById("pubPreco").value = "0.00";
      carregarCaronas();
    } catch (e) {
      msgPublicar.textContent = e.message;
      msgPublicar.className = "mensagem-erro";
    }
  });

  // ---------- motorista: listar / detalhar / cancelar caronas ----------

  btnAtualizarCaronas.addEventListener("click", carregarCaronas);

  async function carregarCaronas() {
    limpar(listaCaronas);
    listaCaronas.append(h("p", { class: "vazio" }, "carregando..."));
    try {
      const dados = await chamarAPI("/api/motorista/caronas", {});
      renderizarCaronas(dados.caronas || []);
    } catch (e) {
      limpar(listaCaronas);
      listaCaronas.append(h("p", { class: "mensagem-erro" }, e.message));
    }
  }

  function renderizarCaronas(caronas) {
    limpar(listaCaronas);
    if (caronas.length === 0) {
      listaCaronas.append(h("p", { class: "vazio" }, "nenhuma carona publicada ainda"));
      return;
    }
    for (const c of caronas) {
      const seloClasse = c.cancelada ? "selo cancelada" : "selo";
      const seloTexto = c.cancelada ? "cancelada" : "ativa";

      const areaDetalhe = h("div", { class: "hidden" });
      const btnDetalhe = h("button", { class: "botao-secundario" }, "Detalhes");
      btnDetalhe.addEventListener("click", () => alternarDetalheCarona(c.id, areaDetalhe, btnDetalhe));

      const acoes = [btnDetalhe];
      if (!c.cancelada) {
        const btnCancelar = h("button", { class: "botao-perigo" }, "Cancelar");
        btnCancelar.addEventListener("click", () => cancelarCarona(c.id, btnCancelar));
        acoes.push(btnCancelar);
      }

      const item = h(
        "div",
        { class: "item" },
        h(
          "div",
          { class: "item-cabecalho" },
          h(
            "div",
            {},
            h("div", { class: "item-titulo" }, `${c.id} — ${formatarRota(c.rota)}`),
            h("div", { class: "vazio" }, `${c.data} ${c.hora} | assentos: ${c.assentosTotal} | preco/trecho: R$${c.preco}`)
          ),
          h("div", {}, h("span", { class: seloClasse }, seloTexto))
        ),
        h("div", { class: "item-acoes" }, ...acoes),
        areaDetalhe
      );
      listaCaronas.append(item);
    }
  }

  async function alternarDetalheCarona(caronaId, container, botao) {
    if (!container.classList.contains("hidden")) {
      container.classList.add("hidden");
      limpar(container);
      return;
    }
    limpar(container);
    container.classList.remove("hidden");
    container.append(h("p", { class: "vazio" }, "carregando..."));
    try {
      const dados = await chamarAPI("/api/motorista/carona/detalhe", { caronaId });
      renderizarDetalheCarona(container, dados.trechos || []);
    } catch (e) {
      limpar(container);
      container.append(h("p", { class: "mensagem-erro" }, e.message));
    }
  }

  function renderizarDetalheCarona(container, trechos) {
    limpar(container);
    if (trechos.length === 0) {
      container.append(h("p", { class: "vazio" }, "sem trechos"));
      return;
    }
    const tabela = h(
      "table",
      { class: "trechos-tabela" },
      h(
        "thead",
        {},
        h("tr", {}, h("th", {}, "Trecho"), h("th", {}, "Assento"), h("th", {}, "Status"))
      )
    );
    const corpo = h("tbody", {});
    for (const t of trechos) {
      corpo.append(
        h(
          "tr",
          {},
          h("td", {}, `${t.origem} -> ${t.destino}`),
          h("td", {}, String(t.assento)),
          h("td", {}, t.status)
        )
      );
    }
    tabela.append(corpo);
    container.append(tabela);
  }

  async function cancelarCarona(caronaId, botao) {
    if (!window.confirm(`Cancelar a carona ${caronaId}? Isso cancela em cascata as reservas dependentes.`)) {
      return;
    }
    botao.disabled = true;
    try {
      await chamarAPI("/api/motorista/carona/cancelar", { caronaId });
      carregarCaronas();
    } catch (e) {
      window.alert(e.message);
      botao.disabled = false;
    }
  }

  // ---------- passageiro: buscar / reservar ----------

  let ultimaBusca = [];

  formBuscar.addEventListener("submit", async (ev) => {
    ev.preventDefault();
    msgBuscar.classList.add("hidden");
    limpar(listaItinerarios);

    const origem = document.getElementById("buscaOrigem").value.trim();
    const destino = document.getElementById("buscaDestino").value.trim();
    const data = document.getElementById("buscaData").value;

    try {
      const dados = await chamarAPI("/api/passageiro/buscar", { origem, destino, data });
      ultimaBusca = dados.itinerarios || [];
      renderizarItinerarios(ultimaBusca);
    } catch (e) {
      msgBuscar.textContent = e.message;
      msgBuscar.className = "mensagem-erro";
      msgBuscar.classList.remove("hidden");
    }
  });

  function renderizarItinerarios(itinerarios) {
    limpar(listaItinerarios);
    if (itinerarios.length === 0) {
      listaItinerarios.append(h("p", { class: "vazio" }, "nenhum itinerario encontrado"));
      return;
    }
    for (const it of itinerarios) {
      const pernasEl = h("ul", { class: "pernas" });
      for (const p of it.pernas) {
        pernasEl.append(h("li", {}, `${p.origem} -> ${p.destino} (carona ${p.caronaId}, R$${p.preco})`));
      }

      const btnReservar = h("button", {}, "Reservar");
      btnReservar.addEventListener("click", () => reservarItinerario(it, btnReservar));

      const item = h(
        "div",
        { class: "item" },
        h(
          "div",
          { class: "item-cabecalho" },
          h("div", { class: "item-titulo" }, `preco total: R$${it.precoTotal} | ${it.pernas.length} trecho(s)`),
          h("div", { class: "item-acoes" }, btnReservar)
        ),
        pernasEl
      );
      listaItinerarios.append(item);
    }
  }

  async function reservarItinerario(itinerario, botao) {
    botao.disabled = true;
    try {
      const pernas = itinerario.pernas.map((p) => ({
        caronaId: p.caronaId,
        origem: p.origem,
        destino: p.destino,
      }));
      const resp = await chamarAPI("/api/passageiro/reservar", { pernas });
      window.alert(`reserva confirmada! id=${resp.id} preco total=R$${resp.precoTotal}`);
      carregarReservas();
    } catch (e) {
      window.alert(e.message);
    } finally {
      botao.disabled = false;
    }
  }

  // ---------- passageiro: minhas reservas ----------

  btnAtualizarReservas.addEventListener("click", carregarReservas);

  async function carregarReservas() {
    limpar(listaReservas);
    listaReservas.append(h("p", { class: "vazio" }, "carregando..."));
    try {
      const dados = await chamarAPI("/api/passageiro/reservas", {});
      renderizarReservas(dados.reservas || []);
    } catch (e) {
      limpar(listaReservas);
      listaReservas.append(h("p", { class: "mensagem-erro" }, e.message));
    }
  }

  function renderizarReservas(reservas) {
    limpar(listaReservas);
    if (reservas.length === 0) {
      listaReservas.append(h("p", { class: "vazio" }, "nenhuma reserva encontrada"));
      return;
    }
    for (const r of reservas) {
      const ativa = r.status === "ATIVA";
      const seloClasse = ativa ? "selo" : "selo cancelada";

      const pernasEl = h("ul", { class: "pernas" });
      for (const p of r.pernas) {
        pernasEl.append(
          h("li", {}, `${p.origem} -> ${p.destino} (carona ${p.caronaId}, assento ${p.assento}, R$${p.preco})`)
        );
      }

      const acoes = [];
      if (ativa) {
        const btnCancelar = h("button", { class: "botao-perigo" }, "Cancelar");
        btnCancelar.addEventListener("click", () => cancelarReserva(r.id, btnCancelar));
        acoes.push(btnCancelar);
      }

      const item = h(
        "div",
        { class: "item" },
        h(
          "div",
          { class: "item-cabecalho" },
          h("div", { class: "item-titulo" }, `${r.id} — R$${r.precoTotal}`),
          h("div", {}, h("span", { class: seloClasse }, r.status), " ", ...acoes)
        ),
        pernasEl
      );
      listaReservas.append(item);
    }
  }

  async function cancelarReserva(reservaId, botao) {
    if (!window.confirm(`Cancelar a reserva ${reservaId}?`)) return;
    botao.disabled = true;
    try {
      await chamarAPI("/api/passageiro/reserva/cancelar", { reservaId });
      carregarReservas();
    } catch (e) {
      window.alert(e.message);
      botao.disabled = false;
    }
  }

  // ---------- inicializacao ----------

  verificarSessao();
})();
