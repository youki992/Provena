@echo off
setlocal
cd /d "%~dp0"

if not exist "Provena.exe" (
  echo [ERROR] Provena.exe was not found. Please extract the complete release package first.
  pause
  exit /b 1
)

set "PROVENA_NODE=%CD%\runtime\node\node.exe"
set "PROVENA_PI_CLI=%CD%\runtime\pi\node_modules\@mariozechner\pi-coding-agent\dist\cli.js"
if not exist "%PROVENA_NODE%" goto :missing_node
if not exist "%PROVENA_PI_CLI%" goto :missing_pi

"%PROVENA_NODE%" "%PROVENA_PI_CLI%" --version >nul 2>nul || goto :missing_pi

echo.
echo   Provena is starting...
echo   Open http://127.0.0.1:8080 after the service is ready.
echo   On the first run, config.yaml and data will be created automatically.
echo.

Provena.exe serve --http -config "%CD%\config.yaml"
set "EXIT_CODE=%ERRORLEVEL%"
echo.
echo Service stopped with exit code %EXIT_CODE%.
pause
exit /b %EXIT_CODE%

:missing_node
echo.
echo [ERROR] The bundled Node.js runtime is missing.
echo Re-extract the complete Provena package.
pause
exit /b 1

:missing_pi
echo.
echo [ERROR] The bundled Pi Agent runtime is missing or damaged.
echo Re-extract the complete Provena package.
pause
exit /b 1
