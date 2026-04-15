@echo off
chcp 65001 >nul
setlocal enabledelayedexpansion

:menu
cls
echo ========================================
echo   短视频系统 - 服务管理
echo ========================================
echo.
echo 当前服务状态：
echo.

REM 检查 Backend API
curl -s -o nul -w "%%{http_code}" http://localhost:8080/metrics >nul 2>&1
if %errorlevel% equ 0 (
    echo [✓] Backend API      : 运行中 (http://localhost:8080)
) else (
    echo [✗] Backend API      : 未运行
)

REM 检查 Worker
curl -s -o nul -w "%%{http_code}" http://localhost:8081/metrics >nul 2>&1
if %errorlevel% equ 0 (
    echo [✓] Worker           : 运行中 (http://localhost:8081)
) else (
    echo [✗] Worker           : 未运行
)

REM 检查 Prometheus
curl -s http://localhost:9090/-/healthy >nul 2>&1
if %errorlevel% equ 0 (
    echo [✓] Prometheus       : 运行中 (http://localhost:9090)
) else (
    echo [✗] Prometheus       : 未运行
)

REM 检查 Grafana
curl -s http://localhost:3000/api/health >nul 2>&1
if %errorlevel% equ 0 (
    echo [✓] Grafana          : 运行中 (http://localhost:3000)
) else (
    echo [✗] Grafana          : 未运行
)

echo.
echo ========================================
echo.
echo 操作选项：
echo   1. 启动所有服务
echo   2. 停止所有服务
echo   3. 重启所有服务
echo   4. 查看服务日志
echo   5. 测试 Metrics 端点
echo   0. 退出
echo.
set /p choice=请选择操作 (0-5):

if "%choice%"=="1" goto start_all
if "%choice%"=="2" goto stop_all
if "%choice%"=="3" goto restart_all
if "%choice%"=="4" goto view_logs
if "%choice%"=="5" goto test_metrics
if "%choice%"=="0" goto end
goto menu

:start_all
cls
echo ========================================
echo   启动所有服务
echo ========================================
echo.

echo [1/4] 启动基础服务 (MySQL, Redis, RabbitMQ)...
docker compose up -d mysql redis rabbitmq
timeout /t 3 /nobreak >nul

echo [2/4] 启动 Backend API...
start "Backend API" cmd /k "cd /d %~dp0backend && go run ./cmd"
timeout /t 3 /nobreak >nul

echo [3/4] 启动 Worker...
start "Worker" cmd /k "cd /d %~dp0backend && go run ./cmd/worker"
timeout /t 3 /nobreak >nul

echo [4/4] 启动 Prometheus 和 Grafana...
docker compose up -d prometheus grafana
timeout /t 3 /nobreak >nul

echo.
echo ✓ 所有服务已启动！
echo.
pause
goto menu

:stop_all
cls
echo ========================================
echo   停止所有服务
echo ========================================
echo.

echo [1/3] 停止 Backend API 和 Worker...
taskkill /FI "WINDOWTITLE eq Backend API*" /F >nul 2>&1
taskkill /FI "WINDOWTITLE eq Worker*" /F >nul 2>&1

echo [2/3] 停止 Prometheus 和 Grafana...
docker compose stop prometheus grafana

echo [3/3] 停止基础服务...
docker compose stop mysql redis rabbitmq

echo.
echo ✓ 所有服务已停止！
echo.
pause
goto menu

:restart_all
cls
echo ========================================
echo   重启所有服务
echo ========================================
echo.
call :stop_all
timeout /t 2 /nobreak >nul
call :start_all
goto menu

:view_logs
cls
echo ========================================
echo   查看服务日志
echo ========================================
echo.
echo 1. Backend API 日志
echo 2. Worker 日志
echo 3. Prometheus 日志
echo 4. Grafana 日志
echo 0. 返回主菜单
echo.
set /p log_choice=请选择 (0-4):

if "%log_choice%"=="1" docker compose logs backend --tail 50
if "%log_choice%"=="2" docker compose logs worker --tail 50
if "%log_choice%"=="3" docker compose logs prometheus --tail 50
if "%log_choice%"=="4" docker compose logs grafana --tail 50
if "%log_choice%"=="0" goto menu

echo.
pause
goto view_logs

:test_metrics
cls
echo ========================================
echo   测试 Metrics 端点
echo ========================================
echo.

echo 1. Backend API Metrics (http://localhost:8080/metrics)
curl -s http://localhost:8080/metrics | findstr /C:"http_requests_total" /C:"cache_hits_total" /C:"video_publish_total"
echo.

echo 2. Worker Metrics (http://localhost:8081/metrics)
curl -s http://localhost:8081/metrics | findstr /C:"mq_consume_total" /C:"inbox_fanout_total" /C:"big_v_skip_count"
echo.

echo 3. Prometheus Health
curl -s http://localhost:9090/-/healthy
echo.

echo 4. Grafana Health
curl -s http://localhost:3000/api/health
echo.

pause
goto menu

:end
echo.
echo 再见喵～ (=^･ω･^=)
timeout /t 2 /nobreak >nul
exit /b 0
