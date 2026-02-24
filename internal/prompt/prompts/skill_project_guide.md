# 类型 A：项目 Server（Go 编译型）规范

> 仅当需要封装项目内部 Go 包时使用。Agent 自建工具应优先选择类型 B（工作台 Server）。
> 本文件不自动加载到系统提示词，需要时通过 file_read 按需读取。

---

## 必须产出的文件

```
internal/tool/builtin/<name>.go       # 实现（实现 tool.Tool 接口）
internal/tool/builtin/<name>_test.go  # 测试
docs/skills/<name>.md                 # 使用说明（按模板填写）
cmd/omega/main.go                     # 注册代码（修改现有文件）
```

## 实现接口

```go
// internal/tool/builtin/<name>.go
package builtin

type MyTool struct { workspaceDir string }

func NewMyTool(workspaceDir string) *MyTool { return &MyTool{workspaceDir: workspaceDir} }

func (t *MyTool) Name() string             { return "domain_action" }
func (t *MyTool) Description() string      { return "..." }
func (t *MyTool) InputSchema() json.RawMessage { return tool.BuildSchema(...) }
func (t *MyTool) Execute(ctx context.Context, args json.RawMessage) (tool.ToolResult, error) { ... }
func (t *MyTool) Init(_ context.Context) error { return nil }
func (t *MyTool) Close() error                 { return nil }
```

## 注册方式

```go
// cmd/omega/main.go
// Excel 工具 — github.com/xuri/excelize/v2
// 通过 SKILL_EXCEL_ENABLED=false 禁用
if os.Getenv("SKILL_EXCEL_ENABLED") != "false" {
    registry.Register(builtin.NewExcelReadTool(workspaceDir))
    registry.Register(builtin.NewExcelWriteTool(workspaceDir))
    fmt.Println("📊 Excel tools enabled")
}
```

<!-- PLACEHOLDER_DOCS -->

---

## 文档规范

类型 A 文档位置：`docs/skills/<name>.md`，面向**未来需要创建类似工具的 agent**，风格要求：结构化、有示例、可照抄。

### `docs/skills/<name>.md` 必须包含

#### `## 概述`
- 一句话说明封装了什么库、暴露了哪些核心能力
- 列出依赖库和版本
- 说明注册条件（环境变量名）

#### `## 工具列表`
- 所有工具、一句话用途、注册条件（必须包含表格）

#### `## 何时使用 / 何时不用`
- 至少 3 个典型使用场景
- 与相似工具的区别和边界
- 此工具无法满足的需求

#### `## 工具详细说明`
每个工具独立子节，必须包含：
1. **参数表**（名称 / 类型 / 必填 / 默认值 / 说明）
2. **输出格式**（真实数据示例，不能只是抽象描述）
3. **错误一览**（错误信息 / 触发条件 / 解决方法 三列表格）

#### `## 使用示例`
- 至少 **2 个不同场景**的完整示例
- 每个示例：场景描述 + 调用参数（JSON）+ 预期输出

#### `## 裁剪决策`
- 记录哪些库功能被暴露，哪些被排除及原因
- 这是给未来 agent 做参考的决策日志，不能省略

---

## 注册规范

在 `cmd/omega/main.go` 中注册，必须包含分组注释和环境变量控制：

```go
// Excel 工具 — github.com/xuri/excelize/v2
// 通过 SKILL_EXCEL_ENABLED=false 禁用
if os.Getenv("SKILL_EXCEL_ENABLED") != "false" {
    registry.Register(builtin.NewExcelReadTool(workspaceDir))
    registry.Register(builtin.NewExcelWriteTool(workspaceDir))
    fmt.Println("📊 Excel tools enabled")
}
```

---

## 自查清单

**代码层**
- [ ] `Name()` 格式为 `<领域>_<动作>`，无大写，无连字符
- [ ] `Description()` 以动词开头，含关键限制，20～80 字
- [ ] 每个必填参数描述包含三要素：格式、约束、示例
- [ ] 每条错误信息包含：问题描述 + 下一步行动
- [ ] 有文件大小上限和输出字数上限保护
- [ ] `safeResolvePath` 已用于所有路径参数

**测试层**
- [ ] 覆盖所有错误路径（空参数、路径逃逸、文件不存在、超限）
- [ ] 正常路径至少 1 个端到端测试（使用真实文件）
- [ ] `go test ./...` 全部通过

**文档层**
- [ ] `docs/skills/<name>.md` 已按上方文档规范创建
- [ ] 包含「何时不用」和「替代工具」说明
- [ ] 输出格式示例使用真实数据
- [ ] 裁剪决策已记录

**注册层**
- [ ] `cmd/omega/main.go` 已注册，含分组注释和库来源
- [ ] 通过环境变量 `SKILL_<NAME>_ENABLED` 控制开关
- [ ] 启动日志中打印注册状态
