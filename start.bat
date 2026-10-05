@echo off
rem ============================================================
rem  StudBot - запуск сервера, бота и публичного туннеля
rem ============================================================
setlocal
cd /d "%~dp0"

echo [1/2] Запускаю StudBot (мини-приложение :8080, панель 127.0.0.1:8081)...
start "StudBot" "%~dp0studbot.exe"

echo [2/2] Запускаю публичный туннель Cloudflare...
start "StudBot tunnel" cmd /k ""C:\Program Files (x86)\cloudflared\cloudflared.exe" tunnel --url http://localhost:8080 --no-autoupdate"

echo.
echo Туннель напечатает адрес вида https://xxxx.trycloudflare.com
echo Если адрес отличается от WEBAPP_URL в .env - впишите новый и перезапустите studbot.exe.
echo Панель администратора: http://127.0.0.1:8081
echo.
timeout /t 20
endlocal
