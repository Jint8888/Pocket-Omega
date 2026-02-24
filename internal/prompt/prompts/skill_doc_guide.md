# MCP Server 创建规范（自定义工具指引）

> 本文件约束 agent 创建新自定义工具（MCP Server）时的全部行为。
> 每次创建工具**必须**严格按照本规范操作，不得省略任何步骤。
> 详细的语言模板和运行时选择规则见 `mcp_server_guide.md`，本文件侧重**规范约束**。

---

## 什么是自定义工具（MCP Server）

自定义工具是对某个库或能力的封装，以一组相关工具的形式注册到工具注册表，供 agent 调用。

同一 server 下的工具共享同一领域前缀（如 `excel_read`、`excel_write` 同属 Excel server）。

Agent 在工具列表中看到的注册名格式为 `mcp_<serverName>__<toolName>`（双下划线分隔）。

### 两种类型

| 类型 | 适用场景 | 代码位置 | 加载方式 |
|------|---------|---------|---------|
| **项目 Server** | Go 实现、需随项目编译、封装项目内部包 | `internal/tool/builtin/<name>.go` | 编译进二进制，main.go 手动注册 |
| **工作台 Server** | 任意语言（TypeScript / Python / Go）、热插拔 | `skills/<name>/` | mcp.json + mcp_server_add / mcp_reload |

**新工具首选工作台 Server（TypeScript）**，仅当需要封装项目内部 Go 包时选项目 Server。

---

## 类型 A：项目 Server（Go 编译型）

> 仅当需要封装项目内部 Go 包时使用。详细规范见 `skill_project_guide.md`。
> Agent 自建工具应优先选择类型 B（工作台 Server）。

---

## 类型 B：工作台 Server（热插拔型）

### 目录结构

```
skills/
└── <server-name>/              ← 目录名即 mcp.json 中的 server key
    ├── server.ts               ← TypeScript 实现（首选）
    │   或 server.py            ← Python 实现
    │   或 main.go + server.exe ← Go 实现（需先编译）
    ├── package.json            ← TypeScript 必须（含 @modelcontextprotocol/sdk）
    └── requirements.txt        ← Python 依赖声明（可选）
```

### 创建流程

> 详细创建流程和验证规程见 mcp_server_guide。

---

## 共用代码规范

### 工具命名

- 格式：`<领域>_<动作>`，全小写，下划线分隔
- 领域取核心能力语义，不取库名（用 `excel` 不用 `excelize`，用 `image` 不用 `imaging`）
- 同一 server 的多个工具共享同一领域前缀

| 好的命名 | 差的命名 | 原因 |
|---------|---------|------|
| `excel_read` | `excelize_read` | 领域语义比库名更稳定 |
| `excel_write` | `write_excel` | 领域在前，动作在后 |
| `image_resize` | `ResizeImage` | 必须全小写 |

---

### 工具描述

**格式约束：**
- 必须以动词开头
- 必须包含核心限制或使用前提
- 长度：20～80 字，中文

**好的示例：**
```
读取 Excel 工作表指定区域的单元格内容，以行列数组形式返回。
支持跨 Sheet 读取，文件大小上限 20MB。
```

**差的示例（及原因）：**
```
"读取 Excel 文件"                        → 太模糊，不知道返回格式
"基于 excelize 库，可读取 xlsx 文件..."   → 不要提实现细节
"Excel 读取工具，功能强大，支持多种功能"  → 无有效信息
```

---

### 参数描述

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
```

**差的示例：**
```
"单元格区域"          ← 没有格式说明
"如 A1:C10"          ← 没有约束说明
"Sheet 名称（可选）"  ← 没有默认值说明
```

---

### 错误信息格式

```
格式：<问题描述>: <具体值（如有）> — <用户下一步应该做什么>
语言：中文
```

**好的示例：**
```
"文件不存在: report.xlsx — 请先用 file_list 确认路径"
"单元格区域超出工作表范围 (最大行: 100) — 请用 excel_info 查询实际尺寸"
"文件过大 (25MB)，超过 20MB 上限 — 请拆分后分批处理"
```

**差的示例：**
```
"open report.xlsx: no such file or directory"  ← 暴露内部错误
"错误"                                         ← 无任何有效信息
"操作失败，请重试"                             ← 不告诉原因和下一步
```

---

### 安全与防护（必须实现）

每个读取文件的工具必须包含：
- 路径安全验证（防止工作区路径逃逸，参考 `file.go` 中的 `safeResolvePath`）
- 文件大小上限检查（推荐 20MB，视场景调整）
- 输出字数上限（推荐 8000 字符，防止 context 溢出）

每个涉及写操作的工具必须包含：
- 文件存在性确认（避免意外覆盖时给出明确提示）
- 参数合法性前置检查（在任何 IO 操作之前）

---

## 文档文件规范

| 类型 | 文档位置 | 说明 |
|------|---------|------|
| 类型 A（项目 Server） | `docs/skills/<name>.md` | 详细规范见 `skill_project_guide.md` |
| 类型 B（工作台 Server） | `skills/<name>/README.md` | 独立文档文件，模板见 mcp_server_guide |

---

## 注册规范

> 类型 A 注册规范见 `skill_project_guide.md`。

### 类型 B（工作台 Server）— mcp_server_add + mcp_reload

```
mcp_server_add:
  name="<server-name>"
  transport="stdio"
  command="node"
  args=["--import", "tsx", "skills/<name>/server.ts"]
  lifecycle="persistent"   ← 或 "per_call"（无状态极简工具用此）

mcp_reload                 ← 使改动生效
```

---

## 创建完成前的自查清单

> 类型 A 自查清单见 `skill_project_guide.md`。

### 类型 B（工作台 Server）检查门

**实现层**
- [ ] 工具命名格式为 `<领域>_<动作>`，全小写
- [ ] 工具描述以动词开头，含关键限制，20～80 字
- [ ] 每个参数描述包含三要素：格式、约束、示例
- [ ] TypeScript: `package.json` 已创建，`npm install` 已执行
- [ ] Python: `requirements.txt` 已创建（含 `mcp` 依赖）
- [ ] Go: `go build` 已成功执行，binary 存在

**安全层**
- [ ] 所有路径参数已做路径安全验证
- [ ] 文件大小上限已检查（如涉及文件操作）
- [ ] 无未捕获异常（try/catch 已包裹核心逻辑）
- [ ] 错误信息格式：`<问题描述>: <具体值> — <下一步行动>`，中文

**注册+验证层**
- [ ] `mcp_server_list` 已确认名称无冲突
- [ ] `mcp_server_add` 注册成功（无冲突错误）
- [ ] `mcp_reload` 返回成功，工具已出现在工具列表
- [ ] 调用工具验证功能正常（正常路径 + 异常路径各测一次）
