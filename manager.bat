@echo off
rem ============================================================
rem  StudBot Manager - запуск приложения управления ботом
rem ============================================================
setlocal
cd /d "%~dp0"
chcp 65001 >nul

where python >nul 2>nul
if errorlevel 1 (
    py --version >nul 2>nul
    if errorlevel 1 (
        echo.
        echo  Python не найден. Установи с https://www.python.org/downloads/
        echo  При установке отметь галочку "Add Python to PATH".
        echo.
        pause
        exit /b 1
    )
    start "StudBot Manager" py "%~dp0manager.py"
) else (
    start "StudBot Manager" python "%~dp0manager.py"
)
endlocal