@echo off
rem ============================================================
rem  StudBot - запуск бота, мини-приложения и туннеля
rem ============================================================
setlocal
cd /d "%~dp0"
chcp 65001 >nul

echo.
echo  [1/2] Запускаю StudBot...
echo         бот      : Telegram
echo         мини-апп : http://localhost:8080
echo         панель   : http://127.0.0.1:8081
echo.
if not exist "studbot.exe" (
    echo  Собираю studbot.exe...
    go build -o studbot.exe ./cmd/bot
    if errorlevel 1 (
        echo  ОШИБКА: не удалось собрать. Проверь, что Go установлен.
        pause
        exit /b 1
    )
)
start "StudBot" "%~dp0studbot.exe"

echo  [2/2] Запускаю туннель Cloudflare...
set CLOUDFLARED=
where cloudflared >nul 2>nul && set CLOUDFLARED=cloudflared
if not defined CLOUDFLARED if exist "C:\Program Files (x86)\cloudflared\cloudflared.exe" set CLOUDFLARED=C:\Program Files (x86)\cloudflared\cloudflared.exe

if defined CLOUDFLARED (
    start "StudBot tunnel" cmd /k ""%CLOUDFLARED%" tunnel --url http://localhost:8080 --no-autoupdate"
) else (
    echo         cloudflared не найден. Установи: winget install GitHub.cloudflare.cloudflared
)

echo.
echo  -------------------------------------------------------
echo  Бот запущен. Адрес туннеля появится в окне "StudBot tunnel".
echo  Скопируй его в WEBAPP_URL в файле .env и перезапусти бота.
echo  Панель управления: http://127.0.0.1:8081
echo  -------------------------------------------------------
echo.
timeout /t 25 >nul
endlocal