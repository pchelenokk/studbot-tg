let view = "dashboard";
let status = null;
let subjects = [];

const $ = (s) => document.querySelector(s);
const content = $("#content");

const TITLES = {
  dashboard: ["Обзор", "состояние сервера и бота"],
  users: ["Пользователи", "роли, посещаемость, назначение прав"],
  books: ["Библиотека", "добавляй книги и файлы (PDF, DOCX и т.д.)"],
  homework: ["Домашка", "задания по предметам"],
  events: ["События", "календарь группы"],
  broadcast: ["Рассылка", "сообщение всем или только студентам"],
  logs: ["Логи", "журнал действий панели"]
};

function esc(s) {
  return String(s ?? "").replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));
}

function toast(text, isErr) {
  const el = $("#toast");
  el.textContent = text;
  el.className = "toast" + (isErr ? " err" : "");
  el.hidden = false;
  clearTimeout(el._t);
  el._t = setTimeout(() => (el.hidden = true), 3200);
}

async function get(path) {
  const res = await fetch(path, { headers: { "X-Requested-With": "panel" } });
  if (res.status === 401) { location.href = "/login"; throw new Error("unauthorized"); }
  if (!res.ok) throw new Error("HTTP " + res.status);
  return res.json();
}

async function post(path, data) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", "X-Requested-With": "panel" },
    body: new URLSearchParams(data)
  });
  if (res.status === 401) { location.href = "/login"; throw new Error("unauthorized"); }
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(json.error || "HTTP " + res.status);
  return json;
}

async function upload(path, fd) {
  const res = await fetch(path, {
    method: "POST",
    headers: { "X-Requested-With": "panel" },
    body: fd
  });
  if (res.status === 401) { location.href = "/login"; throw new Error("unauthorized"); }
  const json = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(json.error || "HTTP " + res.status);
  return json;
}

function kb(bytes) {
  if (bytes < 1024) return bytes + " Б";
  if (bytes < 1048576) return (bytes / 1024).toFixed(1) + " КБ";
  return (bytes / 1048576).toFixed(2) + " МБ";
}

function roleBadge(role) {
  const label = { root: "Староста", admin: "Админ", proforg: "Профорг", student: "Студент" }[role] || "Студент";
  return `<span class="badge ${esc(role)}">${label}</span>`;
}

function subjectOptions(sel) {
  const list = [...new Set(subjects)].filter(Boolean);
  return list.map((s) => `<option value="${esc(s)}" ${s === sel ? "selected" : ""}>${esc(s)}</option>`).join("");
}

function render() {
  const [title, sub] = TITLES[view];
  $("#view-title").textContent = title;
  $("#view-sub").textContent = sub;
  document.querySelectorAll(".nav-item").forEach((b) => b.classList.toggle("active", b.dataset.view === view));
}

async function loadAll() {
  try {
    status = await get("/api/panel/status");
    $("#status-dot").classList.add("on");
    $("#status-text").textContent = status.bot_online === false
      ? "бот не в сети · панель работает"
      : "бот на связи · @" + (status.bot_user || "—");
    $("#uptime").textContent = "⏱ " + status.uptime;
  } catch (e) {
    status = null;
    $("#status-dot").classList.remove("on");
    $("#status-text").textContent = "нет связи с сервером";
  }
  try { subjects = await get("/api/panel/subjects"); } catch (e) { /* datalists stay empty */ }
  render();

  const views = {
    dashboard: viewDashboard,
    users: viewUsers,
    books: viewBooks,
    homework: viewHomework,
    events: viewEvents,
    broadcast: viewBroadcast,
    logs: viewLogs
  };
  try {
    if (views[view]) await views[view]();
  } catch (e) {
    if (e.message !== "unauthorized") {
      content.innerHTML = `<div class="card empty">Не удалось загрузить раздел: ${esc(e.message)}</div>`;
    }
  }
}

async function viewDashboard() {
  const s = status;
  if (!s) { content.innerHTML = `<div class="card empty">Нет связи с ботом</div>`; return; }

  content.innerHTML = `
    <div class="grid">
      <div class="stat"><div class="label">Аптайм</div><div class="value">${esc(s.uptime)}</div><div class="hint">Go ${esc(s.go_version)}</div></div>
      <div class="stat g"><div class="label">Пользователей</div><div class="value">${s.users}</div><div class="hint">студентов: ${s.students}</div></div>
      <div class="stat"><div class="label">Книг</div><div class="value">${s.books}</div><div class="hint">в библиотеке</div></div>
      <div class="stat y"><div class="label">Домашки</div><div class="value">${s.homework}</div><div class="hint">заданий</div></div>
      <div class="stat"><div class="label">Событий</div><div class="value">${s.events}</div><div class="hint">в календаре</div></div>
      <div class="stat"><div class="label">Опросов</div><div class="value">${s.polls}</div><div class="hint">всего</div></div>
      <div class="stat"><div class="label">Память</div><div class="value">${s.per_cpu.toFixed(1)} МБ</div><div class="hint">goroutines: ${s.goroutines}</div></div>
      <div class="stat"><div class="label">База</div><div class="value">${kb(s.db_size)}</div><div class="hint">SQLite на диске</div></div>
    </div>

    <div class="card">
      <h3>Конфигурация</h3>
      <div class="kv">
        <div class="k">Бот</div><div class="v">@${esc(s.bot_user)} ${s.bot_name ? "(" + esc(s.bot_name) + ")" : ""}</div>
        <div class="k">Платформа</div><div class="v">${esc(s.os)} / ${esc(s.arch)}</div>
        <div class="k">Прокси Telegram</div><div class="v">${s.proxy ? "<code>" + esc(s.proxy) + "</code>" : "не используется"}</div>
        <div class="k">URL мини-приложения</div><div class="v"><code>${esc(s.webapp_url)}</code></div>
        <div class="k">Старосты (.env)</div><div class="v">${(s.root_ids || []).map((id) => "<code>" + id + "</code>").join(" ") || "не заданы"}</div>
      </div>
    </div>

    <div class="card">
      <h3>Быстрые действия</h3>
      <div style="display:flex;gap:10px;flex-wrap:wrap">
        <button class="btn" data-act="go" data-view="books">📚 Добавить книгу</button>
        <button class="btn ghost" data-act="go" data-view="homework">📝 Добавить ДЗ</button>
        <button class="btn ghost" data-act="go" data-view="users">👥 Назначить роли</button>
        <button class="btn ghost" data-act="go" data-view="broadcast">📣 Рассылка</button>
        <button class="btn warn" data-act="clear-attendance">🆕 Очистить посещаемость</button>
      </div>
    </div>`;
}

function go(v) { view = v; loadAll(); }

async function viewUsers() {
  const users = await get("/api/panel/users");
  const roots = users.filter((u) => u.role === "root" || u.role === "admin");
  const profs = users.filter((u) => u.role === "proforg");
  const studs = users.filter((u) => u.role === "student");

  let html = `
    <div class="grid">
      <div class="stat y"><div class="label">Старосты</div><div class="value">${roots.length}</div><div class="hint">полный доступ</div></div>
      <div class="stat"><div class="label">Профорги</div><div class="value">${profs.length}</div><div class="hint">добавление и отметки</div></div>
      <div class="stat g"><div class="label">Студенты</div><div class="value">${studs.length}</div><div class="hint">просмотр</div></div>
    </div>

    <div class="card">
      <h3>Как назначить роль</h3>
      <div class="muted">В последней колонке выбери роль — она применится сразу. Изменения действуют на пользователя в Telegram и в мини-приложении.</div>
    </div>

    <div class="card">
      <h3>Все пользователи (${users.length})</h3>`;

  if (!users.length) html += `<div class="empty">Пока никого нет — пусть напишут боту /start</div>`;
  else {
    html += `<table>
      <thead><tr>
        <th>Имя</th><th>Telegram ID</th><th>Роль</th><th>Посещаемость</th><th>Сменить роль</th>
      </tr></thead><tbody>`;
    users.forEach((u) => {
      const nm = u.full_name || u.FullName || "";
      const name = nm.trim() ? esc(nm) : (u.username ? "@" + esc(u.username) : `<span class="muted">ID ${esc(tgOf(u))}</span>`);
      const tg = tgOf(u);
      const role = u.role ?? u.Role ?? "student";
      html += `<tr>
        <td><b>${name}</b>${u.username && nm.trim() ? `<br><span class="muted">@${esc(u.username)}</span>` : ""}</td>
        <td><code>${tg}</code></td>
        <td>${roleBadge(role)}</td>
        <td>${u.total ? `${u.present}/${u.total} (${Math.round(u.percent)}%)` : "<span class='muted'>нет данных</span>"}</td>
        <td>
          <select data-act="role" data-tg="${tg}">
            ${["root", "admin", "proforg", "student"].map((r) => `<option value="${r}" ${role === r ? "selected" : ""}>${ {root:"👑 Староста",admin:"⚙️ Админ",proforg:"📋 Профорг",student:"👤 Студент"}[r] }</option>`).join("")}
          </select>
        </td>
      </tr>`;
    });
    html += `</tbody></table>`;
  }
  html += `</div>`;
  content.innerHTML = html;
}

function tgOf(u) {
  const v = u.tg_id ?? u.TgID;
  return v === undefined || v === null || v === "" ? 0 : v;
}

async function setRole(tgId, role) {
  try {
    await post("/api/panel/role", { tg_id: tgId, role });
    toast(`✅ Роль обновлена: ${role}`);
    await loadAll();
  } catch (e) { toast("❌ " + e.message, true); }
}

async function viewBooks() {
  const d = await get("/api/panel/content");
  let html = `
    <div class="card">
      <h3>➕ Добавить книгу с файлом</h3>
      <div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(200px,1fr));gap:12px">
        <div class="field"><label>Название *</label><input id="b-title" placeholder="Математика 1 курс"></div>
        <div class="field"><label>Предмет *</label><input id="b-subject" list="subj-list" placeholder="Высшая математика">
          <datalist id="subj-list">${subjectOptions("")}</datalist>
        </div>
        <div class="field"><label>Автор</label><input id="b-author" placeholder="Иванов И.И."></div>
        <div class="field"><label>Файл (PDF, DOCX…)</label><input type="file" id="b-file"></div>
      </div>
      <div class="field"><label>Описание</label><input id="b-desc" placeholder="Необязательно"></div>
      <button class="btn" data-act="save-book">💾 Загрузить в библиотеку</button>
    </div>

    <div class="card">
      <h3>📚 Книги (${d.books.length})</h3>`;

  if (!d.books.length) html += `<div class="empty">Библиотека пуста</div>`;
  else {
    html += `<table><thead><tr><th>#</th><th>Название</th><th>Предмет</th><th>Файл</th><th></th></tr></thead><tbody>`;
    d.books.forEach((b) => {
      const fileCell = b.attachment_id
        ? (b.attachment_id.startsWith("local:")
            ? `<a href="/api/panel/book-file?id=${b.id}" target="_blank" style="color:#4c8dff">📁 открыть</a>`
            : "☁️ telegram")
        : "—";
      html += `<tr>
        <td>${b.id}</td>
        <td><b>${esc(b.title)}</b>${b.author ? `<br><span class="muted">${esc(b.author)}</span>` : ""}</td>
        <td>${esc(b.subject)}</td>
        <td>${fileCell}</td>
        <td><button class="btn sm danger" data-act="del" data-kind="book" data-id="${b.id}">Удалить</button></td>
      </tr>`;
    });
    html += `</tbody></table>`;
  }
  html += `</div>`;
  content.innerHTML = html;
}

async function saveBook() {
  const title = $("#b-title").value.trim();
  const subject = $("#b-subject").value.trim();
  if (!title || !subject) { toast("❌ Заполни название и предмет", true); return; }
  const file = $("#b-file").files[0];

  try {
    if (file) {
      const fd = new FormData();
      fd.append("file", file);
      fd.append("title", title);
      fd.append("subject", subject);
      fd.append("author", $("#b-author").value.trim());
      fd.append("description", $("#b-desc").value.trim());
      await upload("/api/panel/upload-book", fd);
    } else {
      await post("/api/panel/add-book", {
        title, subject,
        author: $("#b-author").value.trim(),
        description: $("#b-desc").value.trim()
      });
    }
    toast("✅ Книга добавлена");
    await loadAll();
  } catch (e) { toast("❌ " + e.message, true); }
}

async function viewHomework() {
  const d = await get("/api/panel/content");
  const bySubject = {};
  const order = [];
  d.homework.forEach((h) => {
    if (!bySubject[h.subject]) { bySubject[h.subject] = []; order.push(h.subject); }
    bySubject[h.subject].push(h);
  });

  let html = `
    <div class="card">
      <h3>➕ Добавить домашнее задание</h3>
      <div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:12px">
        <div class="field"><label>Предмет *</label><input id="h-subject" list="h-subj" placeholder="Физика">
          <datalist id="h-subj">${subjectOptions("")}</datalist></div>
        <div class="field"><label>Задание *</label><input id="h-title" placeholder="Задачи 1–10"></div>
        <div class="field"><label>Дедлайн</label><input type="date" id="h-due"></div>
      </div>
      <div class="field"><label>Подробности</label><textarea id="h-desc" placeholder="Что именно сделать"></textarea></div>
      <button class="btn" data-act="save-hw">💾 Сохранить ДЗ</button>
    </div>

    <div class="card">
      <h3>📝 Домашка по предметам (${d.homework.length})</h3>`;

  if (!d.homework.length) html += `<div class="empty">Заданий нет</div>`;
  else {
    order.forEach((subject) => {
      html += `<div class="subject-line">📘 ${esc(subject)} — ${bySubject[subject].length}</div>`;
      html += `<table style="margin-bottom:16px"><thead><tr><th>#</th><th>Задание</th><th>Дедлайн</th><th></th></tr></thead><tbody>`;
      bySubject[subject].forEach((h) => {
        html += `<tr>
          <td>${h.id}</td>
          <td><b>${esc(h.title)}</b>${h.description ? `<br><span class="muted">${esc(h.description)}</span>` : ""}</td>
          <td>${h.due_date ? esc(h.due_date) : "—"}</td>
          <td><button class="btn sm danger" data-act="del" data-kind="homework" data-id="${h.id}">Удалить</button></td>
        </tr>`;
      });
      html += `</tbody></table>`;
    });
  }
  html += `</div>`;
  content.innerHTML = html;
}

async function saveHw() {
  const subject = $("#h-subject").value.trim();
  const title = $("#h-title").value.trim();
  if (!subject || !title) { toast("❌ Заполни предмет и задание", true); return; }
  try {
    await post("/api/panel/add-homework", {
      subject, title,
      description: $("#h-desc").value.trim(),
      due_date: $("#h-due").value
    });
    toast("✅ ДЗ добавлено");
    await loadAll();
  } catch (e) { toast("❌ " + e.message, true); }
}

async function viewEvents() {
  const d = await get("/api/panel/content");
  let html = `
    <div class="card">
      <h3>➕ Добавить событие</h3>
      <div class="grid" style="grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:12px">
        <div class="field"><label>Название *</label><input id="e-title" placeholder="Защита проектов"></div>
        <div class="field"><label>Дата</label><input type="date" id="e-date"></div>
      </div>
      <div class="field"><label>Описание</label><textarea id="e-desc"></textarea></div>
      <button class="btn" data-act="save-event">💾 Сохранить событие</button>
    </div>

    <div class="card">
      <h3>📅 События (${d.events.length})</h3>
      <h3 style="margin-top:18px">📊 Опросы (${d.polls.length})</h3>`;

  if (!d.events.length) html += `<div class="empty">Событий нет</div>`;
  else {
    html += `<table><thead><tr><th>#</th><th>Событие</th><th>Дата</th><th></th></tr></thead><tbody>`;
    d.events.forEach((e) => {
      html += `<tr><td>${e.id}</td>
        <td><b>${esc(e.title)}</b>${e.description ? `<br><span class="muted">${esc(e.description)}</span>` : ""}</td>
        <td>${e.event_date ? esc(e.event_date) : "—"}</td>
        <td><button class="btn sm danger" data-act="del" data-kind="event" data-id="${e.id}">Удалить</button></td></tr>`;
    });
    html += `</tbody></table>`;
  }

  if (!d.polls.length) html += `<div class="empty" style="margin-top:12px">Опросов нет</div>`;
  else {
    html += `<table style="margin-top:10px"><thead><tr><th>#</th><th>Вопрос</th><th>Статус</th><th></th></tr></thead><tbody>`;
    d.polls.forEach((p) => {
      html += `<tr><td>${p.id}</td><td>${esc(p.question)}</td>
        <td>${p.is_active ? '<span class="badge root">активен</span>' : '<span class="badge student">закрыт</span>'}</td>
        <td>
          ${p.is_active ? `<button class="btn sm warn" data-act="poll-close" data-id="${p.id}">Закрыть</button> ` : ""}
          <button class="btn sm danger" data-act="del" data-kind="poll" data-id="${p.id}">Удалить</button>
        </td></tr>`;
    });
    html += `</tbody></table>`;
  }
  html += `</div>`;
  content.innerHTML = html;
}

async function saveEvent() {
  const title = $("#e-title").value.trim();
  if (!title) { toast("❌ Введи название", true); return; }
  try {
    await post("/api/panel/add-event", {
      title,
      description: $("#e-desc").value.trim(),
      event_date: $("#e-date").value
    });
    toast("✅ Событие добавлено");
    await loadAll();
  } catch (e) { toast("❌ " + e.message, true); }
}

async function del(kind, id) {
  if (!confirm(`Удалить ${kind} #${id}?`)) return;
  try { await post("/api/panel/delete", { kind, id }); toast("✅ Удалено"); await loadAll(); }
  catch (e) { toast("❌ " + e.message, true); }
}

async function closePoll(id) {
  try { await post("/api/panel/poll/close", { id }); toast("🔒 Опрос закрыт"); await loadAll(); }
  catch (e) { toast("❌ " + e.message, true); }
}

async function viewBroadcast() {
  content.innerHTML = `
    <div class="card">
      <h3>📣 Рассылка по боту</h3>
      <div class="field">
        <label>Кому</label>
        <select id="b-role">
          <option value="all">Всем пользователям</option>
          <option value="student">Только студентам</option>
        </select>
      </div>
      <div class="field">
        <label>Текст сообщения</label>
        <textarea id="b-text" placeholder="Внимание! Завтра в 10:00 контрольная работа по физике."></textarea>
      </div>
      <button class="btn" data-act="broadcast">🚀 Отправить</button>
    </div>`;
}

async function sendBroadcast() {
  const text = $("#b-text").value.trim();
  const role = $("#b-role").value;
  if (!text) { toast("❌ Введи текст", true); return; }
  if (!confirm(`Отправить (${role === "all" ? "всем" : "студентам"})?`)) return;
  try {
    const r = await post("/api/panel/broadcast", { text, role });
    toast(`✅ Отправлено: ${r.sent}, ошибок: ${r.failed}`);
    $("#b-text").value = "";
  } catch (e) { toast("❌ " + e.message, true); }
}

async function viewLogs() {
  const logs = await get("/api/panel/logs");
  let html = `<div class="card">
    <h3>Журнал действий</h3>
    <div class="logs" id="logs">`;
  if (!logs.length) html += `<span class="muted">Записей пока нет</span>`;
  logs.slice().reverse().forEach((l) => {
    const t = new Date(l.time).toLocaleTimeString("ru-RU");
    html += `<div class="log-line"><span class="log-time">${t}</span>${esc(l.text)}</div>`;
  });
  html += `</div><div style="margin-top:12px"><button class="btn ghost sm" data-act="clear-logs">🗑 Очистить журнал</button></div></div>`;
  content.innerHTML = html;
}

async function clearLogs() {
  if (!confirm("Очистить журнал?")) return;
  try { await post("/api/panel/clear", { what: "logs" }); toast("✅ Журнал очищен"); await viewLogs(); }
  catch (e) { toast("❌ " + e.message, true); }
}

async function clearAttendance() {
  if (!confirm("Очистить все отметки посещаемости за сегодня?")) return;
  try {
    const r = await post("/api/panel/clear", { what: "attendance" });
    toast(`✅ Очищено отметок: ${r.cleared}`);
  } catch (e) { toast("❌ " + e.message, true); }
}

document.querySelectorAll(".nav-item").forEach((btn) => {
  btn.onclick = () => { view = btn.dataset.view; loadAll(); };
});

// All controls use data-act attributes instead of inline handlers, so the
// panel works with a strict Content-Security-Policy.
document.addEventListener("click", (e) => {
  const el = e.target.closest("[data-act]");
  if (!el || el.tagName === "SELECT") return;
  const id = Number(el.dataset.id || 0);
  switch (el.dataset.act) {
    case "reload": loadAll(); break;
    case "go": view = el.dataset.view; loadAll(); break;
    case "clear-attendance": clearAttendance(); break;
    case "save-book": saveBook(); break;
    case "save-hw": saveHw(); break;
    case "save-event": saveEvent(); break;
    case "del": del(el.dataset.kind, id); break;
    case "poll-close": closePoll(id); break;
    case "broadcast": sendBroadcast(); break;
    case "clear-logs": clearLogs(); break;
  }
});

document.addEventListener("change", (e) => {
  const el = e.target.closest("select[data-act='role']");
  if (el) setRole(Number(el.dataset.tg), el.value);
});

loadAll();
setInterval(() => { if (view === "dashboard") loadAll(); }, 5000);