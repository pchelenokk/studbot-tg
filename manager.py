#!/usr/bin/env python3
"""
StudBot Manager — настольное приложение для запуска и управления ботом.

Возможности:
  * запуск, остановка и перезапуск бота
  * живой просмотр журналов
  * состояние Telegram, туннеля и веб-сервисов
  * работа с Git: коммиты, изменения, отправка на GitHub
  * быстрые переходы: админ-панель, мини-приложение, папки проекта

Тяжёлые операции (проверка сети, git, сборка) выполняются в фоновом потоке,
поэтому интерфейс никогда не подвисает.

Зависимостей нет — только стандартная библиотека Python 3.10+.
"""

from __future__ import annotations

import os
import queue
import re
import shutil
import signal
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
APP_VERSION = "1.1.0"

ROOT = Path(__file__).resolve().parent
EXE = ROOT / "studbot.exe"
BOT_LOG = ROOT / "bot.log"
ENV_FILE = ROOT / ".env"
LOG_DIR = ROOT / "data" / "logs"
LOG_FILE = LOG_DIR / "manager.log"

TELEGRAM_URL = "https://api.telegram.org"

# Паттерны секретов. Намеренно строгие, чтобы плейсхолдеры в README
# и .env.example не считались реальными секретами.
SECRET_PATTERNS = [
    re.compile(r"ghp_[A-Za-z0-9]{30,}"),
    re.compile(r"github_pat_[A-Za-z0-9_]{40,}"),
    re.compile(r"\b\d{8,10}:[A-Za-z0-9_-]{35,}\b"),
    re.compile(r"^\s*ADMIN_PASSWORD\s*=\s*[A-Za-z0-9!@#$%^&*._-]{12,}\s*$", re.M),
]

SECRET_FREE_FILES = {".env.example", "manager.py", ".gitignore", "LICENSE", "manager.bat"}

BG = "#0f151e"
CARD = "#1a2331"
CARD2 = "#212c3c"
LINE = "#2b3749"
TEXT = "#e6edf5"
MUTED = "#8494a8"
ACCENT = "#4c8dff"
GREEN = "#2fd07a"
RED = "#ff5c62"
YELLOW = "#ffc53d"


# --------------------------------------------------------------------------- #
#  Вспомогательные функции
# --------------------------------------------------------------------------- #

def run(cmd, cwd=None, timeout=60):
    """Запускает команду, возвращает (код, stdout, stderr)."""
    try:
        p = subprocess.run(
            [str(c) for c in cmd],
            cwd=str(cwd or ROOT),
            capture_output=True,
            text=True,
            encoding="utf-8",
            errors="replace",
            timeout=timeout,
        )
        return p.returncode, p.stdout.strip(), p.stderr.strip()
    except FileNotFoundError:
        return 127, "", f"не найдено: {cmd[0]}"
    except subprocess.TimeoutExpired:
        return 124, "", "превышено время ожидания"
    except Exception as e:  # noqa: BLE001
        return 1, "", str(e)


def bot_pid() -> int | None:
    """PID процесса studbot.exe либо None."""
    if sys.platform == "win32":
        code, out, _ = run(
            ["tasklist", "/FI", "IMAGENAME eq studbot.exe", "/NH", "/FO", "CSV"],
            timeout=15,
        )
        if code == 0:
            for line in out.splitlines():
                parts = [p.strip().strip('"') for p in line.split(",")]
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


def port_open(port: int, host: str = "127.0.0.1", timeout: float = 0.8) -> bool:
    import socket

    try:
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as s:
            s.settimeout(timeout)
            return s.connect_ex((host, port)) == 0
    except OSError:
        return False


def read_proxy() -> str | None:
    """Прокси из .env, иначе из настроек Windows."""
    if ENV_FILE.exists():
        try:
            for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
                line = line.strip()
                if not line or line.startswith("#"):
                    continue
                if line.upper().startswith("PROXY_URL="):
                    value = line.split("=", 1)[1].strip().strip('"').strip("'")
                    return value or None
        except OSError:
            pass
    if sys.platform == "win32":
        try:
            import winreg

            key = winreg.OpenKey(
                winreg.HKEY_CURRENT_USER,
                r"Software\Microsoft\Windows\CurrentVersion\Internet Settings",
            )
            if winreg.QueryValueEx(key, "ProxyEnable")[0] == 1:
                server = winreg.QueryValueEx(key, "ProxyServer")[0]
                if "=" in server:
                    for part in server.split(";"):
                        k, _, v = part.partition("=")
                        if k in ("https", "http"):
                            return "http://" + v
                elif server:
                    return "http://" + server
        except Exception:  # noqa: BLE001
            pass
    return None


def file_has_secret(path: Path) -> bool:
    """Проверяет текстовый файл на типичные секреты."""
    if path.name in SECRET_FREE_FILES or path.suffix.lower() in {
        ".pdf", ".xlsx", ".png", ".jpg", ".jpeg", ".gif", ".exe", ".db", ".zip",
    }:
        return False
    if not path.is_file():
        return False
    try:
        if path.stat().st_size > 3_000_000:
            return False
        text = path.read_text(encoding="utf-8", errors="ignore")
    except OSError:
        return False
    return any(p.search(text) for p in SECRET_PATTERNS)


# --------------------------------------------------------------------------- #
#  Логирование
# --------------------------------------------------------------------------- #

class AppLogger:
    """Пишет в data/logs/manager.log и отдаёт строки в очередь интерфейса."""

    def __init__(self, ui_queue: queue.Queue):
        self.q = ui_queue
        self._lock = threading.Lock()
        try:
            LOG_DIR.mkdir(parents=True, exist_ok=True)
            LOG_FILE.touch(exist_ok=True)
        except OSError:
            pass

    def write(self, text: str, tag: str = "", to_ui: bool = True):
        stamp = datetime.now().strftime("%H:%M:%S")
        line = f"{stamp} {text}"
        with self._lock:
            try:
                with LOG_FILE.open("a", encoding="utf-8") as f:
                    f.write(line + "\n")
            except OSError:
                pass
        if to_ui:
            self.q.put((line, tag))

    def tail(self, count: int = 200) -> list[str]:
        try:
            lines = LOG_FILE.read_text(encoding="utf-8", errors="replace").splitlines()
            return lines[-count:]
        except OSError:
            return []

    def rotate_if_big(self, limit: int = 2_000_000):
        """Не даёт логу расти бесконечно."""
        try:
            if LOG_FILE.exists() and LOG_FILE.stat().st_size > limit:
                backup = LOG_DIR / "manager.prev.log"
                if backup.exists():
                    backup.unlink()
                LOG_FILE.replace(backup)
                LOG_FILE.touch()
                self.write("лог переполнен — предыдущий сохранён как manager.prev.log", "warn")
        except OSError:
            pass


class BotLogWatcher(threading.Thread):
    """Следит за bot.log и отдаёт новые строки в очередь."""

    def __init__(self, out: queue.Queue):
        super().__init__(daemon=True)
        self.out = out
        self._stop = threading.Event()

    def stop(self):
        self._stop.set()

    def run(self):
        pos = 0
        while not self._stop.is_set():
            try:
                if BOT_LOG.exists():
                    size = BOT_LOG.stat().st_size
                    if size < pos:
                        pos = 0
                    with BOT_LOG.open("r", encoding="utf-8", errors="replace") as f:
                        f.seek(pos)
                        for line in f:
                            self.out.put(line.rstrip())
                        pos = f.tell()
            except OSError:
                pass
            self._stop.wait(0.6)


# --------------------------------------------------------------------------- #
#  Тяжёлые операции (выполняются в фоновом потоке)
# --------------------------------------------------------------------------- #

def job_telegram() -> tuple[bool, str]:
    proxy = read_proxy()
    if proxy and curl_available():
        code, out, _ = run(
            ["curl", "-s", "-o", "NUL", "-w", "%{http_code}",
             "--proxy", proxy, "--max-time", "5", TELEGRAM_URL],
            timeout=9,
        )
        if code == 0 and out[:3] in ("200", "302", "401"):
            return True, f"через прокси {proxy}"
    if curl_available():
        code, out, _ = run(
            ["curl", "-s", "-o", "NUL", "-w", "%{http_code}",
             "--noproxy", "*", "--max-time", "5", TELEGRAM_URL],
            timeout=9,
        )
        if code == 0 and out[:3] in ("200", "302", "401"):
            return True, "напрямую"
    if proxy:
        return False, "прокси не отвечает"
    return False, "нет маршрута, включи VPN"


def curl_available() -> bool:
    if sys.platform == "win32":
        return shutil.which("curl.exe") is not None or Path(
            r"C:\Windows\System32\curl.exe"
        ).exists()
    return shutil.which("curl") is not None


def job_status() -> list[tuple[str, str, str]]:
    """Возвращает строки состояния."""
    pid = bot_pid()
    rows: list[tuple[str, str, str]] = []
    rows.append((
        "Процесс",
        f"работает, PID {pid}" if pid else "остановлен",
        "ok" if pid else "bad",
    ))
    rows.append((
        "Сборка",
        "есть" if EXE.exists() else "нет — нажми «Пересобрать»",
        "ok" if EXE.exists() else "warn",
    ))
    proxy = read_proxy()
    rows.append(("Прокси", proxy or "не задан", "ok" if proxy else "warn"))
    up = port_open(8080)
    panel = port_open(8081)
    rows.append(("Мини-приложение :8080", "отвечает" if up else "молчит",
                 "ok" if up else "bad"))
    rows.append(("Админ-панель :8081", "отвечает" if panel else "молчит",
                 "ok" if panel else "bad"))
    rows.append((
        "Журнал бота",
        "bot.log" if BOT_LOG.exists() else "нет файла",
        "ok" if BOT_LOG.exists() else "warn",
    ))
    return rows


def job_git() -> dict:
    """Собирает всю информацию о git."""
    if not (ROOT / ".git").exists():
        return {"ok": False}

    out: dict = {"ok": True}

    code, branch, _ = run(["git", "rev-parse", "--abbrev-ref", "HEAD"], timeout=20)
    out["branch"] = branch or "?"

    code, remote, _ = run(["git", "config", "--get", "remote.origin.url"], timeout=20)
    out["remote"] = remote.rstrip("/").removesuffix(".git") if code == 0 else ""

    code, log_out, _ = run(
        ["git", "log", "--pretty=format:%h|%ad|%s", "--date=format:%d.%m %H:%M", "-25"],
        timeout=30,
    )
    commits = []
    for line in log_out.splitlines():
        parts = line.split("|", 2)
        if len(parts) == 3:
            commits.append(parts)
    out["commits"] = commits

    code, status_out, _ = run(["git", "status", "--porcelain"], timeout=30)
    changes = []
    secrets = []
    for line in status_out.splitlines():
        if len(line) < 4:
            continue
        # Формат: XY<пробел>путь — нельзя резать по индексу, путь может
        # содержать пробелы, а сдвиг индекса съедал первый символ.
        parts = line.split(maxsplit=1)
        if len(parts) < 2:
            continue
        st = (line[:2].strip() or "?")[0]
        path = parts[1].strip()
        tag = {"M": "mod", "A": "add", "R": "mod", "D": "del",
               "C": "mod", "??": "add", "!": "mod"}.get(st, "mod")
        if path.rsplit("/", 1)[-1] == ".env":
            secrets.append(path)
            changes.append((st, path, "secret"))
            continue
        if file_has_secret(ROOT / path):
            secrets.append(path)
            changes.append((st, path, "secret"))
            continue
        changes.append((st, path, tag))
    out["changes"] = changes
    out["secrets"] = secrets

    code, counts, _ = run(
        ["git", "rev-list", "--left-right", "--count", "@{upstream}...HEAD"], timeout=25
    )
    if code == 0 and counts:
        bits = counts.split()
        if len(bits) == 2:
            out["ahead"], out["behind"] = bits
    return out


# --------------------------------------------------------------------------- #
#  Интерфейс
# --------------------------------------------------------------------------- #

class Manager(tk.Tk):
    POLL_MS = 120

    def __init__(self):
        super().__init__()
        self.title(f"{APP_TITLE} {APP_VERSION}")
        self.geometry("1100x740")
        self.minsize(920, 620)
        self.configure(bg=BG)

        self.log_q: queue.Queue = queue.Queue()
        self.job_q: queue.Queue = queue.Queue()
        self.res_q: queue.Queue = queue.Queue()

        self.logger = AppLogger(self.log_q)
        self.logger.rotate_if_big()

        self.proc = None
        self.proc_started = 0.0
        self.pending_secrets: list[str] = []
        self._status_due = 0.0
        self._git_due = 0.0
        self._closing = False

        self._setup_style()
        self._build_ui()

        threading.Thread(target=self._worker, daemon=True).start()
        BotLogWatcher(self.log_q).start()

        self.after(self.POLL_MS, self._pump)
        self.after(300, self._boot)

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

        s.configure("TNotebook", background=BG, bordercolor=LINE, tabmargins=(0, 6, 0, 0))
        s.configure("TNotebook.Tab", background=CARD2, foreground=MUTED,
                    padding=(18, 9), font=("Segoe UI", 10, "bold"))
        s.map("TNotebook.Tab",
              background=[("selected", ACCENT)],
              foreground=[("selected", "#ffffff")])

        s.configure("Accent.TButton", background=ACCENT, foreground="#ffffff",
                    font=("Segoe UI", 10, "bold"), padding=(16, 10), borderwidth=0)
        s.map("Accent.TButton", background=[("active", "#3b7ae8"), ("disabled", "#33465e")])
        s.configure("Ghost.TButton", background=CARD2, foreground=TEXT,
                    font=("Segoe UI", 10), padding=(14, 9), bordercolor=LINE)
        s.map("Ghost.TButton", background=[("active", "#2a3749")])
        s.configure("Danger.TButton", background=RED, foreground="#ffffff",
                    font=("Segoe UI", 10, "bold"), padding=(14, 9), borderwidth=0)
        s.map("Danger.TButton", background=[("active", "#d63f44")])
        s.configure("Green.TButton", background=GREEN, foreground="#04210f",
                    font=("Segoe UI", 10, "bold"), padding=(14, 9), borderwidth=0)
        s.map("Green.TButton", background=[("active", "#26b869")])

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
        top = ttk.Frame(self, padding=(14, 12))
        top.pack(fill="x")
        ttk.Label(top, text="\U0001F916  StudBot", font=("Segoe UI", 17, "bold")).pack(side="left")
        self.lbl_badge = tk.Label(top, text="● проверка…", bg=CARD, fg=YELLOW,
                                  font=("Segoe UI", 10, "bold"), padx=14, pady=7)
        self.lbl_badge.pack(side="left", padx=(16, 0))
        self.lbl_right = tk.Label(top, text="", bg=BG, fg=MUTED, font=("Segoe UI", 9))
        self.lbl_right.pack(side="right")

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

        controls = self._card(left, "Управление ботом", "запуск, остановка, пересборка")
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
        ttk.Button(row2, text="\U0001F310  Админ-панель", style="Ghost.TButton",
                   command=lambda: self._open_url("http://127.0.0.1:8081")).pack(side="left", padx=(0, 8))
        ttk.Button(row2, text="\U0001F4F1  Мини-приложение", style="Ghost.TButton",
                   command=lambda: self._open_url("http://localhost:8080")).pack(side="left")

        status = self._card(left, "Состояние", "обновляется каждые 5 секунд")
        self.status_tree = ttk.Treeview(
            status, columns=("param", "value"), show="headings", height=7
        )
        self.status_tree.heading("param", text="Параметр")
        self.status_tree.heading("value", text="Значение")
        self.status_tree.column("param", width=200, anchor="w")
        self.status_tree.column("value", width=400, anchor="w")
        self.status_tree.pack(fill="x")
        for name, tag, color in (("ok", "ok", GREEN), ("bad", "bad", RED), ("warn", "warn", YELLOW)):
            self.status_tree.tag_configure(name, foreground=color)

        logs = self._card(left, "Журнал",
                          "bot.log — события бота; действия менеджера пишутся в data/logs")
        wrap = ttk.Frame(logs, style="Card.TFrame")
        wrap.pack(fill="both", expand=True)
        self.log_text = tk.Text(
            wrap, height=14, bg=CARD2, fg="#c9d6e5", insertbackground=TEXT,
            relief="flat", font=("Consolas", 9), wrap="none", state="disabled",
        )
        sb = ttk.Scrollbar(wrap, orient="vertical", command=self.log_text.yview)
        self.log_text.configure(yscrollcommand=sb.set)
        self.log_text.pack(side="left", fill="both", expand=True)
        sb.pack(side="right", fill="y")
        for name, color in (("err", RED), ("warn", YELLOW), ("ok", GREEN), ("mgr", ACCENT)):
            self.log_text.tag_configure(name, foreground=color)

    def _build_git(self):
        head = self._card(self.tab_git, "Git", "изменения, коммиты и отправка на GitHub")
        row = ttk.Frame(head, style="Card.TFrame")
        row.pack(fill="x")
        self.lbl_git_branch = tk.Label(row, text="загрузка…", bg=CARD, fg=ACCENT,
                                       font=("Segoe UI", 10, "bold"))
        self.lbl_git_branch.pack(side="left", padx=(0, 16))
        self.lbl_git_sync = tk.Label(row, text="", bg=CARD, fg=MUTED, font=("Segoe UI", 9))
        self.lbl_git_sync.pack(side="left")
        ttk.Button(row, text="⟳  Обновить", style="Ghost.TButton",
                   command=lambda: self._submit(job_git, self._apply_git)).pack(side="right")

        mid = ttk.Frame(self.tab_git)
        mid.pack(fill="both", expand=True, pady=(10, 0))

        left = ttk.Frame(mid)
        left.pack(side="left", fill="both", expand=True, padx=(0, 10))

        changes = self._card(left, "Изменённые файлы", "добавятся в коммит")
        self.changes_tree = ttk.Treeview(
            changes, columns=("st", "path"), show="headings", height=8
        )
        self.changes_tree.heading("st", text="")
        self.changes_tree.heading("path", text="Файл")
        self.changes_tree.column("st", width=34, anchor="center")
        self.changes_tree.column("path", width=440, anchor="w")
        self.changes_tree.pack(fill="both", expand=True)
        self.changes_tree.tag_configure("add", foreground=GREEN)
        self.changes_tree.tag_configure("mod", foreground=YELLOW)
        self.changes_tree.tag_configure("del", foreground=RED)
        self.changes_tree.tag_configure("secret", foreground="#ffffff", background="#8e2a2f")

        commit = self._card(left, "Новый коммит", "кратко опиши, что изменилось")
        self.msg_text = tk.Text(
            commit, height=3, bg=CARD2, fg=TEXT, insertbackground=TEXT,
            relief="flat", font=("Segoe UI", 10), wrap="word", padx=10, pady=8,
        )
        self.msg_text.pack(fill="x")

        brow = ttk.Frame(commit, style="Card.TFrame")
        brow.pack(fill="x", pady=(10, 0))
        self.btn_commit = ttk.Button(brow, text="\U0001F4BE  Только коммит",
                                     style="Ghost.TButton",
                                     command=lambda: self.do_commit(push=False))
        self.btn_commit.pack(side="left", padx=(0, 8))
        self.btn_commit_push = ttk.Button(brow, text="\U0001F680  Коммит и отправить",
                                           style="Accent.TButton",
                                           command=lambda: self.do_commit(push=True))
        self.btn_commit_push.pack(side="left")

        right = ttk.Frame(mid)
        right.pack(side="right", fill="both", expand=True)
        hist = self._card(right, "История", "последние 25 коммитов")
        self.log_tree = ttk.Treeview(
            hist, columns=("sha", "date", "msg"), show="headings", height=22
        )
        self.log_tree.heading("sha", text="Хэш")
        self.log_tree.heading("date", text="Когда")
        self.log_tree.heading("msg", text="Сообщение")
        self.log_tree.column("sha", width=66, anchor="w")
        self.log_tree.column("date", width=112, anchor="w")
        self.log_tree.column("msg", width=240, anchor="w")
        self.log_tree.pack(fill="both", expand=True)

    def _build_tools(self):
        card = self._card(self.tab_tools, "Инструменты", "быстрые действия")
        items = [
            ("\U0001F4CA  Выгрузки Excel", "папка с таблицами посещаемости", self.open_exports),
            ("\U0001F4DA  Загрузить книгу", "выбрать файл и открыть панель", self.upload_book),
            ("\U0001F511  Показать пароль панели", "прочитать ADMIN_PASSWORD", self.show_password),
            ("\U0001F5C2  Папка проекта", "файлы и база данных", lambda: self._open_path(ROOT)),
            ("\U0001F4C1  Папка data", "база, загрузки, логи", lambda: self._open_path(ROOT / "data")),
            ("\U0001F5D3  Открыть свой лог", "data/logs/manager.log", self.open_log_dir),
            ("\U0001F9F9  Очистить логи", "удалить bot.log и manager.log", self.clear_logs),
        ]
        for title, sub, cmd in items:
            row = ttk.Frame(card, style="Card.TFrame")
            row.pack(fill="x", pady=(0, 8))
            ttk.Label(row, text=title, style="CardTitle.TLabel").pack(side="left")
            ttk.Button(row, text="Открыть", style="Ghost.TButton", command=cmd).pack(side="right")
            ttk.Label(row, text=sub, style="CardMuted.TLabel").pack(side="left", padx=(14, 0))

        info = self._card(self.tab_tools, "О проекте")
        ttk.Label(
            info,
            text=(
                f"StudBot Manager {APP_VERSION}\n\n"
                "Telegram-бот для староста группы: библиотека с файлами,\n"
                "домашка по предметам, перекличка через голосование с\n"
                "выгрузкой в Excel по неделям, опросы, события, роли,\n"
                "мини-приложение и админ-панель."
            ),
            style="CardMuted.TLabel", justify="left",
        ).pack(anchor="w")

        btns = ttk.Frame(self.tab_tools, style="Card.TFrame")
        btns.pack(fill="x", pady=(4, 0))
        ttk.Button(
            btns, text="\U0001F419  Репозиторий на GitHub", style="Ghost.TButton",
            command=lambda: self._open_url("https://github.com/pchelenokk/studbot-tg"),
        ).pack(anchor="w")

    # ---------- фоновая обработка ----------

    def _submit(self, func, callback, *args):
        """Кладёт задачу в фоновый поток, результат вернётся в UI-поток."""
        self.job_q.put((func, args, callback))

    def _worker(self):
        while not self._closing:
            try:
                func, args, callback = self.job_q.get(timeout=0.3)
            except queue.Empty:
                continue
            try:
                result = func(*args)
                self.res_q.put((callback, result, None))
            except Exception as e:  # noqa: BLE001
                self.res_q.put((callback, None, str(e)))

    def _pump(self):
        """Разбирает очереди в UI-потоке и планирует периодические задачи."""
        # логи
        while True:
            try:
                line, tag = self.log_q.get_nowait()
            except queue.Empty:
                break
            self._append_log(line, tag)

        # результаты фоновых задач
        while True:
            try:
                callback, result, err = self.res_q.get_nowait()
            except queue.Empty:
                break
            if err:
                self.logger.write(f"ошибка фоновой задачи: {err}", "err")
                continue
            if callback:
                callback(result)

        # периодика
        now = time.monotonic()
        if now >= self._status_due:
            self._status_due = now + 5
            self._submit(self._status_bundle, self._apply_status)
            self._set_running_ui(bot_pid() is not None)

        if self.nb.index("current") == 1 and now >= self._git_due:
            self._git_due = now + 5
            self._submit(job_git, self._apply_git)

        if self.after_id:
            try:
                self.after_cancel(self.after_id)
            except tk.TclError:
                pass
        self.after_id = self.after(self.POLL_MS, self._pump)

    after_id = None

    def _status_bundle(self) -> dict:
        rows = job_status()
        rows.append(("Telegram",) + job_telegram())
        return {"rows": rows, "pid": bot_pid()}

    def _apply_status(self, data):
        pid = data.get("pid")
        rows = data.get("rows", [])
        self._set_running_ui(pid is not None)

        self.status_tree.delete(*self.status_tree.get_children())
        for name, value, tag in rows:
            self.status_tree.insert("", "end", values=(name, value), tags=(tag,))

        if pid and self.proc_started:
            self.lbl_right.configure(text=f"работает {int(time.time() - self.proc_started)} с")
        elif not EXE.exists():
            self.lbl_right.configure(text="сборка не найдена")

        # В журнал пишем только когда состояние реально изменилось
        snapshot = {name: value for name, value, _ in rows}
        if snapshot != self._last_status_snapshot:
            if self._last_status_snapshot:
                for name, value, tag in rows:
                    was = self._last_status_snapshot.get(name)
                    if was and was != value:
                        level = "ok" if tag == "ok" else ("err" if tag == "bad" else "warn")
                        self.logger.write(f"{name}: {was} â†’ {value}", level)
            self._last_status_snapshot = snapshot

    _last_status_snapshot: dict = {}

    def _set_running_ui(self, running: bool):
        self.btn_start.configure(state="disabled" if running else "normal")
        self.btn_stop.configure(state="normal" if running else "disabled")
        if running:
            if self.lbl_badge.cget("fg") != GREEN:
                self.lbl_badge.configure(text="● работает", fg=GREEN)
        elif self.lbl_badge.cget("fg") != RED:
            self.lbl_badge.configure(text="● остановлен", fg=RED)

    def _boot(self):
        self.logger.write(f"менеджер запущен (v{APP_VERSION})", "mgr")
        self.logger.write(f"лог пишется в {LOG_FILE}", "mgr")
        for line in self.logger.tail(60):
            self.log_q.put((line, ""))
        if BOT_LOG.exists():
            try:
                tail = BOT_LOG.read_text(encoding="utf-8", errors="replace").splitlines()[-40:]
                for line in tail:
                    self.log_q.put((line, ""))
            except OSError:
                pass
        self._submit(job_git, self._apply_git)
        self._git_due = time.monotonic() + 5

    # ---------- журнал ----------

    def _append_log(self, line: str, tag: str = ""):
        if not tag:
            low = line.lower()
            if "error" in low or "offline" in low or "failed" in low or "panic" in low:
                tag = "err"
            elif "warn" in low or "retrying" in low:
                tag = "warn"
            elif "online" in low or "started" in low or "запущен" in low:
                tag = "ok"
            elif "менеджер" in low or "[менеджер]" in low:
                tag = "mgr"
        self.log_text.configure(state="normal")
        self.log_text.insert("end", line + "\n", tag)
        lines = int(self.log_text.index("end-1c").split(".")[0])
        if lines > 1500:
            self.log_text.delete("1.0", "400.0")
        self.log_text.see("end")
        self.log_text.configure(state="disabled")

    # ---------- бот ----------

    def start_bot(self):
        if bot_pid():
            self.logger.write("бот уже запущен", "warn")
            return
        if not EXE.exists():
            if not messagebox.askyesno("Нет сборки",
                                       "studbot.exe не найден. Собрать сейчас?"):
                return
            self._submit(lambda: self._build_sync(), self._after_build_start)
            return
        try:
            flags = 0x08000000 if sys.platform == "win32" else 0
            self.proc = subprocess.Popen(
                [str(EXE)], cwd=str(ROOT),
                stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                creationflags=flags,
            )
            self.proc_started = time.time()
            self.logger.write("бот запускается", "ok")
        except Exception as e:  # noqa: BLE001
            self.logger.write(f"не удалось запустить: {e}", "err")

    def stop_bot(self):
        if sys.platform == "win32":
            code, _, err = run(["taskkill", "/F", "/IM", "studbot.exe"], timeout=20)
            ok = code == 0
        else:
            pid = bot_pid()
            ok = True
            if pid:
                try:
                    os.kill(pid, signal.SIGTERM)
                except OSError as e:
                    ok = False
                    err = str(e)
        self.logger.write("бот остановлен" if ok else f"не удалось остановить: {err}",
                          "ok" if ok else "err")
        self.proc = None
        self.proc_started = 0.0

    def restart_bot(self):
        self.stop_bot()
        self.after(1200, self.start_bot)

    def rebuild_bot(self):
        if not messagebox.askyesno("Пересборка",
                                   "Остановить бот и пересобрать studbot.exe?"):
            return
        self.stop_bot()
        self._submit(lambda: self._build_sync(), self._after_build)

    def _build_sync(self) -> bool:
        if shutil.which("go") is None:
            return False
        self.logger.write("запущена сборка…", "mgr")
        code, _, err = run(["go", "build", "-o", "studbot.exe", "./cmd/bot"], timeout=300)
        return code == 0

    def _after_build(self, ok):
        if ok:
            self.logger.write("сборка успешна", "ok")
        else:
            self.logger.write("сборка не удалась — нужен установленный Go", "err")
            messagebox.showerror("Сборка",
                                 "Не удалось собрать. Проверь, что Go установлен и доступен в PATH.")

    def _after_build_start(self, ok):
        self._after_build(ok)
        if ok:
            self.start_bot()

    # ---------- git ----------

    def _apply_git(self, data):
        if not data.get("ok"):
            self.lbl_git_branch.configure(text="не git-репозиторий", fg=RED)
            return

        self.lbl_git_branch.configure(text=f"ветка: {data.get('branch', '?')}", fg=ACCENT)

        sync = "нет данных об upstream"
        color = MUTED
        if "ahead" in data:
            ahead, behind = data.get("ahead", "0"), data.get("behind", "0")
            if ahead != "0" or behind != "0":
                sync = f"вперёд {ahead} · назад {behind}"
                color = YELLOW
            else:
                sync = "синхронизировано с GitHub"
                color = GREEN
        remote = data.get("remote", "")
        self.lbl_git_sync.configure(text=f"{sync}  ·  {remote or 'remote не задан'}", fg=color)

        self.log_tree.delete(*self.log_tree.get_children())
        for sha, date, msg in data.get("commits", []):
            self.log_tree.insert("", "end", values=(sha, date, msg))

        self.changes_tree.delete(*self.changes_tree.get_children())
        for st, path, tag in data.get("changes", []):
            self.changes_tree.insert("", "end", values=(st, path), tags=(tag,))

        self.pending_secrets = data.get("secrets", [])
        if self.pending_secrets:
            self.lbl_git_sync.configure(
                text=f"⚠ секреты в файлах: {len(self.pending_secrets)} — коммит заблокирован",
                fg=RED,
            )
            self.logger.write(
                f"коммит заблокирован: секреты в {len(self.pending_secrets)} файлах", "err"
            )

        # Логируем только когда список изменений изменился
        changes = data.get("changes", [])
        names = sorted(p for _, p, _ in changes)
        if names != self._last_changes:
            if names:
                self.logger.write(f"изменённых файлов: {len(names)}", "mgr")
            elif self._last_changes:
                self.logger.write("рабочая папка чистая", "ok")
            self._last_changes = names

    _last_changes: list = []

    def do_commit(self, push: bool):
        if not (ROOT / ".git").exists():
            messagebox.showinfo("Git", "Папка не является git-репозиторием")
            return

        message = self.msg_text.get("1.0", "end").strip()
        if not message:
            messagebox.showwarning("Нет сообщения",
                                   "Опиши изменения — это сообщение увидят другие.")
            return
        message = message[:200]

        if self.pending_secrets:
            messagebox.showerror(
                "Нельзя закоммитить",
                "В изменённых файлах похожи на секреты:\n\n  "
                + "\n  ".join(self.pending_secrets[:10])
                + "\n\nУбери их из изменений или добавь в .gitignore.",
            )
            self.logger.write(f"коммит заблокирован: секреты в {self.pending_secrets}", "err")
            return

        self._set_commit_enabled(False)
        self.logger.write(f"коммит: {message[:70]}", "mgr")
        self._submit(lambda: self._commit_sync(message, push), self._after_commit)

    def _commit_sync(self, message: str, push: bool) -> dict:
        code, _, err = run(["git", "add", "-A"], timeout=120)
        if code != 0:
            return {"stage": "error", "err": err}

        code, out, err = run(["git", "commit", "-m", message], timeout=120)
        if code != 0:
            if "nothing to commit" in (err + out).lower():
                return {"stage": "empty"}
            return {"stage": "error", "err": err}

        result = {"stage": "ok"}
        if push:
            pcode, pout, perr = run(["git", "push", "origin", "HEAD"], timeout=180)
            result["pushed"] = pcode == 0
            if pcode != 0:
                result["perr"] = perr or pout
        return result

    def _after_commit(self, result):
        self._set_commit_enabled(True)
        stage = result.get("stage")

        if stage == "empty":
            self.logger.write("изменений не осталось", "warn")
            messagebox.showinfo("Нечего коммитить", "Рабочая папка не изменена.")
            return
        if stage == "error":
            self.logger.write(f"ошибка коммита: {result.get('err')}", "err")
            messagebox.showerror("Ошибка коммита", str(result.get("err"))[:500])
            return

        self.msg_text.delete("1.0", "end")
        self.logger.write("коммит создан", "ok")

        if result.get("pushed"):
            self.logger.write("отправлено на GitHub", "ok")
            messagebox.showinfo("Готово", "Изменения отправлены на GitHub.")
        elif result.get("pushed") is False:
            err = str(result.get("perr", ""))
            hint = ""
            if "could not read Username" in err or "Authentication failed" in err or "terminal prompts" in err:
                hint = (
                    "\n\nGit не смог запросить логин и пароль — для оконных приложений это "
                    "обычное дело.\n\nОдин раз выполни в обычной командной строке:\n"
                    "    gh auth login\n\nили отправь вручную:\n"
                    "    git push origin main"
                )
            self.logger.write("push не удался", "err")
            messagebox.showerror("Не отправлено", err[:400] + hint)

        self._submit(job_git, self._apply_git)
        self._git_due = time.monotonic() + 5

    def _set_commit_enabled(self, enabled: bool):
        state = "normal" if enabled else "disabled"
        self.btn_commit.configure(state=state)
        self.btn_commit_push.configure(state=state)

    # ---------- прочее ----------

    def _open_url(self, url: str):
        self.logger.write(f"открываю {url}", "mgr")
        webbrowser.open(url)

    def _open_path(self, path: Path):
        path = Path(path)
        if not path.exists():
            messagebox.showinfo("Не найдено", str(path))
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
            self._open_path(d)
            return
        self._open_path(d)
        if len(files) == 1 and messagebox.askyesno(
            "Открыть файл", f"Открыть {files[0].name}?"
        ):
            webbrowser.open(files[0].as_uri())

    def upload_book(self):
        path = filedialog.askopenfilename(
            title="Выбери файл книги",
            filetypes=[("Документы", "*.pdf *.docx *.doc *.txt *.epub"),
                       ("Все файлы", "*.*")],
        )
        if not path:
            return
        self.logger.write(f"выбран файл книги: {Path(path).name}", "mgr")
        self._open_url("http://127.0.0.1:8081")

    def show_password(self):
        if not ENV_FILE.exists():
            messagebox.showinfo("Нет файла", ".env не найден")
            return
        try:
            for line in ENV_FILE.read_text(encoding="utf-8", errors="replace").splitlines():
                if line.strip().upper().startswith("ADMIN_PASSWORD="):
                    value = line.split("=", 1)[1].strip().strip('"').strip("'")
                    messagebox.showinfo("Пароль админ-панели", value or "(пусто)")
                    return
        except OSError:
            pass
        messagebox.showinfo("Пароль", "ADMIN_PASSWORD не задан в .env")

    def open_log_dir(self):
        LOG_DIR.mkdir(parents=True, exist_ok=True)
        self._open_path(LOG_DIR)

    def clear_logs(self):
        if not messagebox.askyesno("Очистить логи", "Удалить bot.log и manager.log?"):
            return
        for f in (BOT_LOG, LOG_FILE, LOG_DIR / "manager.prev.log"):
            try:
                if f.exists():
                    f.unlink()
            except OSError as e:
                self.logger.write(f"не удалось удалить {f.name}: {e}", "err")
        self.log_text.configure(state="normal")
        self.log_text.delete("1.0", "end")
        self.log_text.configure(state="disabled")
        self.logger.write("логи очищены", "ok")

    def destroy(self):
        self._closing = True
        self.logger.write("менеджер закрыт", "mgr")
        super().destroy()


if __name__ == "__main__":
    app = Manager()
    app.mainloop()
