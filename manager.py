#!/usr/bin/env python3
"""
StudBot Manager — настольное приложение для запуска и управления ботом.

Возможности:
  * запуск, остановка и перезапуск бота
  * живой просмотр логов
  * состояние Telegram, туннеля и веб-сервисов
  * работа с Git: коммиты, изменения, отправка на GitHub
  * быстрые переходы: админ-панель, мини-приложение, папка проекта

Зависимостей нет — только стандартная библиотека Python 3.10+.
"""

from __future__ import annotations

import os
import queue
import re
import shutil
import subprocess
import sys
import threading
import time
import webbrowser
from datetime import datetime
from pathlib import Path

import tkinter as tk
from tkinter import filedialog, messagebox, ttk

APP_TITLE = "StudBot Manager"
ROOT = Path(__file__).resolve().parent
EXE = ROOT / "studbot.exe"
LOG_FILE = ROOT / "bot.log"
ENV_FILE = ROOT / ".env"

# Прокси для проверки доступности Telegram
TELEGRAM_URL = "https://api.telegram.org"

# Паттерны секретов. Требования намеренно строгие, чтобы плейсхолдеры
# в README и .env.example не считались реальными секретами.
SECRET_PATTERNS = [
    re.compile(r"ghp_[A-Za-z0-9]{30,}"),
    re.compile(r"github_pat_[A-Za-z0-9_]{40,}"),
    # реальный токен Telegram выглядит так: 123456789:AAH... (35+ символов)
    re.compile(r"\b\d{8,10}:[A-Za-z0-9_-]{35,}\b"),
    # реальный пароль из .env — ASCII-строка от 12 символов
    re.compile(r"^\s*ADMIN_PASSWORD\s*=\s*[A-Za-z0-9!@#$%^&*._-]{12,}\s*$", re.M),
]

# Файлы, где секретов быть не должно в принципе
SECRET_FREE_FILES = {".env.example", "manager.py", ".gitignore", "LICENSE"}

# Палитра
BG = "#0f151e"
CARD = "#1a2331"
CARD2 = "#212c3c"
LINE = "#2b3749"
TEXT = "#e6edf5"
MUTED = "#8494a8"
ACCENT = "#4c8dff"
ACCENT2 = "#7c5cff"
GREEN = "#2fd07a"
RED = "#ff5c62"
YELLOW = "#ffc53d"


def run(cmd, cwd=None, timeout=60, env=None):
    """Запускает команду и возвращает (код, stdout, stderr)."""
    e = dict(os.environ)
    if env:
        e.update(env)
    try:
        p = subprocess.run(
            cmd,
            cwd=str(cwd or ROOT),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout,
            env=e,
        )
        return p.returncode, p.stdout.strip(), p.stderr.strip()
    except FileNotFoundError:
        return 127, "", f"не найдено: {cmd[0]}"
    except subprocess.TimeoutExpired:
        return 124, "", "превышено время ожидания"


def bot_pid() -> int | None:
    if sys.platform == "win32":
        code, out, _ = run(
            ["tasklist", "/FI", "IMAGENAME eq studbot.exe", "/NH", "/FO", "CSV"],
            timeout=15,
        )
        if code == 0:
            for line in out.splitlines():
                parts = [p.strip('" ') for p in line.split('","')]
                if len(parts) > 1 and parts[0].lower() == "studbot.exe":
                    try:
                        return int(parts[1])
                    except ValueError:
                        pass
    else:
        code, out, _ = run(["pgrep", "-f", "studbot"], timeout=15)
        if code == 0 and out:
            try:
                return int(out.splitlines()[0])
            except ValueError:
                pass
    return None


def kill_bot() -> tuple[bool, str]:
    if sys.platform == "win32":
        code, _, err = run(["taskkill", "/F", "/IM", "studbot.exe"], timeout=20)
        if code == 0:
            return True, "процесс остановлен"
        return False, err or "не удалось остановить"
    pid = bot_pid()
    if not pid:
        return True, "процесс не запущен"
    import signal

    os.kill(pid, signal.SIGTERM)
    return True, "процесс остановлен"


def telegram_ok(timeout: int = 6) -> tuple[bool, str]:
    """Проверяет доступность Telegram через системный прокси."""
    proxy = read_proxy()
    if proxy:
        code, out, _ = run(
            ["curl", "-s", "-o", "NUL", "-w", "%{http_code}",
             "--proxy", proxy, "--max-time", str(timeout), TELEGRAM_URL],
            timeout=timeout + 5,
        )
        if code == 0 and out.startswith(("200", "302", "401")):
            return True, f"доступен через прокси {proxy}"
    code, out, _ = run(
        ["curl", "-s", "-o", "NUL", "-w", "%{http_code}",
         "--noproxy", "*", "--max-time", str(timeout), TELEGRAM_URL],
        timeout=timeout + 5,
    )
    if code == 0 and out.startswith(("200", "302", "401")):
        return True, "доступен напрямую"
    return False, "недоступен — проверь VPN"


def read_proxy() -> str | None:
    """Берёт прокси из .env, иначе из настроек Windows."""
    if ENV_FILE.exists():
        for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
            line = line.strip()
            if line.startswith("#"):
                continue
            if line.upper().startswith("PROXY_URL="):
                value = line.split("=", 1)[1].strip().strip('"').strip("'")
                return value or None
    if sys.platform == "win32":
        try:
            import winreg

            key = winreg.OpenKey(
                winreg.HKEY_CURRENT_USER,
                r"Software\Microsoft\Windows\CurrentVersion\Internet Settings",
            )
            enable = winreg.QueryValueEx(key, "ProxyEnable")[0]
            if enable == 1:
                server = winreg.QueryValueEx(key, "ProxyServer")[0]
                if "=" in server:
                    for part in server.split(";"):
                        k, _, v = part.partition("=")
                        if k in ("https", "http"):
                            return "http://" + v
                elif server:
                    return "http://" + server
        except Exception:
            pass
    return None


def port_open(port: int, host: str = "127.0.0.1") -> bool:
    import socket

    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
        s.settimeout(1.0)
        return s.connect_ex((host, port)) == 0


class LogWatcher:
    """Читает файл лога и отдаёт новые строки через очередь."""

    def __init__(self):
        self.q: queue.Queue[str] = queue.Queue()
        self._stop = threading.Event()
        self._thread = threading.Thread(target=self._loop, daemon=True)
        self._started = False

    def start(self):
        if not self._started:
            self._started = True
            self._thread.start()

    def stop(self):
        self._stop.set()

    def _loop(self):
        pos = 0
        while not self._stop.is_set():
            if LOG_FILE.exists():
                try:
                    size = LOG_FILE.stat().st_size
                    if size < pos:
                        pos = 0
                    with LOG_FILE.open("r", encoding="utf-8", errors="replace") as f:
                        f.seek(pos)
                        for line in f:
                            self.q.put(line.rstrip())
                        pos = f.tell()
                except Exception:
                    pass
            time.sleep(0.7)


class Manager(tk.Tk):
    def __init__(self):
        super().__init__()
        self.title(APP_TITLE)
        self.geometry("1080x720")
        self.minsize(900, 600)
        self.configure(bg=BG)

        self.proc = None
        self.log_queue: queue.Queue[str] = queue.Queue()
        self._last_status = 0.0
        self._closing = False

        self._setup_style()
        self._build_ui()
        self._start_log_watchers()

        self.after(300, self._drain_log)
        self.after(500, self._tick)

    # ---------- оформление ----------

    def _setup_style(self):
        s = ttk.Style()
        try:
            s.theme_use("clam")
        except tk.TclError:
            pass
        s.configure("TFrame", background=BG)
        s.configure("Card.TFrame", background=CARD)
        s.configure("TLabel", background=BG, foreground=TEXT, font=("Segoe UI", 10))
        s.configure("Muted.TLabel", background=BG, foreground=MUTED, font=("Segoe UI", 9))
        s.configure("CardMuted.TLabel", background=CARD, foreground=MUTED, font=("Segoe UI", 9))
        s.configure("CardTitle.TLabel", background=CARD, foreground=TEXT,
                    font=("Segoe UI", 12, "bold"))
        s.configure("Card.TLabelframe", background=CARD, bordercolor=LINE)
        s.configure("Card.TLabelframe.Label", background=CARD, foreground=MUTED,
                    font=("Segoe UI", 9))

        s.configure("TNotebook", background=BG, bordercolor=LINE, tabmargins=(0, 6, 0, 0))
        s.configure("TNotebook.Tab", background=CARD2, foreground=MUTED,
                    padding=(18, 9), font=("Segoe UI", 10, "bold"))
        s.map("TNotebook.Tab",
              background=[("selected", ACCENT)],
              foreground=[("selected", "#ffffff")])

        s.configure("Accent.TButton", background=ACCENT, foreground="#ffffff",
                    font=("Segoe UI", 10, "bold"), padding=(16, 10), borderwidth=0)
        s.map("Accent.TButton",
              background=[("active", "#3b7ae8"), ("disabled", "#33465e")])
        s.configure("Ghost.TButton", background=CARD2, foreground=TEXT,
                    font=("Segoe UI", 10), padding=(14, 9), bordercolor=LINE)
        s.map("Ghost.TButton", background=[("active", "#2a3749")])
        s.configure("Danger.TButton", background=RED, foreground="#ffffff",
                    font=("Segoe UI", 10, "bold"), padding=(14, 9), borderwidth=0)
        s.map("Danger.TButton", background=[("active", "#d63f44")])
        s.configure("Green.TButton", background=GREEN, foreground="#04210f",
                    font=("Segoe UI", 10, "bold"), padding=(14, 9), borderwidth=0)
        s.map("Green.TButton", background=[("active", "#26b869")])

        s.configure("TEntry", fieldbackground=CARD2, background=CARD2, foreground=TEXT,
                    bordercolor=LINE, insertcolor=TEXT, padding=(10, 8))
        s.configure("TText", fieldbackground=CARD2, background=CARD2, foreground=TEXT,
                    insertcolor=TEXT, bordercolor=LINE, padx=8, pady=8)
        s.configure("Treeview", background=CARD2, fieldbackground=CARD2, foreground=TEXT,
                    bordercolor=LINE, rowheight=26, font=("Segoe UI", 9))
        s.configure("Treeview.Heading", background=CARD, foreground=MUTED,
                    font=("Segoe UI", 9, "bold"), relief="flat")
        s.map("Treeview", background=[("selected", ACCENT)],
              foreground=[("selected", "#ffffff")])

    def _card(self, parent, title="", subtitle=""):
        box = ttk.Frame(parent, style="Card.TFrame", padding=(16, 14))
        if title:
            head = ttk.Frame(box, style="Card.TFrame")
            ttk.Label(head, text=title, style="CardTitle.TLabel").pack(anchor="w")
            if subtitle:
                ttk.Label(head, text=subtitle, style="CardMuted.TLabel").pack(anchor="w")
            head.pack(fill="x", pady=(0, 10))
        return box

    def _build_ui(self):
        # верхняя панель
        top = ttk.Frame(self, padding=(14, 12))
        top.pack(fill="x")
        ttk.Label(top, text="🤖  StudBot", font=("Segoe UI", 17, "bold")).pack(side="left")
        self.lbl_badge = tk.Label(
            top, text="● проверка…", bg=CARD, fg=YELLOW,
            font=("Segoe UI", 10, "bold"), padx=14, pady=7,
        )
        self.lbl_badge.pack(side="left", padx=(16, 0))

        self.lbl_uptime = tk.Label(
            top, text="", bg=BG, fg=MUTED, font=("Segoe UI", 9)
        )
        self.lbl_uptime.pack(side="right")

        # вкладки
        self.nb = ttk.Notebook(self)
        self.nb.pack(fill="both", expand=True, padx=14, pady=(0, 14))
        self.tab_main = ttk.Frame(self.nb, padding=(4, 10, 4, 4))
        self.tab_git = ttk.Frame(self.nb, padding=(4, 10, 4, 4))
        self.tab_tools = ttk.Frame(self.nb, padding=(4, 10, 4, 4))
        self.nb.add(self.tab_main, text="  Обзор и логи  ")
        self.nb.add(self.tab_git, text="  Git  ")
        self.nb.add(self.tab_tools, text="  Инструменты  ")

        self._build_main()
        self._build_git()
        self._build_tools()

    def _build_main(self):
        left = ttk.Frame(self.tab_main)
        left.pack(side="left", fill="both", expand=True, padx=(0, 10))

        controls = self._card(left, "Управление ботом", "запуск, остановка и пересборка")
        row = ttk.Frame(controls, style="Card.TFrame")
        row.pack(fill="x")
        self.btn_start = ttk.Button(row, text="▶  Запустить", style="Green.TButton",
                                    command=self.start_bot)
        self.btn_start.pack(side="left", padx=(0, 8))
        self.btn_stop = ttk.Button(row, text="■  Остановить", style="Danger.TButton",
                                   command=self.stop_bot)
        self.btn_stop.pack(side="left", padx=(0, 8))
        ttk.Button(row, text="⟳  Перезапустить", style="Ghost.TButton",
                   command=self.restart_bot).pack(side="left", padx=(0, 8))
        ttk.Button(row, text="⚙  Пересобрать", style="Ghost.TButton",
                   command=self.rebuild_bot).pack(side="left")

        row2 = ttk.Frame(controls, style="Card.TFrame")
        row2.pack(fill="x", pady=(12, 0))
        ttk.Button(row2, text="🌐  Открыть админ-панель", style="Ghost.TButton",
                   command=lambda: self._open_url("http://127.0.0.1:8081")).pack(side="left", padx=(0, 8))
        ttk.Button(row2, text="📱  Открыть мини-приложение", style="Ghost.TButton",
                   command=lambda: self._open_url("http://localhost:8080")).pack(side="left")

        status = self._card(left, "Состояние", "проверяется автоматически каждые 5 секунд")
        self.status_tree = ttk.Treeview(
            status, columns=("param", "value"), show="headings", height=7
        )
        self.status_tree.heading("param", text="Параметр")
        self.status_tree.heading("value", text="Значение")
        self.status_tree.column("param", width=210, anchor="w")
        self.status_tree.column("value", width=380, anchor="w")
        self.status_tree.pack(fill="x")
        self.status_tree.tag_configure("ok", foreground=GREEN)
        self.status_tree.tag_configure("bad", foreground=RED)
        self.status_tree.tag_configure("warn", foreground=YELLOW)

        logs = self._card(left, "Журнал", "bot.log — обновляется в реальном времени")
        wrap = ttk.Frame(logs, style="Card.TFrame")
        wrap.pack(fill="both", expand=True)
        self.log_text = tk.Text(
            wrap, height=12, bg=CARD2, fg="#c9d6e5", insertbackground=TEXT,
            relief="flat", font=("Consolas", 9), wrap="none",
        )
        sb = ttk.Scrollbar(wrap, orient="vertical", command=self.log_text.yview)
        self.log_text.configure(yscrollcommand=sb.set)
        self.log_text.pack(side="left", fill="both", expand=True)
        sb.pack(side="right", fill="y")
        self.log_text.tag_configure("err", foreground=RED)
        self.log_text.tag_configure("warn", foreground=YELLOW)
        self.log_text.tag_configure("ok", foreground=GREEN)

    def _build_git(self):
        head = self._card(self.tab_git, "Git", "изменения, коммиты и отправка на GitHub")
        row = ttk.Frame(head, style="Card.TFrame")
        row.pack(fill="x")
        self.lbl_git_branch = tk.Label(row, text="", bg=CARD, fg=ACCENT,
                                       font=("Segoe UI", 10, "bold"))
        self.lbl_git_branch.pack(side="left", padx=(0, 16))
        self.lbl_git_sync = tk.Label(row, text="", bg=CARD, fg=MUTED,
                                     font=("Segoe UI", 9))
        self.lbl_git_sync.pack(side="left")
        ttk.Button(row, text="⟳  Обновить", style="Ghost.TButton",
                   command=self.refresh_git).pack(side="right")

        mid = ttk.Frame(self.tab_git)
        mid.pack(fill="both", expand=True, pady=(10, 0))

        left = ttk.Frame(mid)
        left.pack(side="left", fill="both", expand=True, padx=(0, 10))

        changes = self._card(left, "Изменённые файлы", "будут добавлены в коммит")
        self.changes_tree = ttk.Treeview(
            changes, columns=("st", "path"), show="headings", height=9
        )
        self.changes_tree.heading("st", text="")
        self.changes_tree.heading("path", text="Файл")
        self.changes_tree.column("st", width=34, anchor="center")
        self.changes_tree.column("path", width=460, anchor="w")
        self.changes_tree.pack(fill="both", expand=True)
        self.changes_tree.tag_configure("add", foreground=GREEN)
        self.changes_tree.tag_configure("mod", foreground=YELLOW)
        self.changes_tree.tag_configure("del", foreground=RED)
        self.changes_tree.tag_configure("secret", foreground=RED, background="#3a1c1f")

        commit = self._card(left, "Новый коммит", "кратко опиши, что изменилось")
        self.msg_text = tk.Text(
            commit, height=3, bg=CARD2, fg=TEXT, insertbackground=TEXT,
            relief="flat", font=("Segoe UI", 10), wrap="word", padx=10, pady=8,
        )
        self.msg_text.pack(fill="x")
        self.msg_text.insert("1.0", "")
        self.msg_text.focus_set()

        brow = ttk.Frame(commit, style="Card.TFrame")
        brow.pack(fill="x", pady=(10, 0))
        self.btn_commit = ttk.Button(brow, text="💾  Только коммит", style="Ghost.TButton",
                                     command=lambda: self.do_commit(push=False))
        self.btn_commit.pack(side="left", padx=(0, 8))
        self.btn_commit_push = ttk.Button(brow, text="🚀  Коммит и отправить",
                                           style="Accent.TButton",
                                           command=lambda: self.do_commit(push=True))
        self.btn_commit_push.pack(side="left")

        right = ttk.Frame(mid)
        right.pack(side="right", fill="both", expand=True)

        hist = self._card(right, "История", "последние коммиты")
        self.log_tree = ttk.Treeview(
            hist, columns=("sha", "date", "msg"), show="headings", height=20
        )
        self.log_tree.heading("sha", text="Хэш")
        self.log_tree.heading("date", text="Когда")
        self.log_tree.heading("msg", text="Сообщение")
        self.log_tree.column("sha", width=70, anchor="w")
        self.log_tree.column("date", width=120, anchor="w")
        self.log_tree.column("msg", width=250, anchor="w")
        self.log_tree.pack(fill="both", expand=True)

    def _build_tools(self):
        card = self._card(self.tab_tools, "Инструменты", "быстрые действия")

        items = [
            ("📊  Скачать таблицу посещаемости", "Открыть папку с файлами Excel", self.open_exports),
            ("📚  Загрузить книгу в библиотеку", "Выбрать PDF или документ", self.upload_book),
            ("🔑  Показать пароль админ-панели", "Прочитать из .env", self.show_password),
            ("📄  Открыть папку проекта", "Файлы и база данных", lambda: self._open_path(ROOT)),
            ("📁  Открыть папку data", "База, загрузки, выгрузки", lambda: self._open_path(ROOT / "data")),
            ("🧹  Очистить логи", "Удалить bot.log", self.clear_logs),
        ]
        for title, sub, cmd in items:
            row = ttk.Frame(card, style="Card.TFrame")
            row.pack(fill="x", pady=(0, 8))
            ttk.Label(row, text=title, style="CardTitle.TLabel").pack(side="left")
            ttk.Button(row, text="Открыть", style="Ghost.TButton",
                       command=cmd).pack(side="right")
            ttk.Label(row, text=sub, style="CardMuted.TLabel").pack(side="left", padx=(14, 0))

        info = self._card(self.tab_tools, "О проекте")
        ttk.Label(
            info,
            text=(
                "StudBot — Telegram-бот для староста группы.\n"
                "Библиотека с файлами, домашние задания по предметам,\n"
                "перекличка через голосование с выгрузкой в Excel по неделям,\n"
                "опросы, события, роли, мини-приложение и админ-панель."
            ),
            style="CardMuted.TLabel", justify="left",
        ).pack(anchor="w")

        btns = ttk.Frame(self.tab_tools, style="Card.TFrame")
        btns.pack(fill="x", pady=(4, 0))
        ttk.Button(btns, text="🐙  Открыть репозиторий на GitHub", style="Ghost.TButton",
                   command=lambda: self._open_url("https://github.com/pchelenokk/studbot-tg")).pack(anchor="w")

    # ---------- логи ----------

    def _start_log_watchers(self):
        if LOG_FILE.exists():
            try:
                data = LOG_FILE.read_text(encoding="utf-8", errors="replace").splitlines()[-300:]
                for line in data:
                    self._append_log(line)
            except Exception:
                pass
        self.msg_queue_pump()

    def msg_queue_pump(self):
        threading.Thread(target=self._log_thread, daemon=True).start()

    def _log_thread(self):
        watcher = LogWatcher()
        watcher.start()
        while not self._closing:
            try:
                line = watcher.q.get(timeout=0.5)
                self.log_queue.put(line)
            except queue.Empty:
                pass

    def _drain_log(self):
        while True:
            try:
                line = self.log_queue.get_nowait()
                self._append_log(line)
            except queue.Empty:
                break
        self.after(300, self._drain_log)

    def _append_log(self, line: str):
        tag = None
        low = line.lower()
        if "error" in low or "offline" in low or "failed" in low or "panic" in low:
            tag = "err"
        elif "warn" in low or "retrying" in low:
            tag = "warn"
        elif "online" in low or "started" in low or "запущен" in low:
            tag = "ok"
        self.log_text.insert("end", line + "\n", tag)
        if int(self.log_text.index("end-1c").split(".")[0]) > 1200:
            self.log_text.delete("1.0", "300.0")
        self.log_text.see("end")

    # ---------- управление ботом ----------

    def start_bot(self):
        if bot_pid():
            self._say("Бот уже запущен")
            return
        if not EXE.exists():
            if not messagebox.askyesno("Нет сборки",
                                       "studbot.exe не найден. Собрать сейчас?"):
                return
            if not self._build():
                return
        flags = 0
        if sys.platform == "win32":
            flags = 0x08000000  # CREATE_NO_WINDOW
        try:
            self.proc = subprocess.Popen(
                [str(EXE)], cwd=str(ROOT),
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
                creationflags=flags,
            )
            self._say("Бот запускается…")
        except Exception as e:
            messagebox.showerror("Ошибка", f"Не удалось запустить:\n{e}")

    def stop_bot(self):
        ok, msg = kill_bot()
        if ok:
            self.proc = None
            self._say("Бот остановлен")
        else:
            messagebox.showwarning("Не удалось", msg)

    def restart_bot(self):
        self.stop_bot()
        time.sleep(1.2)
        self.start_bot()

    def rebuild_bot(self) -> bool:
        if not messagebox.askyesno("Пересборка",
                                   "Остановить бот и пересобрать studbot.exe?"):
            return False
        self.stop_bot()
        return self._build()

    def _build(self) -> bool:
        if shutil.which("go") is None:
            messagebox.showerror("Go не найден",
                                 "Go не установлен или нет в PATH.\n"
                                 "Установи с https://go.dev/dl/")
            return False
        self._say("Сборка…")
        try:
            p = subprocess.run(["go", "build", "-o", "studbot.exe", "./cmd/bot"],
                               cwd=str(ROOT), capture_output=True, text=True,
                               encoding="utf-8", errors="replace", timeout=300)
        except subprocess.TimeoutExpired:
            messagebox.showerror("Сборка", "Превышено время ожидания")
            return False
        if p.returncode != 0:
            messagebox.showerror("Ошибка сборки", p.stderr or p.stdout)
            self._say("Сборка не удалась")
            return False
        self._say("Сборка успешна")
        return True

    # ---------- Git ----------

    def refresh_git(self):
        if not (ROOT / ".git").exists():
            self.lbl_git_branch.configure(text="не git-репозиторий", fg=RED)
            self.changes_tree.delete(*self.changes_tree.get_children())
            return

        code, branch, _ = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], timeout=15)
        branch = branch or "?"
        self.lbl_git_branch.configure(text=f"ветка: {branch}", fg=ACCENT)

        code, remote, _ = run(["git", "config", "--get", "remote.origin.url"], timeout=15)
        repo = remote.rstrip("/").removesuffix(".git") if code == 0 else ""

        code, log_out, _ = run(
            ["git", "log", "--pretty=format:%h|%ad|%s", "--date=format:%d.%m %H:%M", "-25"],
            timeout=25,
        )
        self.log_tree.delete(*self.log_tree.get_children())
        for line in log_out.splitlines():
            parts = line.split("|", 2)
            if len(parts) == 3:
                self.log_tree.insert("", "end", values=(parts[0], parts[1], parts[2]))

        code, status_out, _ = run(["git", "status", "--porcelain"], timeout=25)
        self.changes_tree.delete(*self.changes_tree.get_children())
        secrets: list[str] = []

        for line in status_out.splitlines():
            if len(line) < 4:
                continue
            st = line[:2].strip() or "?"
            path = line[3:].strip()
            tag = {"M": "mod", "A": "add", "R": "mod", "D": "del",
                   "??": "add", "!": "mod"}.get(st[0], "mod")
            if path in (".env", ".env.local"):
                secrets.append(path)
                self.changes_tree.insert("", "end", values=(st, path), tags=("secret",))
                continue
            if _file_has_secret(ROOT / path):
                secrets.append(path)
                self.changes_tree.insert("", "end", values=(st, path), tags=("secret",))
                continue
            self.changes_tree.insert("", "end", values=(st, path), tags=(tag,))

        code, counts, _ = run(
            ["git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD"], timeout=20
        )
        sync = ""
        sync_color = MUTED
        if code == 0 and counts:
            bits = counts.split()
            if len(bits) == 2:
                behind, ahead = bits
                if ahead != "0" or behind != "0":
                    sync = f"вперёд {ahead} · назад {behind}"
                    sync_color = YELLOW
                else:
                    sync = "синхронизировано с GitHub"
                    sync_color = GREEN
        if not sync:
            sync = "нет данных об upstream"
        self.lbl_git_sync.configure(text=f"{sync}  ·  {repo or 'remote не задан'}", fg=sync_color)

        self.pending_secrets = secrets

    def do_commit(self, push: bool):
        if not (ROOT / ".git").exists():
            messagebox.showinfo("Git", "Папка не является git-репозиторием")
            return

        message = self.msg_text.get("1.0", "end").strip()
        if not message:
            messagebox.showwarning("Нет сообщения",
                                   "Опиши изменения — это сообщение увидят другие.")
            return
        if len(message) > 200:
            message = message[:200]

        if getattr(self, "pending_secrets", None):
            messagebox.showerror(
                "Нельзя закоммитить",
                "В этих файлах похожи на секреты:\n\n  "
                + "\n  ".join(self.pending_secrets[:10])
                + "\n\nДобавь их в .gitignore или убери из изменений.",
            )
            return

        code, status_out, _ = run(["git", "status", "--porcelain"], timeout=25)
        if not status_out.strip():
            messagebox.showinfo("Нечего коммитить", "Рабочая папка не изменена.")
            return

        self._set_commit_enabled(False)
        try:
            code, _, err = run(["git", "add", "-A"], timeout=120)
            if code != 0:
                messagebox.showerror("git add", err)
                return

            code, _, err = run(["git", "commit", "-m", message], timeout=120)
            if code != 0:
                low = (err or "").lower()
                if "nothing to commit" in low:
                    self._say("Изменений не осталось")
                    return
                messagebox.showerror("git commit", err)
                return

            self._say(f"Коммит создан: {message[:60]}")
            self.msg_text.delete("1.0", "end")
            self.refresh_git()

            if push:
                self._push()
        finally:
            self._set_commit_enabled(True)

    def _push(self) -> None:
        self._say("Отправка на GitHub…")
        self._set_commit_enabled(False)
        code, out, err = run(["git", "push", "origin", "HEAD"], timeout=180)
        if code == 0:
            self._say("Отправлено на GitHub")
            messagebox.showinfo("Готово", "Изменения отправлены на GitHub.")
        else:
            text = err or out
            self._say("Не удалось отправить")
            hint = ""
            if "could not read Username" in text or "Authentication failed" in text or "terminal prompts" in text:
                hint = (
                    "\n\nGit не смог спросить логин и пароль — это обычное дело для "
                    "приложений без окна ввода.\n\n"
                    "Открой обычную командную строку и выполни:\n"
                    "    gh auth login\n"
                    "или выполни push вручную:\n"
                    "    git push origin main"
                )
            messagebox.showerror("Push не удался", text[:600] + hint)
        self.refresh_git()
        self._set_commit_enabled(True)

    def _set_commit_enabled(self, enabled: bool):
        state = "normal" if enabled else "disabled"
        self.btn_commit.configure(state=state)
        self.btn_commit_push.configure(state=state)

    # ---------- прочее ----------

    def _tick(self):
        now = time.time()
        if now - self._last_status >= 5:
            self._last_status = now
            self._refresh_status()
            if self.nb.index("current") == 1:
                self.refresh_git()
        self.after(1000, self._tick)

    def _refresh_status(self):
        pid = bot_pid()
        running = pid is not None

        self.btn_start.configure(state="disabled" if running else "normal")
        self.btn_stop.configure(state="normal" if running else "disabled")

        if running:
            self.lbl_badge.configure(text=f"● работает  PID {pid}", fg=GREEN)
        else:
            self.lbl_badge.configure(text="● остановлен", fg=RED)

        if EXE.exists():
            mt = datetime.fromtimestamp(EXE.stat().st_mtime)
            self.lbl_uptime.configure(text=f"сборка от {mt:%d.%m.%Y %H:%M}")
        else:
            self.lbl_uptime.configure(text="сборка не найдена")

        tg_ok, tg_msg = telegram_ok()

        proxy = read_proxy()
        rows = [
            ("Процесс", f"работает, PID {pid}" if running else "остановлен",
             "ok" if running else "bad"),
            ("Сборка", "есть" if EXE.exists() else "нет — нажми «Пересобрать»",
             "ok" if EXE.exists() else "warn"),
            ("Telegram", tg_msg, "ok" if tg_ok else "bad"),
            ("Прокси", proxy or "не задан", "ok" if proxy else "warn"),
            ("Мини-приложение", ":8080 отвечает" if port_open(8080) else ":8080 молчит",
             "ok" if port_open(8080) else "bad"),
            ("Админ-панель", ":8081 отвечает" if port_open(8081) else ":8081 молчит",
             "ok" if port_open(8081) else "bad"),
            ("Журнал", "bot.log" if LOG_FILE.exists() else "нет файла",
             "ok" if LOG_FILE.exists() else "warn"),
        ]
        if self.bot_started_at:
            self.lbl_uptime.configure(
                text=f"работает {time.time() - self.bot_started_at:.0f} с"
            )

        self.status_tree.delete(*self.status_tree.get_children())
        for name, value, tag in rows:
            self.status_tree.insert("", "end", values=(name, value), tags=(tag,))

    bot_started_at = 0.0

    def _say(self, text: str):
        stamp = datetime.now().strftime("%H:%M:%S")
        self._append_log(f"{stamp} [менеджер] {text}")

    def _open_url(self, url: str):
        webbrowser.open(url)

    def _open_path(self, path: Path):
        path = Path(path)
        if not path.exists():
            messagebox.showinfo("Папка не найдена", str(path))
            return
        if sys.platform == "win32":
            os.startfile(str(path))  # noqa: S606
        else:
            webbrowser.open(path.as_uri())

    def open_exports(self):
        d = ROOT / "data" / "exports"
        if not d.exists():
            messagebox.showinfo("Пока пусто",
                                "Папка появится после первой закрытой переклички.")
            return
        files = sorted(d.glob("*.xlsx"), key=lambda p: p.stat().st_mtime, reverse=True)
        if not files:
            messagebox.showinfo("Пока пусто", "Файлов ещё нет.")
            return
        self._open_path(d)
        if len(files) == 1 and messagebox.askyesno(
            "Открыть файл", f"Открыть {files[0].name}?"
        ):
            webbrowser.open(files[0].as_uri())

    def upload_book(self):
        path = filedialog.askopenfilename(
            title="Выбери файл книги",
            filetypes=[
                ("Документы", "*.pdf *.docx *.doc *.txt *.epub"),
                ("Все файлы", "*.*"),
            ],
        )
        if not path:
            return
        self._open_url("http://127.0.0.1:8081")

    def show_password(self):
        if not ENV_FILE.exists():
            messagebox.showinfo("Нет файла", ".env не найден")
            return
        for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
            if line.strip().upper().startswith("ADMIN_PASSWORD="):
                value = line.split("=", 1)[1].strip().strip('"').strip("'")
                messagebox.showinfo("Пароль админ-панели", value or "(пусто)")
                return
        messagebox.showinfo("Пароль", "ADMIN_PASSWORD не задан в .env")

    def clear_logs(self):
        if not messagebox.askyesno("Очистить логи", "Удалить bot.log?"):
            return
        try:
            if LOG_FILE.exists():
                LOG_FILE.unlink()
            self.log_text.delete("1.0", "end")
            self._say("Логи очищены")
        except Exception as e:
            messagebox.showerror("Ошибка", str(e))

    def destroy(self):
        self._closing = True
        super().destroy()


def _file_has_secret(path: Path) -> bool:
    """Проверяет текстовый файл на типичные секреты."""
    if path.name in SECRET_FREE_FILES or path.suffix.lower() in {
        ".pdf", ".xlsx", ".png", ".jpg", ".jpeg", ".gif", ".exe", ".db", ".zip"
    }:
        return False
    if not path.is_file():
        return False
    try:
        if path.stat().st_size > 3_000_000:
            return False
        text = path.read_text(encoding="utf-8", errors="ignore")
    except Exception:
        return False
    return any(pat.search(text) for pat in SECRET_PATTERNS)


if __name__ == "__main__":
    app = Manager()
    app.mainloop()
