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

## 创建流程（必须严格按顺序执行）

```
Step 1  调用 mcp_server_list，确认目标名称尚未注册（避免冲突）
Step 2  按运行时规则选择语言模板
Step 3  使用 file_write 创建实现文件（TypeScript: server.ts + package.json）
Step 4  执行依赖安装（TypeScript: npm install；Python: 用户手动 pip install）
Step 5  调用 mcp_server_add 注册到 mcp.json
Step 6  调用 mcp_reload 热加载
Step 7  调用新工具验证功能
Step 8  回报完成
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

对应 `mcp_server_add` 调用：
```
name="<server-name>", transport="stdio",
command="node", args=["--import", "tsx", "skills/<name>/server.ts"],
lifecycle="persistent"
```

---

## Python 模板（依赖 Python 生态时使用）

> **⚠️ 依赖说明**：Python 依赖需用户手动安装（`pip install mcp`），
> Agent 不自动执行 pip install。须在 `requirements.txt` 中声明依赖。

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

对应 `mcp_server_add` 调用（按平台选择 command）：
- **Windows**：`command=".venv/Scripts/python.exe"`
- **Linux/Mac**：`command=".venv/bin/python"`

```
name="<server-name>", transport="stdio",
command=".venv/Scripts/python.exe",  ← Linux/Mac 改为 .venv/bin/python
args=["skills/<name>/server.py"],
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

对应 `mcp_server_add` 调用：
```
name="<server-name>", transport="stdio",
command="skills/<name>/server.exe", args=[],
lifecycle="persistent"
```

---

## 生命周期选择

| lifecycle | 适用场景 | 特点 |
|---|---|---|
| `persistent`（默认） | 有初始化开销（pandas、ML 模型、数据库连接） | 进程常驻，复用连接，零冷启动 |
| `per_call` | 极简无状态工具 | 每次调用新起进程，调完退出，零进程管理 |

---

## 工具命名规范

- 格式：`<领域>_<动作>`，全小写，下划线分隔
- 同一 server 下的多个工具共享同一领域前缀（如 `excel_read`、`excel_write`）
- **MCP 适配器注册名**格式为 `mcp_<serverName>__<toolName>`（双下划线分隔）

---

## 创建完成前的自查清单

- [ ] mcp_server_list 已确认名称无冲突
- [ ] 按运行时规则选择了正确的语言模板
- [ ] TypeScript 路径：package.json 已创建，npm install 已执行
- [ ] Python 路径：requirements.txt 已创建（含 mcp 依赖），提示用户手动安装
- [ ] Go 路径：go build 已成功执行，binary 存在
- [ ] mcp_server_add 注册成功（无冲突错误）
- [ ] mcp_reload 返回成功，工具已出现在工具列表
- [ ] 调用工具验证功能正常
