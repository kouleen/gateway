# Gateway

基于 CloudWeGo Hertz + Kitex 构建的高性能 Go 服务端网关，支持动态 IDL 加载、泛化 RPC 调用、WebSocket 代理、Redis 鉴权以及热更新等特性。

## 特性

- **动态路由配置**：通过 YAML 文件配置路由规则，无需修改代码即可新增服务和接口
- **泛化 RPC 调用**：基于 Kitex Generic Client，无需预生成代码即可代理 Thrift RPC 请求
- **服务发现**：集成 etcd 注册中心，自动发现和路由到后端微服务
- **热更新机制**：通过 Git Webhook 触发 IDL 仓库更新，动态加载新的路由和服务
- **WebSocket 代理**：支持 WebSocket 协议的透传代理
- **Redis 鉴权**：基于 Redis Token 的统一鉴权中间件，支持白名单路径
- **链路追踪**：自动生成 TraceID，支持全链路透传（x-trace-id、x-user-id）
- **Docker 部署**：提供 Dockerfile 和 docker-compose.yml，一键部署

## 项目结构

```
gateway/
├── main.go                      # 程序入口
├── go.mod                       # Go 模块依赖
├── Dockerfile                   # Docker 构建文件
├── docker-compose.yml           # Docker Compose 配置
├── .github/workflows/           # CI/CD 工作流
│   └── docker-image.yml         # Docker 镜像自动构建流水线
├── idl/                         # IDL 定义（示例）
│   ├── common/                  # 公共结构体
│   │   ├── base.thrift          # 基础请求/响应结构
│   │   └── response.thrift      # 统一响应结构
│   ├── gateway/                 # 网关配置
│   │   ├── services.yaml        # 服务路由配置
│   │   └── white-path.yaml      # 白名单路径配置
│   └── user-center/             # 用户中心 IDL
│       ├── user.thrift          # 用户服务主定义
│       ├── user_login.thrift    # 登录相关结构体
│       ├── user_header.thrift   # 用户信息结构体
│       └── user_position.thrift # 用户位置结构体
└── internal/
    ├── config/
    │   └── service.go           # 配置加载与路由表
    ├── generic/
    │   └── pool.go              # 泛化客户端池管理
    ├── handler/
    │   ├── gateway.go           # 网关核心处理器
    │   └── webhook.go           # Webhook 更新处理器
    ├── idlmanager/
    │   └── manager.go           # IDL 仓库管理器（克隆/拉取/热加载）
    ├── middleware/
    │   ├── auth.go              # 鉴权中间件
    │   └── redis.go             # Redis 连接与 Token 查询
    └── router/
        └── register.go          # 路由注册
```

## 快速开始

### 环境要求

- Go 1.25+
- Redis
- etcd（用于服务发现）
- Git（用于拉取 IDL 仓库）

### 安装与运行

1. **克隆项目**

```bash
git clone https://github.com/kouleen/gateway.git
cd gateway
```

2. **设置环境变量**

```bash
export LISTEN_ADDR=":8888"
export ETCD_ENDPOINTS="127.0.0.1:2379"
export IDL_REPO_URL="https://github.com/your-org/idl.git"
export IDL_REPO_BRANCH="main"
export IDL_LOCAL_PATH="/opt/idl-repo"
export WEBHOOK_SECRET="your-secret"
export REDIS_ADDR="127.0.0.1:6379"
export REDIS_PASSWORD=""
export REDIS_DB=0
```

3. **运行服务**

```bash
go run main.go
```

或构建后运行：

```bash
go build -o gateway main.go
./gateway
```

## 配置说明

### 环境变量

| 变量名              | 默认值              | 说明                              |
| ------------------- |------------------| --------------------------------- |
| `LISTEN_ADDR`     | `:8888`          | 网关监听地址                      |
| `ETCD_ENDPOINTS`  | `127.0.0.1:2379` | etcd 服务发现地址，多个用逗号分隔 |
| `IDL_REPO_URL`    | 空                | IDL 仓库的 Git 地址               |
| `IDL_REPO_BRANCH` | 空                | IDL 仓库分支                      |
| `IDL_LOCAL_PATH`  | `idl`            | IDL 仓库本地存储路径              |
| `WEBHOOK_SECRET`  | 空                | Webhook 签名密钥（留空则不校验）  |
| `REDIS_ADDR`      | `127.0.0.1:6379` | Redis 地址                        |
| `REDIS_PASSWORD`  | 空                | Redis 密码                        |
| `REDIS_DB`        | `0`              | Redis 数据库索引                  |

### 服务路由配置（services.yaml）

在 IDL 仓库的 `gateway/services.yaml` 中配置服务路由：

```yaml
servers:
  user:
    service_name: user.service.rpc
    idl_path: user-center/user.thrift
    routes:
      - path: /api/user/login
        method: Login
        http_method: POST
      - path: /api/user/logout
        method: Logout
        http_method: POST
```

**配置字段说明：**

- `service_name`：后端服务在 etcd 注册的服务名
- `idl_path`：IDL 文件相对于仓库根目录的路径
- `routes`：路由规则列表
  - `path`：HTTP 请求路径
  - `method`：对应的 Thrift RPC 方法名
  - `http_method`：HTTP 方法（GET、POST、PUT、DELETE 等）

### 白名单配置（white-path.yaml）

在 IDL 仓库的 `gateway/white-path.yaml` 中配置免鉴权路径：

```yaml
white_path:
  - /api/webhook/idl-update
  - /api/user/login
  - /api/user/sms
```

## 核心功能

### 请求流程

```
客户端请求
    │
    ▼
[Hertz HTTP Server]
    │
    ├── 访问日志中间件
    ├── 异常恢复中间件
    ├── CORS 跨域中间件
    ├── 鉴权中间件（Redis Token 校验）
    │
    ▼
[路由匹配]
    │  根据 HTTP Method + Path 匹配路由表
    │
    ▼
[泛化 RPC 调用]
    │  1. 从客户端池获取对应服务的 Generic Client
    │  2. 组装请求参数（Query/Body/Path Params）
    │  3. 通过 etcd 解析服务地址
    │  4. 发起 Kitex 泛化调用
    │
    ▼
[统一响应]
    │  {
    │    "sign": 1700000000000,
    │    "code": 200,
    │    "message": "success",
    │    "data": { ... },
    │    "traceId": "uuid"
    │  }
```

### 路由规则管理

路由规则通过 `services.yaml` 配置，支持以下特性：

- **新增服务**：在 `servers` 下添加新的服务配置，网关自动热加载
- **多路由映射**：一个 RPC 服务可映射多个 HTTP 路径
- **HTTP 方法支持**：支持 GET、POST、PUT、DELETE 等所有 HTTP 方法

### 鉴权机制

网关使用 Redis Token 进行统一鉴权：

1. 客户端登录成功后，后端将 Token 写入 Redis（Key: `auth:token:{token}`，Value: `userId`）
2. 客户端后续请求需在 `Authorization` 或 `X-Token` 请求头中携带 Token
3. 网关校验 Token 有效性，有效则将 `userId` 注入请求上下文（`x-user-id`）
4. 白名单路径的请求无需鉴权

**支持的 Token 格式：**

- `Authorization: Bearer your-token`
- `Authorization: your-token`
- `X-Token: your-token`

### WebSocket 支持

网关自动识别 WebSocket 升级请求，并通过 WebSocket 连接透传泛化调用：

```
客户端 ──WebSocket──► 网关 ──Kitex GenericCall──► 后端服务
    ◄──────────────────── 响应消息 ──────────────────┘
```

### 热更新

通过 Webhook 触发 IDL 仓库更新和路由热加载：

```bash
# GitHub/GitLab Webhook 配置
POST /api/webhook/update?secret=your-secret
```

**更新流程：**

1. Webhook 触发后，网关执行 `git pull` 拉取最新 IDL 仓库
2. 重新解析 `services.yaml` 和 `white-path.yaml`
3. 重新构建所有泛化客户端和路由映射
4. 原子替换客户端池和路由表，实现无间断热更新

## API 接口

### 路由转发

所有业务接口通过统一入口转发：

```
{HTTP_METHOD} /api/{service_path}
```

**请求示例：**

```bash
# POST 请求
curl -X POST http://localhost:8888/api/user/login \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{"username": "test", "password": "123456"}'

# GET 请求
curl http://localhost:8888/api/user/sendSmsMessage?phone=13800138000
```

**响应格式：**

```json
{
  "sign": 1700000000000,
  "code": 200,
  "message": "success",
  "data": {
    "accessToken": "...",
    "expireTime": 1700000000000
  },
  "traceId": "550e8400-e29b-41d4-a716-446655440000"
}
```

### Webhook 更新

```
GET /api/webhook/update?secret=your-secret
```

触发 IDL 仓库拉取和路由热更新。

### 直调模式（调试用）

```
POST /api/:service/:method
```

直接指定服务 Key 和方法名进行泛化调用，便于调试。

## Docker 部署

### 使用 Docker Compose

```bash
docker compose up -d
```

### 手动构建

```bash
docker build -t kouleen/gateway:latest .
docker run -d -p 8888:8888 \
  -e ETCD_ENDPOINTS="127.0.0.1:2379" \
  -e IDL_REPO_URL="https://github.com/your-org/idl.git" \
  -e IDL_REPO_BRANCH="main" \
  -e REDIS_ADDR="127.0.0.1:6379" \
  kouleen/gateway:latest
```

### 环境变量

| 变量名              | 默认值             | 说明         |
| ------------------- | ------------------ | ------------ |
| `LISTEN_ADDR`     | `:8888`          | 网关监听地址 |
| `ETCD_ENDPOINTS`  | `127.0.0.1:2379` | etcd 地址    |
| `IDL_REPO_URL`    | 空                 | IDL 仓库地址 |
| `IDL_REPO_BRANCH` | 空                 | IDL 仓库分支 |
| `IDL_LOCAL_PATH`  | `/opt/idl-repo`  | IDL 本地路径 |
| `WEBHOOK_SECRET`  | 空                 | Webhook 密钥 |
| `REDIS_ADDR`      | `127.0.0.1:6379` | Redis 地址   |
| `REDIS_PASSWORD`  | 空                 | Redis 密码   |
| `REDIS_DB`        | `0`              | Redis 数据库 |

## IDL 规范

### Thrift IDL 结构

```thrift
namespace go user

struct LoginRequest {
    1: string username
    2: string phone
    3: string code
    4: string password
    5: optional i32 loginType
    6: string uuid
}

struct LoginResponse {
    1: string accessToken
    2: optional i64 expireTime
}

service UserService {
    LoginResponse Login(1: LoginRequest loginRequest)
    bool SendSmsMessage(1: LoginRequest loginRequest)
    bool Logout(1: LoginRequest loginRequest)
}
```

### 请求参数映射

- **GET 请求**：从 URL Query 参数映射到请求体
- **POST/PUT 请求**：从 JSON Body 解析
- **路径参数**：从 URL 路径中提取（如 `/api/:service/:method`）

### 响应处理

- RPC 成功：响应体中 `data` 字段包含 RPC 返回结果
- RPC 失败：响应体中 `message` 字段包含错误信息
- 统一格式：`sign`（时间戳）、`code`（状态码）、`message`（消息）、`data`（数据）、`traceId`（追踪ID）

## 链路追踪

网关自动为每个请求生成 TraceID 并透传：

- `x-trace-id`：请求唯一标识，全链路透传
- `x-user-id`：当前用户ID，通过鉴权中间件注入
- `x-token`：当前请求 Token，用于下游服务校验

## 技术栈

| 技术                                                 | 说明             |
| ---------------------------------------------------- | ---------------- |
| [CloudWeGo Hertz](https://github.com/cloudwego/hertz) | 高性能 HTTP 框架 |
| [CloudWeGo Kitex](https://github.com/cloudwego/kitex) | 高性能 RPC 框架  |
| [go-redis](https://github.com/redis/go-redis)         | Redis 客户端     |
| [etcd](https://github.com/etcd-io/etcd)               | 服务发现与注册   |
| [Thrift](https://thrift.apache.org/)                  | IDL 定义与序列化 |
| [YAML](https://github.com/go-yaml/yaml)               | 配置文件解析     |
| [Docker](https://www.docker.com/)                     | 容器化部署       |

## 依赖说明

```go
require (
    github.com/cloudwego/hertz v0.9.4        // HTTP 框架
    github.com/cloudwego/kitex v0.16.3         // RPC 框架（泛化调用）
    github.com/kitex-contrib/registry-etcd     // etcd 服务发现
    github.com/redis/go-redis/v9               // Redis 客户端
    github.com/hertz-contrib/cors             // CORS 跨域
    github.com/hertz-contrib/logger/accesslog // 访问日志
    github.com/hertz-contrib/websocket        // WebSocket 支持
    gopkg.in/yaml.v3                          // YAML 解析
)
```

## 常见问题

### Q: 如何新增一个服务？

1. 在 IDL 仓库的对应目录创建 `.thrift` 文件
2. 在 `gateway/services.yaml` 中添加服务配置和路由规则
3. 触发 Webhook 或等待自动拉取，网关会自动热加载

### Q: Webhook 未配置 secret 时是否安全？

如果 `WEBHOOK_SECRET` 环境变量为空，Webhook 接口不会校验签名，任何能访问该接口的请求都会触发更新。生产环境务必配置 secret。

### Q: 如何调试 RPC 调用？

使用直调模式：`POST /api/{service_key}/{method_name}`，直接传入 JSON 请求体。

### Q: etcd 连接失败怎么办？

确保 etcd 服务正常运行，且 `ETCD_ENDPOINTS` 环境变量配置正确。网关启动时会尝试连接 etcd，连接失败会导致服务不可用。

### Q: 如何查看网关日志？

网关使用 CloudWeGo 日志库，默认输出到 stdout。日志包含：

- 请求/响应详情
- TraceID 追踪
- 错误信息

## License

MIT License
