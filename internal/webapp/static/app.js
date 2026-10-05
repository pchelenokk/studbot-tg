const tg = window.Telegram?.WebApp || null;
if (tg) {
  tg.ready();
  tg.expand();
  tg.enableClosingConfirmation?.();
}

let state = null;
let me = null;
let tab = "books";

const $ = (sel) => document.querySelector(sel);
const view = $("#view");

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => (
    { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]
  ));
}

function headers() {
  return { "Content-Type": "application/json", "X-Telegram-Init-Data": tg?.initData || "" };
}

async function errText(res) {
  try {
    const body = await res.json();
    if (body && body.error) return body.error;
  } catch (_) { /* not JSON */ }
  if (res.status === 401) return "Не авторизован — открой приложение из меню бота";
  return "Ошибка " + res.status;
}

async function api(action, params = {}) {
  const res = await fetch("/api/action", {
    method: "POST",
    headers: headers(),
    body: JSON.stringify({ action, params })
  });
  if (!res.ok) throw new Error(await errText(res));
  return res.json();
}

async function uploadBook(form) {
  const fd = new FormData(form);
  const res = await fetch("/api/upload", { method: "POST", headers: { "X-Telegram-Init-Data": tg?.initData || "" }, body: fd });
  if (!res.ok) throw new Error(await errText(res));
  return res.json();
}

const isRoot = () => me && (me.role === "root" || me.role === "admin");
const canManage = () => me && ["root", "admin", "proforg"].includes(me.role);

function toast(text) {
  const el = $("#toast");
  el.textContent = text;
  el.hidden = false;
  clearTimeout(el._t);
  el._t = setTimeout(() => (el.hidden = true), 2600);
}

// ask prefers the Telegram confirmation dialog and falls back to the browser.
function ask(text) {
  if (tg?.showConfirm) {
    return new Promise((resolve) => tg.showConfirm(text, (ok) => resolve(!!ok)));
  }
  return Promise.resolve(window.confirm(text));
}

async function refresh() {
  try {
    const res = await fetch("/api/state", { headers: { "X-Telegram-Init-Data": tg?.initData || "" } });
    if (!res.ok) throw new Error(await errText(res));
    state = await res.json();
    me = state.user;
    render();
  } catch (e) {
    view.innerHTML = `<div class="empty">Не удалось загрузить данные.<br>${esc(e.message)}<br><br>Открой приложение из меню бота.</div>`;
  }
}

function roleLabel(role) {
  return { root: "👑 Староста", admin: "⚙️ Админ", proforg: "📋 Профорг", student: "👤 Студент" }[role] || "👤 Студент";
}
function roleChip(role) {
  const cls = ["root", "admin"].includes(role) ? "root" : role === "proforg" ? "proforg" : "student";
  return `<span class="chip ${cls}">${esc(roleLabel(role))}</span>`;
}

const TABS = [
  { id: "books", label: "📚 Книги" },
  { id: "homework", label: "📝 ДЗ" },
  { id: "checkin", label: "🗳 Перекличка" },
  { id: "polls", label: "📊 Опросы" },
  { id: "events", label: "📅 События" },
  { id: "students", label: "👥 Люди" }
];

function renderTabs() {
  const tabs = TABS.filter((t) => t.id !== "students" || canManage());
  $("#tabs").innerHTML = tabs
    .map((t) => `<button class="tab ${t.id === tab ? "active" : ""}" data-tab="${t.id}">${t.label}</button>`)
    .join("");
  $("#tabs").querySelectorAll(".tab").forEach((el) => {
    el.onclick = () => { tab = el.dataset.tab; render(); };
  });
}

function render() {
  $("#who").textContent = me ? nameOf(me) : "—";
  $("#role").textContent = me ? roleLabel(me.role) : "";
  renderTabs();

  switch (tab) {
    case "books": renderBooks(); break;
    case "homework": renderHomework(); break;
    case "checkin": renderCheckin(); break;
    case "polls": renderPolls(); break;
    case "events": renderEvents(); break;
    case "students": renderStudents(); break;
    default: renderBooks();
  }
}

function renderBooks() {
  const books = state.books || [];
  if (!books.length) {
    view.innerHTML = `<div class="card empty">📚<br>Библиотека пуста${canManage() ? "<br><br>Нажми «Добавить книгу», чтобы загрузить первую." : ""}</div>` +
      (canManage() ? `<button class="btn" data-act="book-new">➕ Добавить книгу</button>` : "");
    return;
  }

  const groups = {};
  const order = [];
  books.forEach((b) => {
    if (!groups[b.subject]) { groups[b.subject] = []; order.push(b.subject); }
    groups[b.subject].push(b);
  });

  let html = "";
  order.forEach((subject) => {
    html += `<div class="subject-head">${esc(subject)}</div>`;
    groups[subject].forEach((b) => {
      const hasFile = !!b.attachment_id;
      html += `<div class="list-item">
        <div class="body">
          <div class="title">${hasFile ? "📎" : "📄"} ${esc(b.title)}</div>
          ${b.author ? `<div class="sub">${esc(b.author)}</div>` : ""}
        </div>
        <button class="btn sm" data-act="book-open" data-id="${b.id}">Открыть</button>
      </div>`;
    });
  });

  html += canManage() ? `<button class="btn" data-act="book-new">➕ Добавить книгу</button>` : "";
  view.innerHTML = html;
}

async function bookCard(id) {
  const b = (state.books || []).find((x) => x.id === id);
  if (!b) return;
  let html = `<div class="card">
    <div class="title" style="font-size:17px">${esc(b.title)}</div>
    <div class="sub">Предмет: ${esc(b.subject)}</div>
    ${b.author ? `<div class="sub">Автор: ${esc(b.author)}</div>` : ""}
    ${b.description ? `<div class="sub" style="margin-top:8px">${esc(b.description)}</div>` : ""}
    <div class="sub" style="margin-top:8px">${b.attachment_id ? "📎 Файл прикреплён" : "📄 Без файла"}</div>
  </div>`;
  if (b.attachment_id) html += `<button class="btn" data-act="book-file" data-id="${b.id}">📂 Скачать файл</button>`;
  if (isRoot()) html += `<button class="btn danger" data-act="book-del" data-id="${b.id}">🗑 Удалить книгу</button>`;
  html += `<button class="btn grey" data-act="sheet-close">Закрыть</button>`;
  openSheet(b.title, html);
}

async function openFile(id) {
  try {
    const { url } = await api("file_url", { book_id: id });
    window.open(url, "_blank");
  } catch (e) {
    toast("❌ " + e.message);
  }
}

async function delBook(id) {
  const b = (state.books || []).find((x) => x.id === id);
  if (!(await ask(`Удалить книгу «${b ? b.title : "#" + id}»?`))) return;
  try { await api("delete_book", { id }); toast("✅ Книга удалена"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

function bookForm() {
  openSheet("Новая книга", `
    <div class="field"><label>Название *</label><input id="f-title" placeholder="Математика 1 курс"></div>
    <div class="field"><label>Предмет *</label><input id="f-subject" placeholder="Высшая математика"></div>
    <div class="field"><label>Автор</label><input id="f-author" placeholder="Иванов И.И."></div>
    <div class="field"><label>Описание</label><textarea id="f-desc" placeholder="Необязательно"></textarea></div>
    <div class="field"><label>Файл книги (необязательно)</label><input type="file" id="f-file"></div>
    <button class="btn" data-act="save-book">💾 Сохранить книгу</button>
    <button class="btn grey" data-act="sheet-close">Отмена</button>
  `);
}

async function saveBook() {
  const title = $("#f-title").value.trim();
  const subject = $("#f-subject").value.trim();
  if (!title || !subject) { toast("❌ Заполни название и предмет"); return; }
  const desc = $("#f-desc").value.trim();
  const file = $("#f-file").files[0];

  try {
    if (file) {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("title", title);
      fd.append("subject", subject);
      fd.append("author", $("#f-author").value.trim());
      fd.append("description", desc);
      const res = await fetch("/api/upload", { method: "POST", headers: { "X-Telegram-Init-Data": tg?.initData || "" }, body: fd });
      if (!res.ok) throw new Error(await errText(res));
    } else {
      await api("add_book", { title, subject, author: $("#f-author").value.trim(), description: desc });
    }
    closeSheet();
    toast("✅ Книга добавлена");
    await refresh();
  } catch (e) {
    toast("❌ " + e.message);
  }
}

function renderHomework() {
  const items = state.homework || [];
  let html = "";
  if (!items.length) html = `<div class="card empty">📝<br>Домашних заданий пока нет</div>`;
  items.forEach((h) => {
    html += `<div class="list-item">
      <div class="body">
        <div class="title">${esc(h.title)}</div>
        <div class="sub">${esc(h.subject)}${h.due_date ? " • ⏰ " + esc(h.due_date) : ""}</div>
        ${h.description ? `<div class="sub">${esc(h.description)}</div>` : ""}
      </div>
      ${isRoot() ? `<button class="btn sm danger" data-act="hw-del" data-id="${h.id}">🗑</button>` : ""}
    </div>`;
  });
  if (canManage()) html += `<button class="btn" data-act="hw-new">➕ Добавить ДЗ</button>`;
  view.innerHTML = html;
}

function hwForm() {
  openSheet("Новое ДЗ", `
    <div class="field"><label>Предмет *</label><input id="f-subject" placeholder="Физика"></div>
    <div class="field"><label>Задание *</label><input id="f-title" placeholder="Задачи 1–10"></div>
    <div class="field"><label>Подробности</label><textarea id="f-desc"></textarea></div>
    <div class="field"><label>Дедлайн</label><input type="date" id="f-due"></div>
    <button class="btn" data-act="save-hw">💾 Сохранить ДЗ</button>
    <button class="btn grey" data-act="sheet-close">Отмена</button>
  `);
}

async function saveHw() {
  const subject = $("#f-subject").value.trim();
  const title = $("#f-title").value.trim();
  if (!subject || !title) { toast("❌ Заполни предмет и задание"); return; }
  try {
    await api("add_homework", { subject, title, description: $("#f-desc").value.trim(), due_date: $("#f-due").value });
    closeSheet(); toast("✅ ДЗ добавлено"); await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

async function delHw(id) {
  if (!(await ask("Удалить ДЗ?"))) return;
  try { await api("delete_homework", { id }); toast("✅ Удалено"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

function statusText(s) {
  return { present: "✅ на паре", absent: "❌ меня нет", late: "⏳ опоздаю, но на паре буду" }[s] || "не отмечен";
}

function renderCheckin() {
  const list = state.checkins || [];
  const active = list.filter((c) => c.is_active || c.status === "active");
  const closed = list.filter((c) => !(c.is_active || c.status === "active"));

  let html = "";

  if (canManage()) {
    html += `<button class="btn" data-act="checkin-new">🗳 Создать перекличку</button>`;
  }

  if (!active.length) {
    html += `<div class="card empty">🗳<br>Активных перекличек нет${canManage() ? "<br><br>Создай перекличку — студенты отмечаются кнопками." : "<br><br>Дождись, пока староста или профорг её создаст."}</div>`;
  } else {
    active.forEach((c) => {
      html += `<div class="card">
        <div class="row-between">
          <div>
            <div class="title">📘 ${esc(c.subject)}</div>
            <div class="sub">📅 ${esc(c.date)}</div>
          </div>
          <span class="chip root">открыта</span>
        </div>
        <div style="margin-top:10px">
          <button class="btn green" data-act="checkin-vote" data-id="${c.id}" data-status="present">✅ На паре</button>
          <button class="btn danger" data-act="checkin-vote" data-id="${c.id}" data-status="absent">❌ Меня нет</button>
          <button class="btn" data-act="checkin-vote" data-id="${c.id}" data-status="late">⏳ Опоздаю</button>
        </div>
        ${canManage() ? `<button class="btn grey" data-act="checkin-close" data-id="${c.id}">🔒 Закрыть перекличку</button>` : ""}
      </div>`;
    });
  }

  if (closed.length) {
    html += `<div class="card"><div class="title">Закрытые переклички</div>`;
    closed.slice(0, 10).forEach((c) => {
      html += `<div class="row-between" style="padding:8px 0;border-bottom:1px solid var(--hint)">
        <div class="body"><div class="title">${esc(c.subject)}</div><div class="sub">${esc(c.date)}</div></div>
        <span class="chip student">закрыта</span>
      </div>`;
    });
    html += `</div>`;
  }

  view.innerHTML = html;
}

function checkinForm() {
  openSheet("Новая перекличка", `
    <div class="field"><label>Дата</label><input type="date" id="c-date" value="${esc(state.date || "")}"></div>
    <div class="field"><label>Предмет</label><input id="c-subject" list="c-subj" placeholder="Перекличка">
      <datalist id="c-subj">${(state.subjects || []).map((s) => `<option value="${esc(s)}">`).join("")}</datalist></div>
    <div class="field"><label>Сколько минут открыта</label><input type="number" id="c-min" value="15" min="1" max="240"></div>
    <button class="btn" data-act="save-checkin">🗳 Создать</button>
    <button class="btn grey" data-act="sheet-close">Отмена</button>
  `);
}

async function createCheckin() {
  try {
    await api("checkin_create", {
      date: $("#c-date").value,
      subject: $("#c-subject").value.trim(),
      minutes: Number($("#c-min").value)
    });
    closeSheet();
    toast("✅ Перекличка создана");
    await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

async function checkinVote(id, status) {
  try {
    await api("checkin_vote", { checkin_id: id, status });
    toast("✅ " + statusText(status));
    await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

async function closeCheckin(id) {
  if (!(await ask("Закрыть перекличку? Новые голоса не принимаются."))) return;
  try {
    await api("checkin_close", { checkin_id: id });
    toast("🔒 Перекличка закрыта");
    await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

function renderPolls() {
  const polls = state.polls || [];
  if (!polls.length) {
    view.innerHTML = `<div class="card empty">📊<br>Активных опросов нет${canManage() ? "<br><br>Создай первый опрос." : ""}</div>` +
      (canManage() ? `<button class="btn" data-act="poll-new">➕ Создать опрос</button>` : "");
    return;
  }
  let html = "";
  polls.forEach((p) => {
    html += `<div class="card">
      <div class="title">${esc(p.question)}</div>
      <div class="sub" style="margin:8px 0">Варианты — выбери и нажми «Голосовать»:</div>
      ${p.options.map((o, i) => `<label class="opt">
        <input type="radio" name="poll_${p.id}" value="${i}">
        <span>${esc(o)}</span>
      </label>`).join("")}
      <div class="btn-row">
        <button class="btn" data-act="poll-vote" data-id="${p.id}">🗳 Голосовать</button>
      </div>
      ${canManage() ? `<div class="btn-row">
        <button class="btn grey sm" data-act="poll-close" data-id="${p.id}">🔒 Закрыть</button>
        ${isRoot() ? `<button class="btn danger sm" data-act="poll-del" data-id="${p.id}">🗑 Удалить</button>` : ""}
      </div>` : ""}
    </div>`;
  });
  if (canManage()) html += `<button class="btn" data-act="poll-new">➕ Создать опрос</button>`;
  view.innerHTML = html;
}

function pollForm() {
  openSheet("Новый опрос", `
    <div class="field"><label>Вопрос *</label><input id="f-q" placeholder="Когда сдаём контрольную?"></div>
    <div class="field"><label>Варианты (каждый с новой строки) *</label><textarea id="f-o" placeholder="Понедельник&#10;Среда&#10;Пятница"></textarea></div>
    <button class="btn" data-act="save-poll">🗳 Создать опрос</button>
    <button class="btn grey" data-act="sheet-close">Отмена</button>
  `);
}

async function savePoll() {
  const q = $("#f-q").value.trim();
  const opts = $("#f-o").value.split("\n").map((s) => s.trim()).filter(Boolean);
  if (!q) { toast("❌ Введи вопрос"); return; }
  if (opts.length < 2) { toast("❌ Нужно минимум 2 варианта"); return; }
  try { await api("create_poll", { question: q, options: opts }); closeSheet(); toast("✅ Опрос создан"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

async function votePoll(id) {
  const sel = document.querySelector(`input[name="poll_${id}"]:checked`);
  if (!sel) { toast("Выбери вариант"); return; }
  try { await api("vote_poll", { poll_id: id, option_index: Number(sel.value) }); toast("✅ Голос учтён"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

async function closePoll(id) {
  if (!(await ask("Закрыть опрос?"))) return;
  try { await api("close_poll", { id }); toast("🔒 Опрос закрыт"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

async function delPoll(id) {
  if (!(await ask("Удалить опрос?"))) return;
  try { await api("delete_poll", { id }); toast("✅ Удалено"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

function renderEvents() {
  const events = state.events || [];
  let html = "";
  if (!events.length) html = `<div class="card empty">📅<br>Событий пока нет</div>`;
  events.forEach((e) => {
    html += `<div class="list-item">
      <div class="body">
        <div class="title">${esc(e.title)}</div>
        ${e.event_date ? `<div class="sub">🗓 ${esc(e.event_date)}</div>` : ""}
        ${e.description ? `<div class="sub">${esc(e.description)}</div>` : ""}
      </div>
      ${isRoot() ? `<button class="btn sm danger" data-act="event-del" data-id="${e.id}">🗑</button>` : ""}
    </div>`;
  });
  if (canManage()) html += `<button class="btn" data-act="event-new">➕ Добавить событие</button>`;
  view.innerHTML = html;
}

function eventForm() {
  openSheet("Новое событие", `
    <div class="field"><label>Название *</label><input id="f-title" placeholder="Защита проектов"></div>
    <div class="field"><label>Описание</label><textarea id="f-desc"></textarea></div>
    <div class="field"><label>Дата</label><input type="date" id="f-date"></div>
    <button class="btn" data-act="save-event">💾 Сохранить событие</button>
    <button class="btn grey" data-act="sheet-close">Отмена</button>
  `);
}

async function saveEvent() {
  const title = $("#f-title").value.trim();
  if (!title) { toast("❌ Введи название"); return; }
  try {
    await api("add_event", { title, description: $("#f-desc").value.trim(), event_date: $("#f-date").value });
    closeSheet(); toast("✅ Событие добавлено"); await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

async function delEvent(id) {
  if (!(await ask("Удалить событие?"))) return;
  try { await api("delete_event", { id }); toast("✅ Удалено"); await refresh(); } catch (e) { toast("❌ " + e.message); }
}

function nameOf(u) {
  const n = (u.full_name || "").trim();
  if (n) return n;
  if (u.username) return "@" + u.username;
  return "ID " + (u.tg_id ?? "?");
}

function renderStudents() {
  const users = state.users || [];
  if (!users.length) {
    view.innerHTML = `<div class="card empty">👥<br>Пока никого нет.<br>Пусть студенты напишут боту.</div>`;
    return;
  }

  let html = `<div class="card">
    <div class="title">Как назначить роль</div>
    <div class="sub">Нажми на человека — откроется карточка, где роль можно сменить кнопкой.</div>
  </div>`;

  users.forEach((u) => {
    html += `<div class="list-item" data-act="student" data-id="${u.id}" style="cursor:pointer">
      <div class="body">
        <div class="title">${esc(nameOf(u))}</div>
        <div class="sub">ID: ${u.tg_id ?? "—"}</div>
      </div>
      ${roleChip(u.role)}
    </div>`;
  });

  view.innerHTML = html;
}

function studentCard(id) {
  const u = (state.users || []).find((x) => x.id === id);
  if (!u) return;
  let html = `<div class="card">
    <div class="title" style="font-size:17px">${esc(u.full_name)}</div>
    <div class="sub">Telegram ID: ${u.tg_id}</div>
    <div style="margin-top:10px">${roleChip(u.role)}</div>
  </div>`;

  if (isRoot() && !["root", "admin"].includes(u.role)) {
    html += `<div class="field"><label>Сменить роль</label></div>`;
    html += `<button class="btn green" data-act="role" data-id="${u.id}" data-role="student">👤 Сделать студентом</button>`;
    html += `<button class="btn" data-act="role" data-id="${u.id}" data-role="proforg">📋 Сделать профоргом</button>`;
    html += `<button class="btn" data-act="role" data-id="${u.id}" data-role="root">👑 Сделать старостой</button>`;
  } else if (["root", "admin"].includes(u.role)) {
    html += `<div class="card empty">У этого человека максимальные права</div>`;
  } else {
    html += `<div class="card empty">Менять роль может староста</div>`;
  }
  html += `<button class="btn grey" data-act="sheet-close">Закрыть</button>`;
  openSheet(nameOf(u), html);
}

async function setRole(id, role) {
  const u = (state.users || []).find((x) => x.id === id);
  if (!u) { toast("❌ Пользователь не найден, обнови список"); return; }
  try {
    await api("set_role", { tg_id: u.tg_id, role });
    closeSheet(); toast("✅ Роль обновлена"); await refresh();
  } catch (e) { toast("❌ " + e.message); }
}

function openSheet(title, html) {
  $("#sheet-title").textContent = title;
  $("#sheet-content").innerHTML = html;
  $("#sheet").hidden = false;
}

function closeSheet() {
  $("#sheet").hidden = true;
}

$("#sheet-close").onclick = closeSheet;
$(".sheet-backdrop").onclick = closeSheet;
$("#refresh").onclick = () => { refresh(); tg?.HapticFeedback?.selectionChanged?.(); };
tg?.onEvent?.("themeChanged", render);

// Every button carries data-act instead of an inline onclick, so no data from
// the database is ever interpolated into JavaScript source.
document.addEventListener("click", (e) => {
  const el = e.target.closest("[data-act]");
  if (!el) return;
  const id = Number(el.dataset.id || 0);
  switch (el.dataset.act) {
    case "book-new": bookForm(); break;
    case "book-open": bookCard(id); break;
    case "book-file": openFile(id); break;
    case "book-del": delBook(id); break;
    case "sheet-close": closeSheet(); break;
    case "save-book": saveBook(); break;
    case "hw-new": hwForm(); break;
    case "hw-del": delHw(id); break;
    case "save-hw": saveHw(); break;
    case "checkin-new": checkinForm(); break;
    case "save-checkin": createCheckin(); break;
    case "checkin-vote": checkinVote(id, el.dataset.status); break;
    case "checkin-close": closeCheckin(id); break;
    case "poll-new": pollForm(); break;
    case "poll-vote": votePoll(id); break;
    case "poll-close": closePoll(id); break;
    case "poll-del": delPoll(id); break;
    case "save-poll": savePoll(); break;
    case "event-new": eventForm(); break;
    case "event-del": delEvent(id); break;
    case "save-event": saveEvent(); break;
    case "student": studentCard(id); break;
    case "role": setRole(id, el.dataset.role); break;
  }
});

refresh();
