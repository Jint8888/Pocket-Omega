# MCP Server 创建规范

> 本文件约束 agent 创建新 MCP Server 时的全部行为。
> 新工具**必须**使用 MCP 协议（而非 Legacy Skill 私有协议），严格按本规范操作。

---

## 运行时环境

{{RUNTIME_ENV}}

---

## 运行时选择规则（Agent 必须遵守，不得自行推理）

| 语言 | 运行时 | 调用方式 | 说明 |
|---|---|---|---|
| **TypeScript（默认）** | tsx | `node --import tsx server.ts` | 有类型、无编译步骤，**新工具首选** |
| Python | .venv | `.venv/Scripts/python.exe server.py` | 依赖 Python 生态时使用 |
| Go | binary | `skills/<name>/server[.exe]` | 需高性能或复用项目 Go 代码时使用 |
| JS | node | `node server.js` | 仅当 tsx 不可用时的降级选项 |

**选择逻辑**（读取上方 [运行时环境] 区块，不得自行探测）：
- `tsx 可用` → **必须**使用 TypeScript（`server.ts`），禁止选用 `server.js`
- `tsx 安装中` → 等待或使用 Python / Go；不得使用 tsx 路径
- `tsx 不可用，node 可用` → 降级使用 JavaScript（`server.js`）
- `node 不可用` → 使用 Python 或 Go 模板

> 编译型 TypeScript（tsc + dist/）不在 Agent 自建范围内，由人工维护。

---

## 创建流程（必须严格按顺序执行，每步完成后立即执行下一步）

> ⚠️ **执行纪律**：用 update_plan(set) 设置计划后，**立即**从 Step 1 开始执行。
> 每完成一步，在 reason 中用 `[plan:步骤ID:done]` 标记完成，然后**立即**执行下一步。
> **禁止**在步骤之间重复调用 update_plan(set)。

```
Step 1  调用 mcp_server_list，确认目标名称尚未注册 → 完成后立即进入 Step 2
Step 2  按运行时规则选择语言模板（纯决策，无需工具调用）→ 立即进入 Step 3
Step 3  使用 file_write 创建实现文件（TypeScript: server.ts + package.json）→ 立即进入 Step 4
Step 4  执行依赖安装（TypeScript: npm install；Python: uv pip install -r requirements.txt）→ 立即进入 Step 5
Step 5  调用 mcp_server_add 注册到 mcp.json（⚠️ command 和 args 中的路径必须使用绝对路径）→ 立即进入 Step 6
Step 6  调用 mcp_reload 热加载 → 立即进入 Step 7
Step 7  验证功能（⚠️ 严格按下方验证规程执行，不要自行发挥）→ 立即进入 Step 8
Step 8  创建 skills/<name>/README.md 使用说明文档（模板见下方）→ 立即进入 Step 9
Step 9  answer 回报完成（计划状态已通过侧信道自动更新）
```

### Step 7 验证规程（必须严格遵守，禁止自行发挥）

验证分两步，最多消耗 3 个工具调用：

**7a. 确认连接状态**：检查 Step 6 `mcp_reload` 的返回值。
- 如果返回 `+N connected`（N≥1），说明 server 进程已启动并连接成功，进入 7b
- 如果返回 `+0 connected` 或报错，说明启动失败，参考下方「连接失败诊断」排查

**7b. 调用工具验证**：直接调用 `mcp_<serverName>__<toolName>` 执行一次简单测试。
- 工具调用名 = `mcp_` + mcp_server_add 时的 name + `__` + server 代码中定义的工具名
- 示例：name=`crawl4ai`，工具=`crawl_url` → `mcp_crawl4ai__crawl_url`
- ⚠️ serverName 必须与 mcp_server_add 的 name 参数**完全一致**，不要省略或修改任何部分
- 使用最简参数测试即可，不需要复杂输入
- 如果工具调用成功（无论返回内容是否完美），验证通过，进入 Step 8
- 如果工具调用失败，记录错误信息，仍然进入 Step 8（在 README 中注明已知问题）

**⚠️ 验证阶段禁止事项**：
- 禁止用 `find` 搜索工具名（find 搜索的是文件系统，不是工具列表）
- 禁止调用其他 server 的工具来"测试"（如 enhanced-search、github 等与当前 skill 无关）

---

## README.md 模板（Step 8 必须创建）

Agent 验证工具功能后，**必须**用 `file_write` 创建 `skills/<name>/README.md`：

```markdown
# <server-name>

> 一句话描述此 MCP Server 的用途。

## 工具列表

### `tool_name_1`

**用途**：简要描述工具功能。

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| param1 | string | ✅ | 参数描述 |
| param2 | number | ❌ | 参数描述（默认值：0） |

**示例调用**：
```
tool_name_1(param1="example", param2=5)
```

**返回格式**：描述返回值的结构和含义。

## 依赖

- 运行时：Python 3.x / Node.js / Go
- 关键依赖：列出主要第三方库

## 注意事项

- 已知限制、常见错误、性能注意点
```

---

## TypeScript / tsx 模板（默认，新工具首选）

> **⚠️ 依赖说明**：`server.ts` 依赖 `@modelcontextprotocol/sdk` 和 `zod`。
> Agent 创建 `server.ts` 时**必须**同时创建 `package.json` 并执行 `npm install`，
> 否则 `node --import tsx server.ts` 将因找不到包而失败。

### 必须创建的文件

**`skills/<name>/package.json`**（Agent 需随 server.ts 一起创建）：
```jsonc
{
  "type": "module",
  "dependencies": {
    "@modelcontextprotocol/sdk": "^1.0.0",
    "zod": "^3.0.0"
  }
}
```

Agent 创建文件后**必须**执行安装：
```bash
cd skills/<name> && npm install
```

**`skills/<name>/server.ts`**：
```typescript
import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

const server = new McpServer({ name: "<server-name>", version: "1.0.0" });

server.tool(
  "tool_name",
  // 参数 Schema（Zod）：类型即文档，Agent 可精确表达约束
  {
    param1: z.string().describe("参数描述（是什么 + 约束 + 示例）"),
    param2: z.number().int().default(0).describe("参数描述"),
  },
  async ({ param1, param2 }) => {
    try {
      // 实现逻辑
      const result = `处理结果: ${param1}`;
      return { content: [{ type: "text", text: result }] };
    } catch (e) {
      // 错误格式：<问题>: <值> — <下一步>
      const msg = e instanceof Error ? e.message : String(e);
      return { content: [{ type: "text", text: `操作失败: ${msg} — 请检查参数后重试` }], isError: true };
    }
  }
);

const transport = new StdioServerTransport();
await server.connect(transport);
```

对应 `mcp_server_add` 调用（⚠️ command 和 args 中的文件路径必须使用绝对路径）：
```
name="<server-name>", transport="stdio",
command="node", args=["--import", "tsx", "{WORKSPACE_DIR}/skills/<name>/server.ts"],
lifecycle="persistent"
```

---

## Python 模板（依赖 Python 生态时使用）

> **⚠️ 依赖说明**：Python 依赖须在 `requirements.txt` 中声明，
> Agent 使用 `uv pip install -r requirements.txt` 自动安装（在 mcp_server_add 之前）。

**`skills/<name>/server.py`**：
```python
from mcp.server.fastmcp import FastMCP

mcp = FastMCP("<server-name>")

@mcp.tool()
def tool_name(param1: str, param2: int = 0) -> str:
    """工具描述（20-80 字，中文，以动词开头）。

    Args:
        param1: 参数描述（是什么 + 约束 + 示例）
        param2: 参数描述
    """
    try:
        return "结果"
    except Exception as e:
        raise ValueError(f"操作失败: {e} — 请检查参数后重试")

if __name__ == "__main__":
    mcp.run()
```

对应 `mcp_server_add` 调用（⚠️ command 和 args 中的文件路径必须使用绝对路径，通过 WORKSPACE_DIR 拼接）：
- **Windows**：`command="{WORKSPACE_DIR}/.venv/Scripts/python.exe"`
- **Linux/Mac**：`command="{WORKSPACE_DIR}/.venv/bin/python"`

```
name="<server-name>", transport="stdio",
command="{WORKSPACE_DIR}/.venv/Scripts/python.exe",  ← 替换为实际绝对路径
args=["{WORKSPACE_DIR}/skills/<name>/server.py"],    ← args 也必须用绝对路径
lifecycle="persistent"
```

---

## Go 模板（需高性能或复用项目 Go 代码时使用）

> **⚠️ 编译步骤**：Go 实现需 Agent 执行编译命令后才能运行。

**`skills/<name>/main.go`**：
```go
package main

import (
    "context"
    "github.com/mark3labs/mcp-go/mcp"
    "github.com/mark3labs/mcp-go/server"
)

func main() {
    s := server.NewMCPServer("<server-name>", "1.0.0")
    s.AddTool(mcp.NewTool("tool_name",
        mcp.WithDescription("工具描述"),
        mcp.WithString("param1", mcp.Required(), mcp.Description("参数描述")),
    ), handleToolName)
    server.ServeStdio(s)
}

func handleToolName(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    param1 := req.Params.Arguments["param1"].(string)
    return mcp.NewToolResultText("结果"), nil
}
```

Go 需要额外编译步骤（Agent 执行）：
```bash
cd skills/<name> && go build -o server.exe .   # Windows
cd skills/<name> && go build -o server .        # Unix
```

对应 `mcp_server_add` 调用（⚠️ command 和 args 中的文件路径必须使用绝对路径）：
```
name="<server-name>", transport="stdio",
command="{WORKSPACE_DIR}/skills/<name>/server.exe", args=[],  ← 替换为实际绝对路径
lifecycle="persistent"
```

---

## 生命周期选择

| lifecycle | 适用场景 | 特点 |
|---|---|---|
| `persistent`（默认） | 有初始化开销（pandas、ML 模型、数据库连接） | 进程常驻，复用连接，零冷启动 |
| `per_call` | 极简无状态工具 | 每次调用新起进程，调完退出，零进程管理 |

---

> 工具命名规范见 skill_doc_guide 中的「工具命名」章节。

---

> 完整自查清单见 skill_doc_guide 中的「创建完成前的自查清单」章节。

---

## MCP Server 连接失败诊断

当 `mcp_reload` 后 server 未连接（connected +0）或工具调用报 transport error 时，按以下顺序排查：

1. **检查 command 路径是否存在**：用 `shell_exec` 执行 `where <command>`（Windows）或 `which <command>`（Unix），确认可执行文件存在
2. **检查 args 中的文件路径**：确认 server.ts / server.py / server.exe 的**绝对路径**正确，文件确实存在
3. **检查依赖是否安装**：
   - TypeScript：`skills/<name>/node_modules/` 目录是否存在（需先 `npm install`）
   - Python：`uv pip install -r requirements.txt` 是否执行成功（⚠️ 不要用 `python -m uv`）
   - Go：binary 是否已编译（`go build` 是否执行）
4. **手动测试启动**：用 `shell_exec` 运行 command + args，观察 stderr 输出。注意：stdio server 会阻塞等待 stdin，看到启动无报错即可 Ctrl+C
5. **检查 mcp.json 注册**：用 `mcp_server_list` 确认 server 已注册，command/args 与预期一致
6. **常见错误模式**：
   - `MODULE_NOT_FOUND`：npm install 未执行或 package.json 缺少依赖
   - `No module named 'mcp'`：Python 依赖未安装，执行 `uv pip install mcp`
   - `tsx: not found`：tsx 未全局安装，检查 `node --import tsx` 是否可用
   - 路径含空格：确保 args 中的路径用引号包裹或无空格
