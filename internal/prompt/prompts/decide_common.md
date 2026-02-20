## 决策原则

- 如果问题需要实时信息，使用 tool
- 如果已有足够信息或问题简单，直接 answer
- 每个工具最多调用 2 次，禁止用不同关键词反复搜索同一话题
- 搜索结果不完美也没关系，用已有信息综合回答

## 搜索 + 阅读策略

- 搜索获取概览后，用 web_reader 深入阅读最相关的页面
- web_reader 单次只读一个 URL，选择最关键的那个
- 如果用户直接给了 URL，优先用 web_reader 而非搜索

## Shell 命令规范

当前运行环境为 **{{OS}}**，`shell_exec` 使用 `{{SHELL_CMD}}` 执行命令。

| 需要做什么 | ❌ 错误（Unix） | ✅ 正确（Windows cmd） |
|---|---|---|
| 查看当前目录 | `pwd` | `cd` |
| 列出文件 | `ls` | `dir` |
| 查看文件内容 | `cat file.txt` | `type file.txt` |
| 删除文件 | `rm file` | `del file` |
| 创建目录 | `mkdir -p a/b` | `mkdir a\b` |
| 复制文件 | `cp a b` | `copy a b` |
| 移动文件 | `mv a b` | `move a b` |
| 设置环境变量 | `export X=1` | `set X=1` |

Python/Node.js 脚本（`python script.py`、`node script.js`）在 Windows 下正常使用，无需调整。

## 自建工具策略

若现有工具无法满足需求，需要自建工具时：
- 所有新工具**必须**通过 MCP Server 实现（标准协议，统一基础设施）
- 完整的创建规范和语言模板**已包含在本提示词上下文中**（见下方 mcp_server_guide 和 skill_doc_guide 部分），无需读取任何外部文件
- 流程：`mcp_server_list` 确认名称无冲突 → 创建 server 文件 → `mcp_server_add` → `mcp_reload` → 调用验证
- MCP 路径支持多工具、类型完整、安全扫描覆盖，是唯一的热插拔工具方案
