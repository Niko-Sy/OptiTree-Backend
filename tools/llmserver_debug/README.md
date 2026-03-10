# Mock LLM Server

这是一个专门给 Go 侧 `internal/ai/llm_server.go` 联调用的 FastAPI 调试服务。

它实现了两个接口：

- `POST /generate/fault-tree`
- `POST /generate/knowledge-graph`

返回格式与 Go 客户端当前约定一致，使用 SSE：

- `data: {"type":"progress", ...}`
- `data: {"type":"result", ...}`
- `data: {"type":"error", ...}`
- `data: [DONE]`

## 运行

在当前目录执行：

```powershell
cd tools/llmserver_debug
python -m venv .venv
.\.venv\Scripts\Activate.ps1
pip install -r requirements.txt
python app.py
```

默认监听：

- `http://127.0.0.1:8001`

可以通过环境变量调整：

- `LLM_DEBUG_HOST`
- `LLM_DEBUG_PORT`
- `LLM_DEBUG_DELAY`

例如：

```powershell
$env:LLM_DEBUG_PORT = "8001"
$env:LLM_DEBUG_DELAY = "0.1"
python app.py
```

## 对接 Go 服务

确认你的主服务配置里：

```yaml
llm_server:
  base_url: "http://127.0.0.1:8001"
```

然后启动 Go 服务即可。

## 测试示例

故障树：

```powershell
curl -N -X POST http://127.0.0.1:8001/generate/fault-tree `
  -H "Content-Type: application/json" `
  -d '{"documents":["液压系统说明文档"],"top_event":"液压系统压力不足","config":{"quality":"balanced","model":"qwen-plus","depth":4,"max_nodes":30}}'
```

知识图谱：

```powershell
curl -N -X POST http://127.0.0.1:8001/generate/knowledge-graph `
  -H "Content-Type: application/json" `
  -d '{"documents":["液压系统说明文档"],"config":{"quality":"balanced","model":"qwen-plus","entity_types":["component","event","cause"]}}'
```

## 模拟错误

如果请求中的 `config.model` 传入下面任意值，将返回 error 事件：

- `fail`
- `error`
- `mock-error`

这样可以直接测试 Go 端错误处理逻辑。