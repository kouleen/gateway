# Gateway

基于 CloudWeGo Hertz + Kitex 构建的高性能 Go 服务端网关。通过动态加载 IDL 定义实现泛化 RPC 转发，支持路由热更新、WebSocket 代理、Redis 鉴权等能力，让后端微服务以零代码侵入的方式暴露 HTTP 接口。

## 特性

- **动态 IDL 加载**：运行时从 Git 仓库克隆/拉取 Thrift IDL 文件，无需预生成代码
- **泛化 RPC 调用**：基于 Kitex Generic Client，HTTP 请求自动转换为 Thrift RPC 调用
- **动态路由配置**：通过 YAML 文件配置路由规则，新增服务无需修改代码、无需重启
- **服务发现**：集成 etcd 注册中心，自动发现后端微服务实例
- **热更新**：Webhook 触发 IDL 仓库更新，原子替换路由表和客户端池，无间断切换
- **WebSocket 代理**：自动识别 WebSocket 升级请求，每条消息独立泛化调用
- **统一鉴权**：基于 Redis Token 的鉴权中间件，支持路径白名单（IDL 配置驱动）
- **链路追踪**：自动生成 TraceID 并透传 x-trace-id / x-user-id / x-token
- **Git 认证**：支持 Basic Auth 访问私有 IDL 仓库
- **容器化部署**：提供 Dockerfile、docker-compose.yml 和 etcd 独立部署配置

## 技术栈

| 组件         | 技术                             | 版本    |
| ------------ | -------------------------------- | ------- |
| HTTP 框架    | CloudWeGo Hertz                  | v0.9.4  |
| RPC 框架     | CloudWeGo Kitex                  | v0.16.3 |
| 泛化调用     | Kitex Generic (MapThriftGeneric) | —      |
| 服务发现     | kitex-contrib/registry-etcd      | v0.3.0  |
| Redis 客户端 | go-redis                         | v9.22.0 |
| Git 操作     | go-git (纯 Go 实现)              | v5.19.2 |
| 密码加密     | golang.org/x/crypto (bcrypt)     | v0.53.0 |
| 并发控制     | golang.org/x/sync (singleflight) | v0.21.0 |
| 日志         | bytedance/gopkg/util/logger      | —      |
| 配置解析     | gopkg.in/yaml.v3                 | v3.0.1  |
| 跨域         | hertz-contrib/cors               | v0.1.0  |
| 访问日志     | hertz-contrib/logger/accesslog   | —      |
| WebSocket    | hertz-contrib/websocket          | v0.2.0  |
| 唯一 ID      | google/uuid                      | v1.6.0  |

## 项目结构

```
gateway/
├── main.go                          # 程序入口（配置→Redis→IDL管理器→Hertz→中间件→路由）
├── go.mod                           # Go 模块定义
├── go.sum                           # 依赖锁定
├── Dockerfile                       # 多阶段 Docker 构建
├── docker-compose.yml               # Gateway 容器编排
├── etcd/
│   └── docker-compose.yml           # etcd 独立部署配置
├── .github/workflows/
│   └── docker-image.yml             # CI/CD：push release 分支自动构建镜像并回调部署
├── internal/
│   ├── config/
│   │   └── service.go               # 配置结构、环境变量加载、路由表（读写锁）
│   ├── generic/
│   │   └── pool.go                  # 泛化客户端池（sync.Map，支持原子替换）
│   ├── handler/
│   │   ├── gateway.go               # 核心处理器：路由匹配→参数组装→泛化调用→WebSocket
│   │   └── webhook.go               # Webhook 更新处理器（bcrypt 签名校验）
│   ├── idlmanager/
│   │   └── manager.go               # IDL 管理器：go-git 克隆/拉取 + 动态加载 + 热更新
│   ├── middleware/
│   │   ├── auth.go                  # 鉴权中间件（白名单 + Redis Token 校验）
│   │   └── redis.go                 # Redis 连接初始化 + Token 查询
│   └── router/
│       └── register.go              # 路由注册（/api/webhook/update + /api/*通配）
```

## 架构设计

### 启动流程

```
main()
  │
  ├─ 1. config.LoadConfig()         从环境变量加载全局配置
  ├─ 2. InitRedis()                 初始化 Redis 单例（Ping 失败则 Fatal）
  ├─ 3. InitManager()
  │     ├─ InitPool()               初始化泛化客户端池
  │     ├─ initRepo()              go-git 浅克隆 IDL 仓库（不存在时）/ 拉取（已存在时）
  │     └─ reloadAll()              解析 services.yaml + white-path.yaml
  │                                   为每个服务加载 Thrift → 构建 GenericClient → 填充路由表
  ├─ 4. hlog.SetLevel(LevelInfo)    设置日志级别
  ├─ 5. server.Default()            创建 Hertz 实例（监听地址 + 30s 读超时）
  ├─ 6. 注册中间件链
  │     ├─ accesslog                访问日志
  │     ├─ recovery                 异常恢复
  │     ├─ cors                     CORS 跨域
  │     └─ AuthMiddleware           鉴权（白名单 → Token 提取 → Redis 校验）
  ├─ 7. RegisterRoutes()
  │     ├─ GET  /api/webhook/update   Webhook 更新入口
  │     └─ ANY  /api/*path            通配转发入口
  └─ 8. h.Spin()                    启动 HTTP 服务
```

### 请求处理流程

```
HTTP Request
     │
     ▼
 ┌─────────────────────────────────────┐
 │          中间件链（按顺序）            │
 │  ① accesslog  ② recovery           │
 │  ③ cors       ④ AuthMiddleware    │
 └─────────────────────────────────────┘
     │
     ▼
 白名单路径？ ──Yes──▶ 直接放行
     │No
     ▼
 提取 Authorization / X-Token Header
 去除 "Bearer " 前缀
     │
     ▼
 Redis GET auth:token:{token} ──失败──▶ 401 Unauthorized
     │成功
     ▼
 注入 x-user-id、x-token 到 metainfo
     │
     ▼
 ┌─────────────────────────────────────┐
 │        CustomRouteHandler            │
 │                                      │
 │  生成 x-trace-id (UUID)              │
 │  匹配路由表 (HTTPMethod:Path)        │
 │     └─匹配失败→404                   │
 │                                      │
 │  获取 GenericClient                  │
 │     └─不存在→404                    │
 │                                      │
 │  WebSocket 请求？──Yes──▶ WS 处理   │
 │     │No                              │
 │     ▼                                │
 │  组装请求体：Query + JSON + Params   │
 │     │                                │
 │     ▼                                │
 │  cli.GenericCall(method, body)      │
 │     │                                │
 │     ▼                                │
 │  统一 JSON 响应                      │
 └─────────────────────────────────────┘
     │
     ▼
 后端 Kitex 服务
```

### WebSocket 处理流程

```
客户端 ──WS 握手──▶ Hertz WS 升级
                          │
                          ▼
                     读取每条 WS 消息
                          │
                          ▼
                     JSON 反序列化为 map
                          │
                          ▼
                     cli.GenericCall(method, body)
                          │
                          ▼
                     封装统一响应写回
                          │
                          ▼
                     继续读下一条消息...
```

### 热更新流程

```
IDL 仓库 Push → 触发 Webhook
     │
     ▼
 GET /api/webhook/update?secret={webhook_secret}
     │
     ▼
 bcrypt 校验 secret（配置为空时跳过）
     │
     ▼
 异步 goroutine 执行 TriggerUpdate()
     │
     ▼
 singleflight.Do("idl-update")   并发合并
     │
     ├─ pullRepo()               go-git fetch + merge
     │     └─NoErrAlreadyUpToDate → 跳过
     │
     ├─ reloadAll()
     │     ├─ 重新解析 services.yaml / white-path.yaml
     │     ├─ 为每个服务加载 Thrift + 构建 GenericClient
     │     ├─ 填充新的路由表 + 客户端池
     │     └─ 成功后原子替换（ReplaceRouteTable + ReplacePool）
     │           └─失败则保留旧版本
     │
     └─ 返回结果
```

## 快速开始

### 1. 环境要求

| 依赖             | 说明              |
| ---------------- | ----------------- |
| Go 1.25+         | 编译运行          |
| Redis            | Token 存储与校验  |
| etcd 3.6+        | 服务注册与发现    |
| Git 账号（可选） | 私有 IDL 仓库认证 |

### 2. 启动 etcd

```bash
cd etcd
docker compose up -d
```

### 3. 拉取 IDL 仓库

IDL 文件需存放在独立的 Git 仓库中。仓库结构示例：

```
idl-repo/
├── gateway/
│   ├── services.yaml      # 服务路由配置
│   └── white-path.yaml    # 白名单路径配置
├── common/
│   ├── base.thrift        # 公共结构体
│   └── response.thrift    # 统一响应结构
└── user-center/
    ├── user.thrift
    ├── user_login.thrift
    └── ...
```

### 4. 配置环境变量

```bash
# 网关监听地址
export LISTEN_ADDR=":8888"

# etcd 服务发现地址
export ETCD_ENDPOINTS="127.0.0.1:2379"

# IDL 仓库 Git 地址
export IDL_REPO_URL="https://github.com/your-org/idl-repo.git"
export IDL_REPO_BRANCH="main"

# IDL 本地存储路径
export IDL_LOCAL_PATH="/opt/idl-repo"

# Webhook 签名密钥
export WEBHOOK_SECRET="your-secret-key"

# Redis 连接
export REDIS_ADDR="127.0.0.1:6379"
export REDIS_PASSWORD=""
export REDIS_DB=0

# Git 认证（可选，私有仓库需配置）
export GIT_AUTH_USER="git"
export GIT_AUTH_PASSWORD="your-pat-token"
```

### 5. 运行

```bash
# 开发模式
go run main.go

# 构建后运行
go build -o gateway main.go
./gateway
```

## 配置说明

### 环境变量

| 变量                  | 默认值             | 类型   | 说明                                          |
| --------------------- | ------------------ | ------ | --------------------------------------------- |
| `LISTEN_ADDR`       | `:8888`          | string | 网关 HTTP 监听地址                            |
| `ETCD_ENDPOINTS`    | `127.0.0.1:2379` | string | etcd 地址列表，逗号分隔                       |
| `IDL_REPO_URL`      | 空                 | string | IDL 仓库 Git URL                              |
| `IDL_REPO_BRANCH`   | 空                 | string | IDL 仓库分支名                                |
| `IDL_LOCAL_PATH`    | `idl`            | string | IDL 仓库本地克隆路径                          |
| `WEBHOOK_SECRET`    | 空                 | string | Webhook 签名密钥，空则跳过校验                |
| `REDIS_ADDR`        | `127.0.0.1:6379` | string | Redis 地址                                    |
| `REDIS_PASSWORD`    | 空                 | string | Redis 密码                                    |
| `REDIS_DB`          | `0`              | int    | Redis 数据库索引                              |
| `GIT_AUTH_USER`     | 空                 | string | Git Basic Auth 用户名（PAT 场景填任意字符串） |
| `GIT_AUTH_PASSWORD` | 空                 | string | Git Basic Auth 密码或 PAT Token               |

### 服务路由配置（services.yaml）

存放在 IDL 仓库 `gateway/services.yaml`：

```yaml
servers:
  user:
    service_name: user.service.rpc
    idl_path: user-center/user.thrift
    routes:
      - path: /api/user/login
        method: Login
        http_method: POST
      - path: /api/user/sendSmsMessage
        method: SendSmsMessage
        http_method: GET
      - path: /api/user/logout
        method: Logout
        http_method: POST
      - path: /api/user/queryUserHeaderPage
        method: QueryUserHeaderPage
        http_method: GET
```

**字段说明：**

| 字段                     | 说明                                       |
| ------------------------ | ------------------------------------------ |
| `service_name`         | 后端服务在 etcd 中注册的服务名             |
| `idl_path`             | IDL 文件相对于仓库根目录的路径             |
| `routes[].path`        | HTTP 请求路径                              |
| `routes[].method`      | 对应的 Thrift RPC 方法名                   |
| `routes[].http_method` | HTTP 方法（GET / POST / PUT / DELETE ...） |

> 多个服务在 `servers` 下以 key 区分，key 作为服务标识用于客户端池索引。

### 白名单配置（white-path.yaml）

存放在 IDL 仓库 `gateway/white-path.yaml`：

```yaml
white_path:
  - /api/webhook/update
  - /api/user/login
  - /api/user/sendSmsMessage
```

白名单路径的请求将跳过鉴权中间件，直接放行。

## 鉴权机制

### 工作流程

1. 客户端调用登录接口（白名单路径），后端校验通过后生成 Token
2. 后端将 Token 写入 Redis：`SET auth:token:{token} {userId}`
3. 客户端后续请求携带 Token 访问业务接口
4. 网关鉴权中间件执行：
   - 白名单路径 → 直接放行
   - 提取 `Authorization` 或 `X-Token` Header
   - 去除 `Bearer ` 前缀
   - `GET auth:token:{token}` 查询 Redis
   - Token 有效 → 注入 `x-user-id`、`x-token` 到 metainfo
   - Token 无效/过期 → 返回 401

### 支持的 Token 传输格式

```
Authorization: Bearer eyJhbGciOi...
Authorization: eyJhbGciOi...
X-Token: eyJhbGciOi...
```

### Redis Key 约定

```
Key:   auth:token:{your-token}
Value: {user-id}
```

## API 接口

### 路由转发（核心入口）

```
{HTTP_METHOD} /api/*path
```

所有配置在 `services.yaml` 中的路由都通过此统一入口处理。

**请求示例：**

```bash
# POST 请求（JSON Body）
curl -X POST http://localhost:8888/api/user/login \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-token" \
  -d '{"username": "admin", "password": "123456"}'

# GET 请求（Query 参数）
curl http://localhost:8888/api/user/sendSmsMessage?phone=13800138000
```

**统一响应格式：**

```json
{
  "sign": 1700000000000,
  "code": 200,
  "message": "success",
  "data": {
    "accessToken": "xxx",
    "expireTime": 1700000000000
  },
  "traceId": "550e8400-e29b-41d4-a716-446655440000"
}
```

| 字段        | 说明                     |
| ----------- | ------------------------ |
| `sign`    | 服务器响应时间戳（毫秒） |
| `code`    | HTTP 状态码              |
| `message` | 业务消息                 |
| `data`    | RPC 返回结果             |
| `traceId` | 请求追踪 ID（UUID）      |

### Webhook 更新

```
GET /api/webhook/update?secret={webhook_secret}
```

手动触发 IDL 仓库更新和路由热加载。

**注意：**

- 需在 IDL 仓库的 `white-path.yaml` 中添加 `/api/webhook/update` 以跳过鉴权
- `secret` 参数与 `WEBHOOK_SECRET` 环境变量匹配时才会触发更新
- 更新为异步执行，接口立即返回

### 直调模式（调试用）

```
POST /api/:service/:method
```

直接指定服务 Key 和 RPC 方法名进行泛化调用，便于调试。当前代码中路由已注释，需在 `router/register.go` 中启用。

## IDL 规范

### Thrift 定义示例

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

| HTTP 方法           | 参数来源  | 映射方式                    |
| ------------------- | --------- | --------------------------- |
| GET                 | URL Query | 自动合并为请求体            |
| POST / PUT / DELETE | JSON Body | `BindJSON` 反序列化为 map |
| 路径参数            | URL Path  | 路由`:param` 自动注入     |

> 所有参数合并后以 map 形式传入 `GenericCall`，Kitex 泛化框架根据 Thrift IDL 中的字段名自动匹配。

### 泛化调用机制

- 使用 `MapThriftGeneric` 泛化器：支持 map 类型入参，避免预生成代码
- 使用 `TTHeaderFramed` 传输协议：与 Kitex 服务端保持一致
- RPC 超时：10 秒

## 链路追踪

网关为每个请求生成唯一 TraceID 并沿调用链透传：

| Key            | 类型       | 来源           | 说明                    |
| -------------- | ---------- | -------------- | ----------------------- |
| `x-trace-id` | Persistent | 网关自动生成   | UUID，全链路透传        |
| `x-user-id`  | Persistent | 鉴权中间件注入 | 从 Redis Token 查询获得 |
| `x-token`    | Persistent | 鉴权中间件注入 | 当前请求 Token          |

> `PersistentValue` 表示该值会随 Kitex RPC 调用自动透传到下游服务，无需手动传递。

## 日志

网关使用 `bytedance/gopkg/util/logger`（基于 zap）：

- 默认日志级别：`Info`
- 日志内容：
  - 请求详情（Method / Path / Request Body）
  - 响应详情（Response / Error）
  - TraceID 贯穿整个日志链路
- 输出目标：stdout（Docker 容器中可通过 `docker logs` 查看）

## Docker 部署

### 使用 Docker Compose

```bash
# 1. 启动 etcd
cd etcd && docker compose up -d && cd ..

# 2. 启动 gateway
docker compose up -d
```

### 手动构建

```bash
docker build -t kouleen/gateway:latest .

docker run -d \
  --name gateway \
  -p 8888:8888 \
  -e ETCD_ENDPOINTS="127.0.0.1:2379" \
  -e IDL_REPO_URL="https://github.com/your-org/idl-repo.git" \
  -e IDL_REPO_BRANCH="main" \
  -e REDIS_ADDR="127.0.0.1:6379" \
  -e WEBHOOK_SECRET="your-secret" \
  -e GIT_AUTH_USER="git" \
  -e GIT_AUTH_PASSWORD="your-pat-token" \
  --network go-server-internal \
  kouleen/gateway:latest
```

### Docker Compose 环境变量

| 变量                | 说明         |
| ------------------- | ------------ |
| `LISTEN_ADDR`     | 网关监听地址 |
| `ETCD_ENDPOINTS`  | etcd 地址    |
| `IDL_REPO_URL`    | IDL 仓库地址 |
| `IDL_REPO_BRANCH` | IDL 仓库分支 |
| `IDL_LOCAL_PATH`  | IDL 本地路径 |
| `WEBHOOK_SECRET`  | Webhook 密钥 |
| `REDIS_ADDR`      | Redis 地址   |
| `REDIS_PASSWORD`  | Redis 密码   |
| `REDIS_DB`        | Redis 数据库 |

### 镜像构建

多阶段构建：

- **构建阶段**：`kouleen/golang:1.25` 镜像，禁用 CGO 编译
- **运行阶段**：`kouleen/alpine:latest` 镜像，安装 tzdata / git / ca-certificates
- **时区**：`Asia/Shanghai`
- **端口**：8888

## CI/CD

### GitHub Actions

推送代码到 `release` 分支时触发：

1. 构建 Docker 镜像
2. 推送到 Docker Hub（`kouleen/gateway:latest`）
3. 回调自定义 API 通知部署

配置的 Secrets：

| Secret              | 说明              |
| ------------------- | ----------------- |
| `DOCKER_USERNAME` | Docker Hub 用户名 |
| `DOCKER_PASSWORD` | Docker Hub 密码   |
| `ETCD_ENDPOINTS`  | etcd 地址         |
| `IDL_LOCAL_PATH`  | IDL 本地路径      |
| `IDL_REPO_BRANCH` | IDL 仓库分支      |
| `IDL_REPO_URL`    | IDL 仓库地址      |
| `LISTEN_ADDR`     | 监听地址          |
| `REDIS_ADDR`      | Redis 地址        |
| `REDIS_PASSWORD`  | Redis 密码        |
| `REDIS_DB`        | Redis 数据库      |
| `WEBHOOK_SECRET`  | Webhook 密钥      |

## 常见问题

### Q: 如何新增一个后端服务？

1. 在 IDL 仓库中创建 `.thrift` 文件定义服务接口
2. 在 `gateway/services.yaml` 中添加服务配置和路由规则
3. 触发 Webhook 或等待下次更新，网关自动热加载

### Q: 如何配置私有 IDL 仓库？

```bash
export GIT_AUTH_USER="git"
export GIT_AUTH_PASSWORD="your-github-pat-token"
```

网关将使用 go-git 的 HTTP Basic Auth 克隆和拉取私有仓库。

### Q: Webhook 不生效怎么办？

1. 确认 `WEBHOOK_SECRET` 已正确配置
2. 确认 `white-path.yaml` 中包含 `/api/webhook/update`
3. 确认 IDL 仓库 Push 事件触发了对应 Webhook
4. 查看网关日志排查 git pull 是否成功

### Q: etcd 连接失败怎么办？

1. 确认 etcd 服务正常运行
2. 确认 `ETCD_ENDPOINTS` 地址正确
3. Docker 部署时确认容器在同一 Network 内
4. 网关启动时 etcd 连接失败会 Fatal 退出

### Q: Redis 连接失败怎么办？

1. 确认 Redis 服务正常运行
2. 确认地址、密码、数据库索引配置正确
3. 网关启动时 Redis 连接失败会 Fatal 退出

### Q: 如何调试泛化调用？

在 `router/register.go` 中启用直调路由：

```go
apiGroup.POST("/:service/:method", handler.DirectCallHandler)
```

然后：

```bash
curl -X POST http://localhost:8888/api/{service_key}/{method_name} \
  -H "Content-Type: application/json" \
  -d '{"field": "value"}'
```

### Q: WebSocket 如何使用？

客户端以 WebSocket 协议连接到已配置的 HTTP 路径：

```
ws://localhost:8888/api/your/ws/path
```

每条 WebSocket 消息为独立的 JSON 请求体，网关对每条消息独立执行泛化调用并返回结果。

## License

MIT License
