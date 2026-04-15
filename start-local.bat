@echo off
chcp 65001 >nul
echo ========================================
echo   短视频系统 - 本地启动脚本
echo ========================================
echo.

echo [1/4] 检查依赖服务...
docker compose ps mysql redis rabbitmq >nul 2>&1
if %errorlevel% neq 0 (
    echo ✗ Docker Compose 服务未运行
    echo 正在启动 MySQL、Redis、RabbitMQ...
    docker compose up -d mysql redis rabbitmq
    timeout /t 10 /nobreak >nul
) else (
    echo ✓ 依赖服务已运行
)
echo.

echo [2/4] 启动 Backend API (端口 8080)...
start "Backend API" cmd /k "cd /d %~dp0backend && go run ./cmd"
timeout /t 3 /nobreak >nul
echo ✓ Backend API 已启动
echo.

echo [3/4] 启动 Worker (端口 8081)...
start "Worker" cmd /k "cd /d %~dp0backend && go run ./cmd/worker"
timeout /t 3 /nobreak >nul
echo ✓ Worker 已启动
echo.

echo [4/4] 启动 Prometheus 和 Grafana...
docker compose up -d prometheus grafana
timeout /t 5 /nobreak >nul
echo ✓ 可观测平台已启动
echo.

echo ========================================
echo   所有服务已启动！
echo ========================================
echo.
echo 访问地址：
echo   - Backend API:    http://localhost:8080
echo   - Backend Metrics: http://localhost:8080/metrics
echo   - Worker Metrics:  http://localhost:8081/metrics
echo   - Prometheus:      http://localhost:9090
echo   - Grafana:         http://localhost:3000 (admin/admin123)
echo   - RabbitMQ:        http://localhost:15672 (admin/password123)
echo.
echo 按任意键退出...
pause >nul
