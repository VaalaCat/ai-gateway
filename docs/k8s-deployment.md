# AI Gateway Kubernetes Deployment Guide

> 适用版本：当前 `main` 分支  
> 运行模式：`master`（控制平面）+ 可选外置 `agent`（数据平面）

## 1. 架构与部署策略

AI Gateway 支持两种部署形态：

1. **单实例 master（推荐起步）**
   - `master` 进程会自动拉起 embedded agent
   - 优点：部署简单、组件少、排障成本低
   - 缺点：控制面与数据面资源共享，扩展性有限

2. **master + 外置 agent**
   - 额外部署一个或多个 `agent` 副本
   - 优点：数据面可独立扩容、故障域隔离
   - 缺点：运维复杂度更高，需要管理 enrollment token 与 agent 凭据

---

## 2. 前置条件

- Kubernetes >= 1.24
- 可用镜像：`your-registry/ai-gateway:<tag>`
- 已安装 `kubectl`
- 建议配套 Ingress Controller（如 NGINX Ingress）

---

## 3. Namespace 与基础配置

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: ai-gateway
```

---

## 4. 部署 master（含 embedded agent）

### 4.1 ConfigMap（master 配置）

> SQLite WAL 已支持，推荐 `db_path` 带 `_pragma=journal_mode(WAL)`。

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ai-gateway-master-config
  namespace: ai-gateway
data:
  config.yaml: |
    role: master
    listen: ":8140"
    log_level: info

    master:
      db_path: "/data/master.db?_pragma=journal_mode(WAL)"
      jwt_secret: "replace-with-strong-secret"
      enrollment_token_ttl: 3600
      admin_user: "admin"
      admin_password: "change-this-password"

    agent:
      listen: ":8139"
      master_url: "http://127.0.0.1:8140"
      enrollment_token: ""
      credentials_file: "/data/agent_credentials.json"
      full_sync_interval: 300
      report_buffer_size: 1000
      report_flush_interval: 5
      heartbeat_interval: 30
      retry_max: 3

    relay:
      timeout: 300
      max_idle_conns: 100
      max_idle_conns_per_host: 10

    eventbus:
      type: memory
```

### 4.2 PVC（SQLite 持久化）

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ai-gateway-master-pvc
  namespace: ai-gateway
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

### 4.3 Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ai-gateway-master
  namespace: ai-gateway
spec:
  replicas: 1
  selector:
    matchLabels:
      app: ai-gateway-master
  template:
    metadata:
      labels:
        app: ai-gateway-master
    spec:
      containers:
        - name: ai-gateway
          image: your-registry/ai-gateway:latest
          imagePullPolicy: IfNotPresent
          args: ["master", "--config", "/app/config.yaml"]
          ports:
            - containerPort: 8140
          volumeMounts:
            - name: config
              mountPath: /app/config.yaml
              subPath: config.yaml
              readOnly: true
            - name: data
              mountPath: /data
          readinessProbe:
            httpGet:
              path: /ping
              port: 8140
            initialDelaySeconds: 5
            periodSeconds: 10
          livenessProbe:
            httpGet:
              path: /ping
              port: 8140
            initialDelaySeconds: 15
            periodSeconds: 20
      volumes:
        - name: config
          configMap:
            name: ai-gateway-master-config
        - name: data
          persistentVolumeClaim:
            claimName: ai-gateway-master-pvc
```

### 4.4 Service

```yaml
apiVersion: v1
kind: Service
metadata:
  name: ai-gateway-master
  namespace: ai-gateway
spec:
  selector:
    app: ai-gateway-master
  ports:
    - name: http
      port: 8140
      targetPort: 8140
```

### 4.5 Ingress（可选）

```yaml
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: ai-gateway
  namespace: ai-gateway
spec:
  rules:
    - host: ai-gateway.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: ai-gateway-master
                port:
                  number: 8140
```

---

## 5. 部署外置 agent（可选）

## 5.1 先生成 enrollment token

先登录 master 并创建 enrollment token（TTL 例子为 1 小时）：

```bash
ADMIN_TOKEN=$(curl -s http://ai-gateway.example.com/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"change-this-password"}' | jq -r '.token')

ENROLLMENT_TOKEN=$(curl -s http://ai-gateway.example.com/api/agents/enrollment-token \
  -X POST \
  -H "Authorization: Bearer ${ADMIN_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '{"ttl":3600}' | jq -r '.enrollment_token')

echo "${ENROLLMENT_TOKEN}"
```

### 5.2 ConfigMap（agent 配置）

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: ai-gateway-agent-config
  namespace: ai-gateway
data:
  agent.yaml: |
    role: agent
    listen: ":8139"
    log_level: info

    master:
      db_path: "/tmp/unused.db"
      jwt_secret: "unused"
      enrollment_token_ttl: 3600
      admin_user: "unused"
      admin_password: "unused"

    agent:
      listen: ":8139"
      master_url: "http://ai-gateway-master.ai-gateway.svc.cluster.local:8140"
      enrollment_token: "REPLACE_WITH_GENERATED_TOKEN"
      credentials_file: "/data/agent_credentials.json"
      full_sync_interval: 300
      report_buffer_size: 1000
      report_flush_interval: 5
      heartbeat_interval: 30
      retry_max: 3

    relay:
      timeout: 300
      max_idle_conns: 100
      max_idle_conns_per_host: 10

    eventbus:
      type: memory
```

### 5.3 Deployment（外置 agent）

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: ai-gateway-agent
  namespace: ai-gateway
spec:
  replicas: 2
  selector:
    matchLabels:
      app: ai-gateway-agent
  template:
    metadata:
      labels:
        app: ai-gateway-agent
    spec:
      containers:
        - name: ai-gateway-agent
          image: your-registry/ai-gateway:latest
          imagePullPolicy: IfNotPresent
          args: ["agent", "--config", "/app/agent.yaml"]
          ports:
            - containerPort: 8139
          volumeMounts:
            - name: config
              mountPath: /app/agent.yaml
              subPath: agent.yaml
              readOnly: true
            - name: data
              mountPath: /data
          readinessProbe:
            httpGet:
              path: /ping
              port: 8139
            initialDelaySeconds: 5
            periodSeconds: 10
      volumes:
        - name: config
          configMap:
            name: ai-gateway-agent-config
        - name: data
          emptyDir: {}
```

> 生产建议：首次注册成功后，去掉 `enrollment_token`，并改为持久卷保存 agent 凭据，避免 Pod 重建后重复注册。

---

## 6. 升级与回滚

### 升级镜像

```bash
kubectl -n ai-gateway set image deploy/ai-gateway-master ai-gateway=your-registry/ai-gateway:<new-tag>
kubectl -n ai-gateway rollout status deploy/ai-gateway-master
```

### 回滚

```bash
kubectl -n ai-gateway rollout undo deploy/ai-gateway-master
```

---

## 7. 常见问题与排查

### 7.1 Agent 未注册

- 检查 master 是否可达：`kubectl -n ai-gateway logs deploy/ai-gateway-agent`
- 检查 token 是否过期
- 检查 `agent.master_url` 是否为集群内可访问地址

### 7.2 SQLite 文件权限问题

- 确认 `/data` 挂载正常
- 确认容器运行用户对挂载目录有写权限

### 7.3 WebSocket 连接不稳定

- 检查集群网络策略
- 检查 Ingress/网关超时配置（如果通过网关转发 WS）

---

## 8. 生产建议

- 将 `master.jwt_secret` 与管理员密码改为 Secret 注入
- 给 API 配置 TLS（Ingress + cert-manager）
- 使用 NetworkPolicy 限制访问范围
- 为 master 配置资源请求/限制与监控告警
