"use strict";

// Биндинги Wails: window.go.main.App.<Method>() → Promise.
const api = new Proxy({}, {
  get: (_, name) => (...args) => window.go.main.App[name](...args),
});

const $ = (sel) => document.querySelector(sel);
const $$ = (sel) => document.querySelectorAll(sel);
const esc = (s) => String(s ?? "").replace(/[&<>"']/g, (c) => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
}[c]));

const state = {
  roots: [], // папки проектов
  repos: [],
  filter: "",
  sort: localStorage.getItem("sort") || "git",
  source: localStorage.getItem("source") || "all", // all | pinned | attention | proj:<path> | repo:<path>
  focus: localStorage.getItem("focus") || "",      // путь ворктри в инспекторе
  selected: new Set(),                             // мультивыбор (⌘/Shift-клик)
  anchor: "",                                      // начало диапазона для Shift-клика
  lastFetch: 0,
  loading: false,
};

// ---------- toasts ----------
function toast(msg, isError = false) {
  const el = document.createElement("div");
  el.className = "toast" + (isError ? " error" : "");
  el.textContent = msg;
  $("#toasts").appendChild(el);
  setTimeout(() => el.remove(), isError ? 8000 : 3000);
}
const fail = (e) => toast(String(e?.message ?? e), true);

// ---------- данные ----------
async function load() {
  if (state.loading) return;
  state.loading = true;
  $("#refresh-btn").disabled = true;
  try {
    state.roots = (await api.GetRoots()) || [];
    state.repos = state.roots.length ? (await api.ListRepos()) || [] : [];
    for (const r of state.repos) {
      r.worktrees = r.worktrees || [];
      for (const w of r.worktrees) w.conflicts = w.conflicts || [];
    }
    // выбор переживает обновление, но только для существующих ворктри
    const alive = new Set(allWorktrees().map(({ wt }) => wt.path));
    for (const p of state.selected) if (!alive.has(p)) state.selected.delete(p);
    if (!sourceExists(state.source)) setSource("all");
    if (!findWt(state.focus)) setFocus(visible()[0]?.wt.path || "");
    render();
  } catch (e) {
    fail(e);
    render();
  } finally {
    state.loading = false;
    $("#refresh-btn").disabled = false;
  }
}

const allWorktrees = () =>
  state.repos.flatMap((repo) => (repo.worktrees || []).map((wt) => ({ repo, wt })));
const findWt = (path) => allWorktrees().find(({ wt }) => wt.path === path);

const shortHome = (p) => p.replace(/^\/Users\/[^/]+/, "~");
// проект, в котором лежит путь (самая глубокая подходящая папка)
const rootOf = (p) => state.roots.filter((r) => p === r || p.startsWith(r + "/")).sort((a, b) => b.length - a.length)[0] || "";
function relPath(p) {
  const r = rootOf(p);
  if (r && p !== r) return p.slice(r.length + 1);
  return shortHome(p);
}
const projName = (root) => root.split("/").pop() || root;
const repoLabel = (repo) => multiProject() ? `${projName(repo.project)} / ${repo.name}` : repo.name;
const liveRepos = () => state.repos.filter((r) => !(r.error && r.path === r.project));
const multiProject = () => state.roots.length > 1;
const baseName = (ref) => (ref || "").replace(/^origin\//, "");

const rtf = new Intl.RelativeTimeFormat("ru", { numeric: "auto" });
function ago(unix) {
  if (!unix) return "";
  let v = unix - Date.now() / 1000;
  for (const [k, unit] of [[60, "second"], [60, "minute"], [24, "hour"], [7, "day"], [4.35, "week"], [12, "month"], [Infinity, "year"]]) {
    if (Math.abs(v) < k) return rtf.format(Math.round(v), unit);
    v /= k;
  }
}
// компактно для списка: 7м, 2ч, 3д, 2мес
function agoShort(unix) {
  if (!unix) return "";
  const s = Date.now() / 1000 - unix;
  if (s < 3600) return Math.max(1, Math.round(s / 60)) + "м";
  if (s < 86400) return Math.round(s / 3600) + "ч";
  if (s < 86400 * 30) return Math.round(s / 86400) + "д";
  if (s < 86400 * 365) return Math.round(s / 86400 / 30) + "мес";
  return Math.round(s / 86400 / 365) + "г";
}

function plural(n, one, few, many) {
  const m10 = n % 10, m100 = n % 100;
  if (m10 === 1 && m100 !== 11) return one;
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) return few;
  return many;
}
const files = (n) => `${n} ${plural(n, "файл", "файла", "файлов")}`;
const commits = (n) => `${n} ${plural(n, "коммит", "коммита", "коммитов")}`;

const branchLabel = (wt) => wt.branch || (wt.bare ? "(bare)" : `detached @ ${(wt.head || "").slice(0, 8)}`);
const wtTitle = (wt) => wt.name || branchLabel(wt);

const canPull = (wt) => wt.exists && wt.upstream && !wt.gone && !wt.rebasing;
const canRebase = (wt) => wt.exists && !wt.rebasing && wt.baseRef && wt.baseBehind > 0;

// Самая важная проблема ворктри и действие, которое её решает.
// level: 2 — нужно разобраться, 1 — отстаёт / есть изменения, 0 — в порядке.
function triage(wt) {
  const base = baseName(wt.baseRef);
  const c = wt.conflicts.length;
  if (!wt.exists) return { level: 2, text: "Папки ворктри больше нет — его можно убрать через Prune", act: "prune", label: "Prune" };
  if (wt.rebasing) return {
    level: 2, act: "rebase-continue", label: "Продолжить rebase",
    text: "Rebase остановился" + (c ? ` на конфликте в ${c === 1 ? wt.conflicts[0].split("/").pop() : files(c)}` : "") +
      " — разрешите конфликты и продолжите",
  };
  if (wt.gone) return { level: 2, text: "Ветка удалена на remote — скорее всего, уже смёржена", act: "remove", label: "Удалить ворктри…" };
  if (c && canRebase(wt)) return { level: 2, text: `Rebase на ${base} даст конфликт: ${files(c)}`, act: "rebase", label: "Rebase с конфликтами" };
  if (wt.dirty < 0) return { level: 1, text: "Не удалось прочитать git status" };
  if (wt.behind && canPull(wt)) return { level: 1, text: `На remote ${commits(wt.behind)}, которых нет локально`, act: "pull", label: `Pull ${wt.behind}` };
  if (canRebase(wt)) return { level: 1, text: `Отстаёт от ${base} на ${commits(wt.baseBehind)}`, act: "rebase", label: `Rebase на ${base}` };
  if (wt.dirty > 0) return { level: 1, text: `Незакоммиченные изменения: ${files(wt.dirty)}` };
  if (wt.ahead) return { level: 0, text: `${commits(wt.ahead)} ждут push` };
  if (wt.upstream) return { level: 0, text: `Совпадает с ${wt.upstream}` };
  if (wt.bare) return { level: 0, text: "Bare-репозиторий" };
  return { level: 0, text: wt.branch ? "Локальная ветка без upstream" : "Detached HEAD" };
}

// ---------- источники (сайдбар) ----------
const sources = [
  { id: "all", ic: "◫", label: "Все", f: () => true },
  { id: "pinned", ic: "◆", label: "Закреплённые", f: ({ wt }) => wt.pinned },
  { id: "attention", ic: "!", label: "Требуют внимания", f: ({ wt }) => triage(wt).level === 2, alert: true },
];

function setSource(id) {
  state.source = id;
  localStorage.setItem("source", id);
}

function sourceFilter() {
  const s = sources.find((x) => x.id === state.source);
  if (s) return s.f;
  const path = state.source.slice(5);
  if (state.source.startsWith("proj:")) return ({ repo }) => repo.project === path;
  return ({ repo }) => repo.path === path;
}

function sourceExists(id) {
  if (sources.some((s) => s.id === id)) return true;
  if (id.startsWith("proj:")) return state.roots.includes(id.slice(5));
  if (id.startsWith("repo:")) return state.repos.some((r) => r.path === id.slice(5));
  return false;
}

// заголовок списка и репо для ⌘N по текущему источнику
function sourceInfo() {
  const s = sources.find((x) => x.id === state.source);
  if (s) return { title: s.id === "all" ? "Все ворктри" : s.label };
  const path = state.source.slice(5);
  if (state.source.startsWith("proj:")) return { title: projName(path), project: path };
  const repo = state.repos.find((r) => r.path === path);
  return { title: repo ? repoLabel(repo) : "", repo };
}

function renderSources() {
  const items = allWorktrees();
  const count = (f) => items.filter(f).length;
  const btn = (id, ic, label, n, alert) => `
    <button class="src${state.source === id ? " on" : ""}" data-src="${esc(id)}">
      <span class="ic">${ic}</span><span class="lbl">${esc(label)}</span>
      <span class="n${alert && n ? " alert" : ""}">${n}</span>
    </button>`;
  const projects = state.roots.map((root) => {
    const repos = state.repos.filter((r) => r.project === root && !(r.error && r.path === root));
    const missing = state.repos.find((r) => r.project === root && r.error && r.path === root);
    const id = "proj:" + root;
    return `
      <div class="proj">
        <button class="proj-head${state.source === id ? " on" : ""}" data-src="${esc(id)}" title="${esc(shortHome(root))}">
          <span class="lbl">${esc(projName(root))}</span>
        </button>
        <button class="proj-x" data-act="remove-root" data-root="${esc(root)}" title="Убрать проект из списка (файлы на диске не трогаются)">✕</button>
      </div>
      ${missing ? `<div class="proj-err" title="${esc(missing.error)}">папка недоступна</div>` : ""}
      ${!missing && !repos.length ? `<div class="proj-err">нет git-репозиториев</div>` : ""}
      ${repos.map((r) => btn("repo:" + r.path, "⌥", r.name, r.worktrees.length, false)).join("")}`;
  }).join("");
  $("#sources").innerHTML = !state.roots.length ? "" :
    `<h3>Обзор</h3>` +
    sources.map((s) => btn(s.id, s.ic, s.label, count(s.f), s.alert)).join("") +
    projects;

  $("#fetch-info").textContent = state.lastFetch ? `fetch ${ago(state.lastFetch / 1000)}` : "";
}

// ---------- список ----------
function matches(repo, wt) {
  const f = state.filter;
  if (!f) return true;
  return [repo.name, wt.name, wt.branch, wt.path, wt.head, wt.subject]
    .some((s) => (s || "").toLowerCase().includes(f));
}

// Порядок: закреплённые → основной → остальные; внутри групп — выбранная сортировка.
function sorted(wts) {
  const dir = state.sort === "updated" ? -1 : 1;
  const flag = (x) => Number(!!x);
  return wts
    .map((wt, i) => ({ wt, i }))
    .sort((a, b) =>
      (flag(b.wt.pinned) - flag(a.wt.pinned)) ||
      (flag(b.wt.main) - flag(a.wt.main)) ||
      (state.sort === "git" ? a.i - b.i : dir * ((a.wt.updated || 0) - (b.wt.updated || 0))))
    .map(({ wt }) => wt);
}

// Ворктри в порядке отображения — для стрелок и Shift-диапазона.
function visible() {
  const f = sourceFilter();
  return state.repos.flatMap((repo) =>
    sorted((repo.worktrees || []).filter((wt) => f({ repo, wt }) && matches(repo, wt))).map((wt) => ({ repo, wt })));
}

// Длина полосы: корень, чтобы и 2, и 88 коммитов читались; потолок — половина шкалы.
const barW = (n, max) => Math.max(3, Math.min(max, Math.round(Math.sqrt(n) * max / 8)));

function rail(wt, max = 62) {
  if (wt.main) return `<div class="rail"><span class="na">основная ветка</span></div>`;
  if (!wt.exists) return `<div class="rail"><span class="na">папки нет</span></div>`;
  if (!wt.baseRef) return `<div class="rail"><span class="na">—</span></div>`;
  const b = wt.baseBehind || 0, a = wt.baseAhead || 0;
  const cf = wt.conflicts.length && !wt.rebasing ? " cf" : "";
  const base = baseName(wt.baseRef);
  const tip = `${base} впереди на ${b}, своих коммитов ${a}` + (cf ? `\nконфликты при rebase:\n${wt.conflicts.join("\n")}` : "");
  return `<div class="rail" title="${esc(tip)}">
    <span class="side-l">${b ? `<span class="num-l${cf}">${b}${cf ? " ⚠︎" : ""}</span><span class="bar bh${cf}" style="width:${barW(b, max)}px"></span>` : ""}</span>
    <span class="axis"></span>
    <span class="side-r">${a ? `<span class="bar ah" style="width:${barW(a, max)}px"></span><span class="num-r">${a}</span>` : ""}</span>
  </div>`;
}

function render() {
  renderSources();
  renderList();
  renderInspector();
}

function renderList() {
  const root = $("#content");
  const info = sourceInfo();
  const narrow = !!(info.repo || info.project); // открыт конкретный репо или проект
  $("#list-title").textContent = info.title;
  $("#legend").hidden = !state.repos.length;

  if (!state.roots.length) {
    $("#list-sub").textContent = "";
    root.innerHTML = `<div class="empty">Добавьте папку проекта — приложение найдёт в ней git-репозитории и их ворктри.<br><br>
      <button class="btn primary" data-act="add-root">Добавить проект</button></div>`;
    return;
  }
  if (!state.repos.length) {
    $("#list-sub").textContent = "";
    root.innerHTML = `<div class="empty">В папках проектов не найдено git-репозиториев.<br>
      Добавьте другую папку — кнопкой внизу слева.</div>`;
    return;
  }

  const f = sourceFilter();
  let total = 0;
  let lastProject = null;
  const html = state.repos.map((repo, ri) => {
    const all = repo.worktrees;
    const inSource = all.filter((wt) => f({ repo, wt }));
    const wts = sorted(inSource.filter((wt) => matches(repo, wt)));
    const inView = narrow && f({ repo, wt: {} });
    const rootMissing = repo.error && repo.path === repo.project; // недоступна сама папка проекта
    if (rootMissing ? state.filter || !(inView || state.source === "all") : !wts.length && (state.filter || !inView)) return "";
    total += wts.length;
    const hasPrunable = all.some((w) => w.prunable || !w.exists);
    // при нескольких проектах в общем списке — заголовок проекта перед его репо
    let projHead = "";
    if (multiProject() && !narrow && repo.project !== lastProject) {
      lastProject = repo.project;
      projHead = `<div class="proj-sep" title="${esc(shortHome(repo.project))}">${esc(projName(repo.project))}</div>`;
    }
    if (rootMissing) {
      return projHead + `<div class="repo-error">Папка проекта ${esc(shortHome(repo.path))} недоступна: ${esc(repo.error)}</div>`;
    }
    return projHead + `
      <div class="grp">
        <span class="grp-name">${esc(repo.name)}</span>
        <span class="spacer"></span>
        <span class="acts">
          ${hasPrunable ? `<button class="lnk" data-act="prune" data-repo="${ri}" title="git worktree prune">Prune</button>` : ""}
          <button class="lnk" data-act="fetch" data-repo="${ri}" title="git fetch --all --prune">Fetch</button>
          ${all.some(canPull) ? `<button class="lnk" data-act="pull-repo" data-repo="${ri}" title="git pull во всех ворктри репо, у которых есть upstream">Pull все</button>` : ""}
          <button class="lnk" data-act="add" data-repo="${ri}" title="Новый ворктри (⌘N)">＋ Ворктри</button>
        </span>
      </div>
      ${repo.error ? `<div class="repo-error">${esc(repo.error)}</div>` : ""}
      ${wts.map((wt) => renderRow(wt)).join("")}`;
  }).join("");

  $("#list-sub").textContent = `${total} ворктри`;
  root.innerHTML = html || (state.filter
    ? `<div class="empty">Ничего не найдено по «${esc(state.filter)}»</div>`
    : `<div class="empty">${state.source === "attention" ? "Всё в порядке — ни одному ворктри не нужно внимание." : "Здесь пока пусто."}</div>`);
  $(".row.on")?.scrollIntoView({ block: "nearest" });
}

function renderRow(wt) {
  const t = triage(wt);
  const ch = [];
  if (wt.exists && wt.dirty > 0) ch.push(`●${wt.dirty}`);
  if (wt.behind) ch.push(`↓${wt.behind}`);
  if (wt.ahead) ch.push(`↑${wt.ahead}`);
  if (wt.locked) ch.push(`lock`);
  const cls = ["row"];
  if (wt.path === state.focus) cls.push("on");
  if (state.selected.has(wt.path)) cls.push("multi");
  if (!wt.exists) cls.push("missing");
  const sub = wt.name
    ? `<span class="b mono">${esc(branchLabel(wt))}</span>`
    : `<span class="b">${esc(t.text)}</span>`;
  return `
    <div class="${cls.join(" ")}" data-p="${esc(wt.path)}" title="${esc(t.text)}">
      <span class="dot l${t.level}${wt.exists ? "" : " off"}"></span>
      <span class="t">${wt.pinned ? `<span class="pin">◆</span>` : ""}${esc(wtTitle(wt))}${wt.main ? `<span class="tag">main</span>` : ""}</span>
      ${rail(wt)}
      <span class="tm" title="${wt.updated ? esc(new Date(wt.updated * 1000).toLocaleString("ru")) : ""}">${esc(agoShort(wt.updated))}</span>
      ${sub}
      <span class="ch">${ch.join(" ")}</span>
    </div>`;
}

// ---------- инспектор ----------
function renderInspector() {
  const box = $("#insp");
  const sel = selectedItems();
  if (sel.length > 1) { box.innerHTML = renderMulti(sel); return; }

  const it = findWt(state.focus);
  if (!it) {
    box.innerHTML = state.repos.length
      ? `<div class="placeholder">Выберите ворктри в списке.<br><span class="hint">Двойной клик или ↵ — открыть в VS Code</span></div>`
      : "";
    return;
  }
  const { repo, wt } = it;
  const t = triage(wt);
  const base = baseName(wt.baseRef);
  const fix = t.act ? `<div class="btns"><button class="btn wide" data-act="${t.act}" data-p="${esc(wt.path)}">${esc(t.label)}</button>
    ${wt.rebasing ? `<button class="btn wide" data-act="rebase-abort" data-p="${esc(wt.path)}">Отменить rebase</button>` : ""}</div>` : "";
  const d = `data-p="${esc(wt.path)}"`;
  const c = wt.conflicts;

  let up = "";
  if (wt.gone) up = `<span class="gone">${esc(wt.upstream)} удалена на remote</span>`;
  else if (wt.upstream) {
    up = `${esc(wt.upstream)}: ` + (wt.behind || wt.ahead
      ? [wt.behind ? `<span class="bh">↓${wt.behind} не скачано</span>` : "", wt.ahead ? `<span class="ah">↑${wt.ahead} не запушено</span>` : ""].filter(Boolean).join(", ")
      : "совпадает");
  } else if (wt.branch) up = "нет upstream — ветка только локальная";

  box.innerHTML = `
    <h2>${esc(wtTitle(wt))}</h2>
    <div class="br mono">${esc(repoLabel(repo))} / ${esc(branchLabel(wt))}</div>
    <div class="state l${t.level}">${esc(t.text)}${t.level ? fix : ""}</div>

    <div class="btns">
      <button class="btn primary big wide" data-act="code" ${d} ${wt.exists ? "" : "disabled"}>Открыть в VS Code</button>
      <button class="btn" data-act="finder" ${d} ${wt.exists ? "" : "disabled"}>Finder</button>
      <button class="btn" data-act="term" ${d} ${wt.exists ? "" : "disabled"}>Terminal</button>
      <button class="btn${wt.behind ? " warn" : ""}" data-act="pull" ${d} ${canPull(wt) ? "" : "disabled"}
        title="${wt.upstream ? `git pull из ${esc(wt.upstream)} (fast-forward; если ветка разошлась — --rebase)` : "У ветки нет upstream"}">Pull${wt.behind ? " " + wt.behind : ""}</button>
      <button class="btn${c.length ? " danger" : ""}" data-act="rebase" ${d} ${canRebase(wt) ? "" : "disabled"}
        title="${!wt.baseRef ? "Основная ветка" : c.length
          ? `git rebase ${esc(wt.baseRef)} — будут конфликты: rebase остановится, ворктри откроется в VS Code`
          : `git rebase --autostash ${esc(wt.baseRef)}`}">Rebase${c.length ? " ⚠︎" : ""}</button>
    </div>

    ${wt.baseRef && wt.exists ? `
    <div class="sec">
      <h4>Относительно ${esc(base)}</h4>
      ${rail(wt, 110)}
      <div class="sync-line"><span>${wt.baseBehind ? `отстаёт на <b>${wt.baseBehind}</b>` : "в курсе"}</span>
        <span>своих <b>${wt.baseAhead || 0}</b></span></div>
    </div>` : ""}
    ${up ? `<div class="sync-up">${up}</div>` : ""}

    <dl>
      ${wt.subject ? `<dt>Последний коммит</dt><dd>${esc(wt.subject)}<br><span class="mono muted">${esc((wt.head || "").slice(0, 8))}</span></dd>` : ""}
      ${wt.updated ? `<dt>Изменён</dt><dd>${esc(ago(wt.updated))}</dd>` : ""}
      ${wt.exists ? `<dt>Изменения</dt><dd>${wt.dirty > 0 ? files(wt.dirty) + " не закоммичено" : wt.dirty < 0 ? "неизвестно" : "нет"}</dd>` : ""}
      ${c.length ? `<dt>${wt.rebasing ? "Конфликты" : "Конфликтуют"}</dt><dd><ul class="cf-list mono">${c.map((f) => `<li>${esc(f)}</li>`).join("")}</ul></dd>` : ""}
      <dt>Путь</dt><dd class="mono">${esc(relPath(wt.path))}</dd>
      ${wt.locked ? `<dt>Lock</dt><dd>ворктри заблокирован (git worktree lock)</dd>` : ""}
    </dl>

    <div class="btns mt">
      <button class="btn" data-act="pin" ${d}>${wt.pinned ? "Открепить" : "Закрепить"}</button>
      <button class="btn" data-act="rename" ${d}>Переименовать</button>
      <button class="btn wide" data-act="copy" ${d}>Скопировать путь</button>
      ${wt.main ? "" : `<button class="btn danger wide" data-act="remove" ${d} ${wt.pinned ? 'disabled title="Сначала открепите"' : ""}>Удалить ворктри…</button>`}
    </div>`;
}

function renderMulti(items) {
  const st = selectionState();
  return `
    <h2>Выбрано: ${items.length}</h2>
    <div class="br">⌘-клик — добавить или убрать, Shift-клик — диапазон, Esc — снять</div>
    <ul class="multi-list">${items.map(({ repo, wt }) => `
      <li>${wt.pinned ? "◆ " : ""}${esc(wtTitle(wt))} <span class="muted">${esc(repoLabel(repo))}</span></li>`).join("")}</ul>
    <div class="btns">
      <button class="btn wide" data-act="sel-pin" ${st.pin === null ? "disabled" : ""} title="${esc(st.pinWhy)}">${st.pin === false ? "Открепить" : "Закрепить"}</button>
      <button class="btn danger wide" data-act="sel-remove" ${st.remove ? "" : "disabled"} title="${esc(st.removeWhy)}">Удалить ворктри…</button>
      <button class="btn wide" data-act="sel-clear">Снять выделение</button>
    </div>
    ${st.removeWhy ? `<p class="hint">${esc(st.removeWhy)}</p>` : ""}`;
}

const selectedItems = () => allWorktrees().filter(({ wt }) => state.selected.has(wt.path));

// закреп — только если все выбранные в одном состоянии;
// удаление — только если среди выбранных нет закреплённых и основного ворктри.
function selectionState() {
  const items = selectedItems();
  const pinned = items.filter(({ wt }) => wt.pinned).length;
  const res = { items, pin: null, pinWhy: "", remove: items.length > 0, removeWhy: "" };
  if (pinned === items.length) res.pin = false;
  else if (pinned === 0) res.pin = true;
  else res.pinWhy = "Выбраны и закреплённые, и незакреплённые ворктри";
  if (pinned) {
    res.remove = false;
    res.removeWhy = "Среди выбранных есть закреплённые — сначала открепите";
  } else if (items.some(({ wt }) => wt.main)) {
    res.remove = false;
    res.removeWhy = "Основной ворктри удалить нельзя";
  }
  return res;
}

// ---------- выбор ----------
function setFocus(path) {
  state.focus = path;
  localStorage.setItem("focus", path);
}

function clickRow(path, ev) {
  if (ev.metaKey) {
    // при первом ⌘-клике в набор попадает и текущий ворктри
    if (!state.selected.size && state.focus && state.focus !== path) state.selected.add(state.focus);
    state.selected.has(path) ? state.selected.delete(path) : state.selected.add(path);
  } else if (ev.shiftKey && (state.anchor || state.focus)) {
    const list = visible().map(({ wt }) => wt.path);
    const a = list.indexOf(state.anchor || state.focus), b = list.indexOf(path);
    if (a >= 0 && b >= 0) {
      state.selected.clear();
      list.slice(Math.min(a, b), Math.max(a, b) + 1).forEach((p) => state.selected.add(p));
    }
  } else {
    state.selected.clear();
    state.anchor = path;
  }
  setFocus(path);
  renderList();
  renderInspector();
}

function moveFocus(delta) {
  const list = visible();
  if (!list.length) return;
  const i = list.findIndex(({ wt }) => wt.path === state.focus);
  const n = list[i < 0 ? 0 : Math.max(0, Math.min(list.length - 1, i + delta))];
  state.selected.clear();
  state.anchor = n.wt.path;
  setFocus(n.wt.path);
  renderList();
  renderInspector();
}

// ---------- действия ----------
async function busy(btn, label, fn) {
  if (!btn) return fn();
  const old = btn.textContent;
  btn.disabled = true;
  btn.textContent = label;
  try { return await fn(); } finally { btn.disabled = false; btn.textContent = old; }
}

document.addEventListener("click", async (ev) => {
  const src = ev.target.closest("[data-src]");
  if (src) {
    setSource(src.dataset.src);
    state.selected.clear();
    // выбранный ворктри из другого проекта/репо — переключиться на первый видимый
    if (!visible().some(({ wt }) => wt.path === state.focus)) setFocus(visible()[0]?.wt.path || "");
    render();
    return;
  }

  const el = ev.target.closest("[data-act]");
  if (!el) {
    const row = ev.target.closest(".row[data-p]");
    if (row) clickRow(row.dataset.p, ev);
    return;
  }
  const repo = el.dataset.repo != null ? state.repos[+el.dataset.repo] : findWt(el.dataset.p)?.repo;
  const wt = el.dataset.p ? findWt(el.dataset.p)?.wt : null;
  try {
    switch (el.dataset.act) {
      case "add-root": await addRoot(); break;
      case "remove-root": await removeRoot(el.dataset.root); break;
      case "code": await openCode(wt.path); break;
      case "finder": await api.OpenInFinder(wt.path); break;
      case "term": await api.OpenInTerminal(wt.path); break;
      case "copy": await api.CopyText(wt.path); toast("Путь скопирован"); break;
      case "add": await openAdd(repo); break;
      case "rename": openRename(wt); break;
      case "pin":
        await api.SetPinned(wt.path, !wt.pinned);
        await load();
        break;
      case "remove": openRemove([{ repo, wt }]); break;
      case "pull": {
        const out = await busy(el, "Pull…", () => api.Pull(wt.path));
        toast(`${wt.branch}: ${out || "обновлено"}`);
        await load();
        break;
      }
      case "pull-repo": await pullRepo(el, repo); break;
      case "rebase": {
        const keep = wt.conflicts.length > 0;
        const stopped = await busy(el, "Rebase…", () => api.Rebase(wt.path, wt.baseRef, keep));
        if (stopped) {
          toast("Rebase остановился на конфликтах — разрешите их в VS Code и нажмите «Продолжить rebase».", true);
          openCode(wt.path).catch(fail);
        } else {
          toast(`${wt.branch}: rebase на ${wt.baseRef} выполнен${wt.upstream ? " (для push понадобится --force-with-lease)" : ""}`);
        }
        await load();
        break;
      }
      case "rebase-continue":
        await busy(el, "…", () => api.RebaseContinue(wt.path));
        toast(`${wt.branch}: rebase продолжен`);
        await load();
        break;
      case "rebase-abort":
        await busy(el, "…", () => api.RebaseAbort(wt.path));
        toast(`${wt.branch}: rebase отменён`);
        await load();
        break;
      case "fetch":
        await busy(el, "Fetch…", () => api.Fetch(repo.path));
        toast(`${repo.name}: fetch выполнен`);
        await load();
        break;
      case "prune":
        await api.Prune(repo.path);
        toast(`${repo.name}: prune выполнен`);
        await load();
        break;
      case "sel-clear": state.selected.clear(); render(); break;
      case "sel-remove": {
        const st = selectionState();
        if (st.remove) openRemove(st.items);
        break;
      }
      case "sel-pin": {
        const st = selectionState();
        if (st.pin === null) break;
        for (const { wt: w } of st.items) await api.SetPinned(w.path, st.pin);
        await load();
        break;
      }
    }
  } catch (e) { fail(e); }
});

document.addEventListener("dblclick", (ev) => {
  if (ev.target.closest("button, input")) return;
  const row = ev.target.closest(".row[data-p]");
  if (!row) return;
  const wt = findWt(row.dataset.p)?.wt;
  if (wt?.exists) openCode(wt.path).catch(fail);
});

// OpenInVSCode возвращает пояснение, если пришлось открыть не так, как ожидалось.
async function openCode(path) {
  const note = await api.OpenInVSCode(path);
  if (note) toast(note, true);
}

async function addRoot() {
  const dir = await api.AddRoot();
  if (!dir) return;
  setSource("proj:" + dir);
  setFocus("");
  await load();
  const n = state.repos.filter((r) => r.project === dir && !r.error).length;
  toast(n ? `Проект ${projName(dir)} добавлен: ${n} ${plural(n, "репозиторий", "репозитория", "репозиториев")}`
    : `Проект ${projName(dir)} добавлен, но git-репозиториев в нём не найдено`, !n);
  // fetch только по новым репо — остальные обновятся по расписанию
  await Promise.allSettled(state.repos.filter((r) => r.project === dir && !r.error).map((r) => api.Fetch(r.path)));
  await load();
}

async function removeRoot(dir) {
  await api.RemoveRoot(dir);
  for (const { wt } of allWorktrees()) if (rootOf(wt.path) === dir) state.selected.delete(wt.path);
  toast(`Проект ${projName(dir)} убран из списка. Файлы на диске не тронуты.`);
  await load();
}

async function pullRepo(btn, repo) {
  const targets = repo.worktrees.filter(canPull);
  const errors = [];
  let ok = 0;
  await busy(btn, "Pull…", async () => {
    // последовательно: pull делает fetch, а параллельные fetch в одном репо дерутся за lock
    for (const w of targets) {
      try { await api.Pull(w.path); ok++; } catch (e) { errors.push(`${w.branch}: ${e?.message ?? e}`); }
    }
  });
  if (ok) toast(`${repo.name}: pull выполнен в ${ok} ворктри`);
  if (errors.length) toast(errors.join("\n"), true);
  await load();
}

// фоновый fetch — чтобы ↓N и «отстаёт от master» были актуальны без ручного Fetch
const AUTO_FETCH_MS = 10 * 60 * 1000;
async function autoFetch() {
  if (!liveRepos().length) return;
  await Promise.allSettled(liveRepos().map((r) => api.Fetch(r.path)));
  state.lastFetch = Date.now();
  if (!$("dialog[open]")) await load();
}

async function fetchAll() {
  const btn = $("#fetch-all-btn");
  await busy(btn, "…", async () => {
    const repos = liveRepos();
    const res = await Promise.allSettled(repos.map((r) => api.Fetch(r.path)));
    const errs = res.map((r, i) => r.status === "rejected" ? `${repoLabel(repos[i])}: ${r.reason}` : "").filter(Boolean);
    state.lastFetch = Date.now();
    errs.length ? toast(errs.join("\n"), true) : toast("Fetch выполнен во всех репо");
  });
  await load();
}

// репо для ⌘N: открытый в сайдбаре, иначе репо выбранного ворктри, иначе первый
function currentRepo() {
  const info = sourceInfo();
  if (info.repo) return info.repo;
  const focused = findWt(state.focus)?.repo;
  if (info.project) return focused?.project === info.project ? focused : liveRepos().find((r) => r.project === info.project);
  return focused || liveRepos()[0];
}

// ---------- диалог добавления ----------
const add = { repo: null, branches: [], pathTouched: false };

async function openAdd(repo) {
  if (!repo) return;
  add.repo = repo;
  add.pathTouched = false;
  add.branches = await api.ListBranches(repo.path);

  $("#add-repo-name").textContent = repoLabel(repo);
  $("#add-branch").value = "";
  $("#add-name").value = "";
  document.querySelector('input[name="mode"][value="new"]').checked = true;

  const mainBranch = repo.worktrees?.[0]?.branch;
  $("#add-base").innerHTML = add.branches.map((b) =>
    `<option value="${esc(b.name)}"${b.name === mainBranch ? " selected" : ""}>${esc(b.name)}</option>`).join("");

  const free = add.branches.filter((b) => !b.inUse);
  const used = add.branches.filter((b) => b.inUse);
  $("#add-existing").innerHTML =
    free.map((b) => `<option value="${esc(b.name)}">${esc(b.name)}</option>`).join("") +
    (used.length ? `<optgroup label="Уже открыты в ворктри">${
      used.map((b) => `<option disabled>${esc(b.name)}</option>`).join("")}</optgroup>` : "");

  syncMode();
  await suggestPath();
  $("#add-dialog").showModal();
  $("#add-name").focus();
}

const mode = () => document.querySelector('input[name="mode"]:checked').value;
const chosenBranch = () => (mode() === "new" ? $("#add-branch").value : $("#add-existing").value) || "";

function syncMode() {
  const m = mode();
  $$(".mode-new").forEach((e) => (e.hidden = m !== "new"));
  $$(".mode-existing").forEach((e) => (e.hidden = m !== "existing"));
}

let suggestSeq = 0;
async function suggestPath() {
  if (add.pathTouched || !add.repo) return;
  const seq = ++suggestSeq;
  const p = await api.SuggestPath(add.repo.path, chosenBranch(), $("#add-name").value);
  if (seq === suggestSeq && !add.pathTouched) $("#add-path").value = p;
}

$$('input[name="mode"]').forEach((r) =>
  r.addEventListener("change", () => { syncMode(); suggestPath(); }));
$("#add-branch").addEventListener("input", suggestPath);
$("#add-name").addEventListener("input", suggestPath);
$("#add-existing").addEventListener("change", suggestPath);
$("#add-path").addEventListener("input", () => (add.pathTouched = true));

$("#add-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const req = {
    repoPath: add.repo.path,
    mode: mode(),
    branch: chosenBranch().trim(),
    base: $("#add-base").value,
    path: $("#add-path").value.trim(),
    name: $("#add-name").value.trim(),
  };
  try {
    await busy($("#add-submit"), "Создаю…", () => api.AddWorktree(req));
    $("#add-dialog").close();
    toast(`Ворктри создан: ${relPath(req.path)}`);
    if ($("#add-open").checked) openCode(req.path).catch(fail);
    setFocus(req.path);
    await load();
  } catch (e) {
    fail(e);
  }
});

// ---------- диалог удаления (один или несколько) ----------
let rmItems = [];

const needsForce = (wt) => wt.exists && (wt.dirty > 0 || wt.locked);

function openRemove(items) {
  rmItems = items.filter(({ wt }) => !wt.main);
  if (!rmItems.length) return;

  $("#rm-title").textContent = rmItems.length === 1 ? "Удалить ворктри?" : `Удалить ворктри (${rmItems.length})?`;
  $("#rm-list").innerHTML = rmItems.map(({ repo, wt }) => {
    const b = [];
    if (wt.dirty > 0) b.push(`<span class="tag dirty">●${wt.dirty} изм.</span>`);
    if (wt.locked) b.push(`<span class="tag locked">locked</span>`);
    if (wt.ahead > 0) b.push(`<span class="tag ahead" title="не запушено">↑${wt.ahead}</span>`);
    return `<li>${wt.name ? `<b>${esc(wt.name)}</b>, ` : ""}<b class="mono">${esc(branchLabel(wt))}</b>
      <span class="muted">в ${esc(repoLabel(repo))}</span>${b.join("")}<br>
      <span class="muted mono">${esc(relPath(wt.path))}</span></li>`;
  }).join("");

  const forced = rmItems.filter(({ wt }) => needsForce(wt));
  const unpushed = rmItems.filter(({ wt }) => wt.ahead > 0 || (wt.branch && !wt.upstream));
  const warn = [];
  if (forced.length) warn.push(`У ${forced.length} ворктри есть незакоммиченные изменения или lock — нужно «Принудительно».`);
  if (unpushed.length) warn.push(`У ${unpushed.length} ветки есть незапушенные коммиты или нет upstream — не удаляйте ветки, если они нужны.`);
  $("#rm-dirty").hidden = !warn.length;
  $("#rm-dirty").textContent = warn.join(" ");

  $("#rm-force").checked = false;
  $("#rm-force-wrap").hidden = !rmItems.some(({ wt }) => wt.exists);
  $("#rm-branch-wrap").hidden = !rmItems.some(({ wt }) => wt.branch);
  $("#rm-delete-branch").checked = false;

  syncRemoveBtn();
  $("#rm-dialog").showModal();
}

function syncRemoveBtn() {
  $("#rm-submit").disabled = rmItems.some(({ wt }) => needsForce(wt)) && !$("#rm-force").checked;
}
$("#rm-force").addEventListener("change", syncRemoveBtn);

$("#rm-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  const force = $("#rm-force").checked;
  const deleteBranch = $("#rm-delete-branch").checked;
  const errors = [];
  let done = 0;

  await busy($("#rm-submit"), "Удаляю…", async () => {
    // последовательно: параллельные git worktree в одном репо дерутся за lock
    for (const { repo, wt } of rmItems) {
      try {
        await api.RemoveWorktree({ repoPath: repo.path, path: wt.path, branch: wt.branch || "", force, deleteBranch });
        state.selected.delete(wt.path);
        if (state.focus === wt.path) setFocus("");
        done++;
      } catch (e) {
        errors.push(`${relPath(wt.path)}: ${e?.message ?? e}`);
      }
    }
  });
  $("#rm-dialog").close();
  if (done) toast(done === 1 ? `Удалён: ${relPath(rmItems[0].wt.path)}` : `Удалено ворктри: ${done}`);
  if (errors.length) toast(errors.join("\n"), true);
  await load();
});

// ---------- название ворктри ----------
let renameWt = null;

function openRename(wt) {
  renameWt = wt;
  $("#name-path").textContent = relPath(wt.path);
  $("#name-input").value = wt.name || "";
  $("#name-dialog").showModal();
  $("#name-input").select();
}

$("#name-form").addEventListener("submit", async (ev) => {
  ev.preventDefault();
  try {
    await api.SetName(renameWt.path, $("#name-input").value);
    $("#name-dialog").close();
    await load();
  } catch (e) { fail(e); }
});

// ---------- общее ----------
$$("[data-close]").forEach((b) => b.addEventListener("click", () => b.closest("dialog").close()));

$("#refresh-btn").addEventListener("click", load);
$("#fetch-all-btn").addEventListener("click", () => fetchAll().catch(fail));
$("#search").addEventListener("input", (e) => {
  state.filter = e.target.value.trim().toLowerCase();
  renderList();
});
$("#sort").value = state.sort;
$("#sort").addEventListener("change", (e) => {
  state.sort = e.target.value;
  localStorage.setItem("sort", state.sort);
  renderList();
});

document.addEventListener("keydown", (e) => {
  if ($("dialog[open]")) return;
  const typing = e.target.matches("input, select, textarea");
  const k = e.key.toLowerCase();
  if (e.metaKey && k === "r") { e.preventDefault(); load(); return; }
  if (e.metaKey && k === "f") { e.preventDefault(); $("#search").focus(); return; }
  if (e.metaKey && k === "n") { e.preventDefault(); openAdd(currentRepo()).catch(fail); return; }
  if (typing) {
    if (e.key === "Escape" || e.key === "ArrowDown") { e.target.blur(); if (e.key === "ArrowDown") moveFocus(0); }
    return;
  }
  if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); moveFocus(e.key === "ArrowDown" ? 1 : -1); return; }
  if (e.key === "Enter") {
    const wt = findWt(state.focus)?.wt;
    if (wt?.exists) openCode(wt.path).catch(fail);
    return;
  }
  if (e.key === "Escape" && state.selected.size) { state.selected.clear(); render(); return; }
  if ((e.key === "Backspace" || e.key === "Delete") && e.metaKey) {
    e.preventDefault();
    if (state.selected.size) {
      const st = selectionState();
      st.remove ? openRemove(st.items) : st.removeWhy && toast(st.removeWhy, true);
    } else {
      const it = findWt(state.focus);
      if (it && !it.wt.main && !it.wt.pinned) openRemove([it]);
    }
  }
});

// обновлять при возврате в окно — ворктри могли поменять из терминала
window.addEventListener("focus", () => { if (!$("dialog[open]")) load(); });
// «fetch N минут назад» в сайдбаре
setInterval(() => { if (state.lastFetch) renderSources(); }, 60 * 1000);

load().then(autoFetch);
setInterval(autoFetch, AUTO_FETCH_MS);
