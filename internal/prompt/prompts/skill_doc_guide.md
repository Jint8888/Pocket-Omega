# Skill 创建规范

> 本文件约束 agent 创建新 Skill 时的全部行为。
> 每次创建 Skill 必须严格按照本规范产出所有要求的文件，不得省略任何章节。

---

## 什么是 Skill

Skill 是对某个库或能力的封装，以一组相关工具的形式注册到工具注册表，供 agent 调用。

同一 Skill 下的工具共享同一领域前缀（如 `excel_read`、`excel_write` 同属 Excel Skill）。

Skill 分为两类，选择哪类取决于实现语言和是否需要随项目编译：

| 类型 | 适用场景 | 代码位置 | 加载方式 |
|------|---------|---------|---------|
| **项目 Skill** | Go 实现、无运行时依赖 | `internal/tool/skill/<name>/` | 编译进二进制 |
| **工作台 Skill** | Python / TS / Go / 任意可执行程序 | `<workspace>/skills/<name>/` | 运行时发现，热插拔 |

---

## 类型 A：项目 Skill（Go 编译型）

### 必须产出的文件

```
internal/tool/skill/<name>/tool.go       # 实现（包名 skill<Name>）
internal/tool/skill/<name>/tool_test.go  # 测试
docs/skills/<name>.md                    # 使用说明（按模板填写）
cmd/omega/main.go                        # 注册代码（修改现有文件）
```

### 注册方式

```go
// cmd/omega/main.go
// Excel Skill — github.com/xuri/excelize/v2
if os.Getenv("SKILL_EXCEL_ENABLED") != "false" {
    registry.Register(skillexcel.NewReadTool(workspaceDir))
    registry.Register(skillexcel.NewWriteTool(workspaceDir))
    fmt.Println("📊 Excel skill enabled")
}
```

---

## 类型 B：工作台 Skill（workspace 热插拔型）

### 目录结构规则

```
<workspace>/skills/
└── <skill-name>/               ← 目录名即工具域名前缀
    ├── skill.yaml              ← 唯一必须文件（定义 + 文档合一）
    ├── main.py                 ← Python 实现（runtime: python）
    │   或 main.ts              ← TypeScript 实现（runtime: node）
    │   或 main.go + go.mod     ← Go 实现（runtime: go，主程序自动编译）
    │   或 skill.exe            ← 已编译二进制（runtime: binary）
    └── requirements.txt        ← Python 依赖（可选）
        package.json            ← Node 依赖（可选）
```

**规则说明：**
- `skill.yaml` 是唯一强制文件，缺少则该目录被忽略
- 目录名直接作为工具名的域前缀（`excel/` → 工具名以 `excel_` 开头）
- Go 实现时主程序自动执行 `go build`，编译产物存放在同目录下
- 执行文件名在 Windows 为 `skill.exe`，其他平台为 `skill`

### skill.yaml 完整格式

```yaml
# ── 工具定义（必填）────────────────────────────────────────
name: excel_read                          # 工具名，须以目录名为前缀
description: "读取 Excel 工作表指定区域内容，返回行列数组。文件上限 20MB。"
runtime: python                           # python | node | go | binary
entry: main.py                            # 入口文件；go 填 main.go；binary 填可执行文件名

parameters:
  - name: path
    type: string                          # string | integer | number | boolean
    required: true
    description: "Excel 文件路径（相对工作区）。示例：data/report.xlsx"

  - name: sheet
    type: string
    required: false
    default: ""
    description: "工作表名，默认第一个 Sheet。示例：Sheet1"

  - name: range
    type: string
    required: false
    default: ""
    description: "单元格区域，格式 A1:C10。默认读取全部。示例：A1:D20"

# ── 使用说明（供 LLM 和开发者阅读，可选但强烈建议）──────────
docs:
  when_to_use:
    - 需要读取 .xlsx 文件中的表格数据时
    - 用户上传了 Excel 报表需要分析时

  when_not_to_use:
    - CSV 文件请直接用 file_read
    - 只需查看文件结构用 excel_info 更轻量

  examples:
    - scenario: "读取销售报表前 10 行"
      arguments:
        path: "data/sales.xlsx"
        range: "A1:E10"
```

### 执行协议（stdio JSON）

主程序调用 skill 时通过 stdin/stdout 交换 JSON：

```
# 主程序 → 子进程（stdin，单行 JSON + 换行）
{"arguments": {"path": "data.xlsx", "sheet": "Sheet1"}}

# 子进程 → 主程序（stdout，单行 JSON + 换行）
{"output": "姓名,分数\n张三,95\n李四,88", "error": ""}

# 出错时
{"output": "", "error": "文件不存在: data.xlsx — 请先用 file_list 确认路径"}
```

**各语言实现模板：**

Python（`main.py`）：
```python
import sys, json

def run(arguments: dict) -> dict:
    # 实现逻辑
    return {"output": "结果", "error": ""}

if __name__ == "__main__":
    req = json.loads(sys.stdin.readline())
    result = run(req.get("arguments", {}))
    print(json.dumps(result, ensure_ascii=False))
```

TypeScript（`main.ts`）：
```typescript
import * as readline from "readline";

async function run(arguments: Record<string, unknown>) {
  // 实现逻辑
  return { output: "结果", error: "" };
}

const rl = readline.createInterface({ input: process.stdin });
rl.once("line", async (line) => {
  const req = JSON.parse(line);
  const result = await run(req.arguments ?? {});
  console.log(JSON.stringify(result));
  process.exit(0);
});
```

Go（`main.go`）：
```go
package main

import (
    "bufio"
    "encoding/json"
    "fmt"
    "os"
)

type Request struct {
    Arguments map[string]any `json:"arguments"`
}
type Response struct {
    Output string `json:"output"`
    Error  string `json:"error"`
}

func run(args map[string]any) Response {
    // 实现逻辑
    return Response{Output: "结果"}
}

func main() {
    scanner := bufio.NewScanner(os.Stdin)
    scanner.Scan()
    var req Request
    if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
        resp := Response{Error: fmt.Sprintf("参数解析失败: %v", err)}
        json.NewEncoder(os.Stdout).Encode(resp)
        return
    }
    json.NewEncoder(os.Stdout).Encode(run(req.Arguments))
}
```

---

## 两类 Skill 共用的代码规范

### 工具命名

- 格式：`<领域>_<动作>`，全小写，下划线分隔
- 领域取核心能力语义，不取库名（用 `excel` 不用 `excelize`，用 `image` 不用 `imaging`）
- 同一 Skill 的多个工具共享同一领域前缀

| 好的命名 | 差的命名 | 原因 |
|---------|---------|------|
| `excel_read` | `excelize_read` | 领域语义比库名更稳定 |
| `excel_write` | `write_excel` | 领域在前，动作在后 |
| `image_resize` | `ResizeImage` | 必须全小写 |

---

### 工具描述 `Description()`

**格式约束：**
- 必须以动词开头
- 必须包含核心限制或使用前提
- 长度：20～80 字
- 语言：中文

**好的示例：**
```
读取 Excel 工作表指定区域的单元格内容，以行列数组形式返回。
支持跨 Sheet 读取，文件大小上限 20MB。
```

**差的示例（及原因）：**
```
"读取 Excel 文件"
→ 太模糊，不知道返回什么格式

"这个工具基于 excelize 库，可以读取 xlsx 格式的 Excel 文件..."
→ 不要提实现细节，LLM 不需要知道用哪个库

"Excel 读取工具，功能强大，支持多种功能"
→ 废话，没有有效信息
```

---

### 参数描述（`InputSchema` 中每个 `SchemaParam.Description`）

每个参数描述必须包含以下**三要素**（缺一不可）：

```
① 是什么（格式 / 类型 / 含义）
② 有什么约束（范围 / 默认值 / 上限 / 必填说明）
③ 一个具体的例子
```

**好的示例：**
```
"要读取的单元格区域，格式为 A1:C10。跨 Sheet 写法：Sheet2!A1:B5。
 最大范围 1000 行 × 100 列。示例：A1:D20"

"工作表名称，默认第一个 Sheet。示例：Sheet1、销售数据"

"是否包含表头行（默认 true）。为 true 时第一行作为字段名返回"
```

**差的示例：**
```
"单元格区域"                ← 没有格式说明
"如 A1:C10"                ← 没有约束说明
"Sheet 名称（可选）"        ← 没有默认值说明
```

---

### 错误信息格式（`tool.ToolResult{Error: ...}`）

```
格式：<问题描述>: <具体值（如有）> — <用户下一步应该做什么>
语言：中文
```

**好的示例：**
```
"文件不存在: report.xlsx — 请先用 file_list 确认路径"
"单元格区域超出工作表范围 (最大行: 100) — 请用 excel_info 查询实际尺寸"
"文件过大 (25MB)，超过 20MB 上限 — 请拆分后分批处理"
"Sheet 不存在: 销售数据 — 可用 sheet 列表: [Sheet1, Sheet2]"
```

**差的示例（及原因）：**
```
"open report.xlsx: no such file or directory"   ← 英文 + 暴露 Go 内部错误
"错误"                                          ← 无任何有效信息
"操作失败，请重试"                              ← 不告诉原因和下一步
"runtime error: index out of range"             ← 绝对禁止暴露 panic 信息
```

---

### 安全与防护（必须实现）

每个读取文件的工具必须包含：
- `safeResolvePath` 路径安全验证（防止工作区逃逸）
- 文件大小上限检查（推荐 20MB，视场景调整）
- 输出字数上限（推荐 8000 字符，防止 context 溢出）

每个涉及写操作的工具必须包含：
- 文件存在性确认（避免意外覆盖时给出明确提示）
- 参数合法性前置检查（在任何 IO 操作之前）

---

## 文档文件规范

**文档位置取决于 Skill 类型：**

| 类型 | 文档位置 | 说明 |
|------|---------|------|
| 类型 A（项目 Skill） | `docs/skills/<name>.md` | 独立文档文件，参照模板 |
| 类型 B（工作台 Skill） | `skill.yaml` 的 `docs:` 节 | 定义与文档合一，无需单独 `.md` |

类型 A 的文档面向**未来需要创建类似 Skill 的 agent**，写作风格要求：结构化、有示例、可照抄。

参照 `docs/skills/_template.md` 中的模板填写，各部分要求如下：

### `## 概述` 部分
- 一句话说明这个 Skill 封装了什么库、暴露了哪些核心能力
- 列出依赖库和版本
- 说明注册条件（环境变量名）

### `## 工具列表` 部分
- 列出所有工具、一句话用途、注册条件
- 必须包含表格

### `## 何时使用 / 何时不用` 部分
- 至少 3 个典型使用场景
- 必须说明与相似工具的区别和边界
- 必须说明此 Skill 无法满足的需求

### `## 工具详细说明` 部分
每个工具独立一个子节，必须包含：
1. **参数表**（名称 / 类型 / 必填 / 默认值 / 说明）
2. **输出格式**（真实数据示例，不能只是抽象描述）
3. **错误一览**（错误信息 / 触发条件 / 解决方法 三列表格）

### `## 使用示例` 部分
- 必须有至少 **2 个不同场景**的完整示例
- 每个示例包含：场景描述 + 调用参数（JSON）+ 预期输出

### `## 裁剪决策` 部分
- 记录哪些库功能被暴露，哪些被排除及原因
- 这是给未来 agent 做参考的决策日志，不能省略

---

## 注册规范

### 类型 A（项目 Skill）— 手动注册到 main.go

在 `cmd/omega/main.go` 中注册，必须包含分组注释和环境变量控制：

```go
// Excel Skill — github.com/xuri/excelize/v2
// 通过 SKILL_EXCEL_ENABLED=false 禁用
if os.Getenv("SKILL_EXCEL_ENABLED") != "false" {
    registry.Register(skillexcel.NewReadTool(workspaceDir))
    registry.Register(skillexcel.NewWriteTool(workspaceDir))
    fmt.Println("📊 Excel skill enabled")
}
```

### 类型 B（工作台 Skill）— 自动发现，无需修改 main.go

只需确保以下条件满足，skill loader 会在启动和 reload 时自动注册：
- `<workspace>/skills/<name>/skill.yaml` 文件存在且格式有效
- `skill.yaml` 中 `entry` 字段指向的入口文件存在
- Go 实现时：`go.mod` 存在，代码可编译（loader 自动执行 `go build`）

---

## 创建完成前的自查清单

agent 提交 Skill 前，必须逐项确认以下检查门。**有任何一项未通过，不得视为完成。**

---

### 类型 A（项目 Skill）检查门

#### 代码层
- [ ] `Name()` 格式为 `<领域>_<动作>`，无大写，无连字符
- [ ] `Description()` 以动词开头，含关键限制，20～80 字
- [ ] 每个必填参数描述包含：格式、约束、示例（三要素）
- [ ] 每条错误信息包含：问题描述 + 下一步行动
- [ ] 有文件大小上限和输出字数上限保护
- [ ] `safeResolvePath` 已用于所有路径参数

#### 测试层
- [ ] 覆盖所有错误路径（空参数、路径逃逸、文件不存在、超限）
- [ ] 正常路径至少 1 个端到端测试（使用真实文件）
- [ ] 有 GUI 副作用的测试已用 mock/no-op 替换（参考 `file_open_test.go`）
- [ ] `go test ./...` 全部通过，覆盖率 ≥ 80%

#### 文档层
- [ ] `docs/skills/<skill>.md` 已按模板创建
- [ ] 包含「何时不用」和「替代工具」说明
- [ ] 输出格式示例使用真实数据（非抽象描述）
- [ ] 裁剪决策已记录

#### 注册层
- [ ] `cmd/omega/main.go` 已注册，含分组注释和库来源（如 `// Excel Skill — github.com/xuri/excelize/v2`）
- [ ] import 包名为 `skill<Name>`（如 `skillexcel`），而非 `builtin`
- [ ] 通过环境变量 `SKILL_<NAME>_ENABLED` 控制开关
- [ ] 启动日志中打印注册状态

---

### 类型 B（工作台 Skill）检查门

#### skill.yaml 层
- [ ] `name` 格式为 `<领域>_<动作>`，与目录名前缀一致
- [ ] `description` 以动词开头，含关键限制，20～80 字
- [ ] 每个参数的 `description` 包含：格式、约束、示例（三要素）
- [ ] `runtime` 与 `entry` 填写正确，入口文件实际存在
- [ ] `docs.when_to_use` 和 `docs.when_not_to_use` 已填写

#### 实现层
- [ ] 入口文件严格遵守 stdio JSON 协议（单行读入 / 单行输出）
- [ ] 错误信息格式：`<问题描述>: <具体值> — <下一步行动>`，中文
- [ ] 无未捕获异常（Python `try/except`，TS `try/catch`，Go `recover`）
- [ ] Go 实现：有 `go.mod`，`go build` 可成功编译

#### 验证层
- [ ] 手动测试：通过 `echo '{"arguments":{...}}' | python main.py` 或同等命令验证输出
- [ ] 异常测试：传入无效参数时返回 `{"output":"","error":"..."}` 而非崩溃
- [ ] reload 后工具出现在工具列表中（`mcp_reload` 或重启验证）
