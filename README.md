# feedsystem_video_go

基于 Go 的短视频 Feed 系统（后端 + 前端），包含账号、视频、点赞、评论、关注与 Feed 流；支持 Redis 缓存与 RabbitMQ 异步 Worker（API 进程与 Worker 进程可拆分部署）。

详细设计与接口说明请阅读：`feedsystem_video_go项目设计.md`（包含模块设计、表结构、流程图与接口清单）。

## [项目演示](https://www.bilibili.com/video/BV1Dti7B9E6Y?vd_source=4b2884373b2c4c4147b10162c1709276)

---

## 快速启动

### 方式一：Docker Compose 一键启动（推荐）

**前置要求：**
- 已安装 Docker Desktop / Docker Engine + Docker Compose

**启动命令：**
```bash
docker compose up -d --build
```

**访问地址：**
- 前端：`http://localhost:5173`
- 后端 API：`http://localhost:8080`
- RabbitMQ 管理台：`http://localhost:15672`（默认账号 `admin` / `password123`）
- Prometheus：`http://localhost:9090`
- Grafana：`http://localhost:3000`（默认账号 `admin` / `admin123`）

**说明：**
- Compose 会启动 `mysql`、`redis`、`rabbitmq`、`backend`（API）、`worker`、`prometheus`、`grafana`、`frontend` 共 8 个服务
- 容器内后端配置使用 `backend/configs/config.docker.yaml`（会挂载到 `/app/configs/config.yaml`）
- 可观测平台详细使用说明请查看 `OBSERVABILITY.md`

**停止服务：**
```bash
docker compose down
```

---

### 方式二：本地开发启动（不容器化）

**前置要求：**
- Go 1.21+
- Node.js 18+
- Docker（用于启动 MySQL、Redis、RabbitMQ）

#### Windows 用户

**一键启动脚本（推荐）：**
```bash
# 启动所有服务（依赖 + Backend + Worker + 可观测平台）
start-local.bat

# 或使用服务管理脚本（交互式菜单）
manage-services.bat
```

**手动启动：**

1. 启动依赖服务（MySQL、Redis、RabbitMQ）：
```bash
docker compose up -d mysql redis rabbitmq
```

2. 启动后端 API（新窗口）：
```bash
cd backend
go run ./cmd
```

3. 启动 Worker（新窗口）：
```bash
cd backend
go run ./cmd/worker
```

4. （可选）启动 Prometheus 和 Grafana：
```bash
docker compose up -d prometheus grafana
```

5. （可选）启动前端开发服务器（新窗口）：
```bash
cd frontend
npm install
npm run dev
```

#### Linux/macOS 用户

**一键启动脚本：**
```bash
# 启动所有服务（依赖 + Backend + Worker + Frontend）
./start.sh

# 仅启动 Backend 和 Worker（不启动 Frontend）
START_FRONTEND=0 ./start.sh

# 仅启动依赖服务
START_BACKEND=0 START_WORKER=0 START_FRONTEND=0 START_RABBITMQ=1 ./start.sh
```

**手动启动：**

1. 启动依赖服务：
```bash
docker compose up -d mysql redis rabbitmq
```

2. 启动后端 API：
```bash
cd backend
go run ./cmd
```

3. 启动 Worker：
```bash
cd backend
go run ./cmd/worker
```

4. （可选）启动前端：
```bash
cd frontend
npm install
npm run dev
```

---

## 配置说明

### 本地开发配置

本地开发使用 `backend/configs/config.yaml`，默认配置如下：

```yaml
server:
  port: 8080

database:
  host: localhost
  port: 3307          # 本地 MySQL 端口（避免与系统 MySQL 冲突）
  user: root
  password: 123456
  dbname: feedsystem

redis:
  host: localhost
  port: 6379
  password: 123456
  db: 0

rabbitmq:
  host: localhost
  port: 5672
  username: admin
  password: password123
```

### Docker 容器配置

Docker 环境使用 `backend/configs/config.docker.yaml`，配置如下：

```yaml
server:
  port: 8080

database:
  host: mysql         # Docker 服务名
  port: 3306
  user: root
  password: 123456
  dbname: feedsystem

redis:
  host: redis         # Docker 服务名
  port: 6379
  password: 123456
  db: 0

rabbitmq:
  host: rabbitmq      # Docker 服务名
  port: 5672
  username: admin
  password: password123
```

---

## 验证启动

### 检查服务状态

**Backend API：**
```bash
curl http://localhost:8080/metrics
```

**Worker：**
```bash
curl http://localhost:8081/metrics
```

**Prometheus：**
```bash
curl http://localhost:9090/-/healthy
```

**Grafana：**
```bash
curl http://localhost:3000/api/health
```

### 查看日志

**Docker 环境：**
```bash
# 查看所有服务日志
docker compose logs -f

# 查看特定服务日志
docker compose logs -f backend
docker compose logs -f worker
```

**本地开发：**
- Backend 和 Worker 日志会直接输出到终端
- 前端日志会输出到 Vite 开发服务器终端

---

## 常见问题

### 1. 端口冲突

如果遇到端口冲突，可以修改 `docker-compose.yml` 中的端口映射：

```yaml
services:
  mysql:
    ports:
      - "3307:3306"  # 本地端口:容器端口
```

### 2. MySQL 连接失败

确保 MySQL 服务已完全启动（healthcheck 通过）：

```bash
docker compose ps mysql
```

如果状态为 `starting`，请等待几秒后重试。

### 3. RabbitMQ 连接失败

检查 RabbitMQ 是否正常运行：

```bash
docker compose logs rabbitmq
```

访问管理台确认：`http://localhost:15672`（admin / password123）

### 4. 前端无法访问后端

确认：
- Backend API 已启动（`http://localhost:8080`）
- 前端 Vite 代理配置正确（`frontend/vite.config.ts`）

---

## 项目结构

```
.
├── backend/                 # Go 后端
│   ├── cmd/                # 主程序入口
│   │   ├── main.go        # API 服务
│   │   └── worker/        # Worker 服务
│   ├── configs/           # 配置文件
│   ├── internal/          # 业务逻辑
│   └── Dockerfile         # 多阶段构建
├── frontend/              # Vue 前端
├── docker-compose.yml     # Docker Compose 配置
├── prometheus.yml         # Prometheus 配置
├── grafana-dashboard.json # Grafana 仪表盘
├── start.sh              # Linux/macOS 启动脚本
├── start-local.bat       # Windows 启动脚本
└── manage-services.bat   # Windows 服务管理脚本
```
