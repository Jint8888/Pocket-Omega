## 组件知识

workspace — sandbox 根目录，文件工具的操作范围。包含 `rules.md`、`soul.md`、`mcp.json`、`prompts/`（覆盖内置提示词）、`skills/`（自建 MCP server）。

sandbox 约束 — `file_read`/`file_write`/`file_list`/`file_patch`/`file_delete`/`file_move` 等文件工具**只能操作 workspace 目录内的文件**。任何 workspace 外的路径（包括项目根目录、系统目录等）都会被拒绝。操作 workspace 外的文件必须用 `shell_exec`（如 `type`/`cat` 读取、`echo >` 写入）。`.env` 可通过 `config_edit`（白名单机制）编辑。**常见错误**：用 `file_read` 读取 `.env` 或项目根目录文件 → 会被 sandbox 拒绝，应改用 `config_edit` 或 `shell_exec`。

.env — 位于项目根目录（非 workspace），存放 `WORKSPACE_DIR`、`LLM_MODEL`、`LLM_BASE_URL` 等配置。程序启动时读取，修改后需重启生效。用 `config_edit` 的 `set` 操作更新。

shell 环境 — 当前系统为 **{{OS}}**，`shell_exec` 使用 `{{SHELL_CMD}}` 执行命令。Windows 下注意：PowerShell 不支持 `&&`，用 `;` 或 `if/else` 替代；路径分隔符用 `\`；常用命令对照：`dir`（非 `ls`）、`type`（非 `cat`）、`copy`（非 `cp`）、`move`（非 `mv`）。

mcp 系统 — `mcp.json` 定义外部 MCP server 配置。**添加/移除/修改 server 必须用 `mcp_server_add`/`mcp_server_remove` 工具，禁止用 `file_write`/`file_patch` 或任何文件编辑工具直接修改 mcp.json**（直接编辑会破坏 JSON 格式化）。修改后调用 `mcp_reload` 热更新（无需重启）。自建工具必须通过 MCP Server 实现，创建规范见后续 MCP 指引。

热更新 — `mcp_reload` 工具同时刷新 MCP 连接和提示词缓存。rules.md 修改后必须调用 `mcp_reload` 才能生效。stdio 类型的 MCP server 不能用 `shell_exec` 直接运行测试——它们会阻塞在 stdin 等待 JSON-RPC 输入。要验证 server 是否正常，用 `mcp_reload` 后观察 connected 数量和工具列表变化。

workspace 迁移 — 新路径的文件操作必须通过 `shell_exec`（sandbox 限制），不能用 file 类工具。核心文件：`mcp.json`、`rules.md`、`soul.md`、`skills/`、`prompts/`。迁移后用 `config_edit` 更新 `.env` 中的 `WORKSPACE_DIR`，提醒用户重启。不主动删除旧 workspace 文件。

git_info — 只读 Git 查询工具。支持 status/diff/log/branch/stash/show。查看变更：`git_info(command="status")` 或 `git_info(command="diff", path="file.go")`。查看历史：`git_info(command="log")` 默认最新 20 条。查看提交：`git_info(command="show", args="<hash>")`；查看指定文件：`args="<hash>:path/to/file"`（path 参数对 show/branch 无效）。无需用 `shell_exec` 运行 git 命令——`git_info` 更安全且 shell 禁用时仍可用。

Python 依赖安装 — 项目使用 `uv` 作为 Python 包管理器。正确用法：`uv pip install -r requirements.txt`（直接命令行调用）。**常见错误**：`python -m uv` → uv 不是 Python 模块，不能通过 `-m` 调用；`python -m pip install` → 项目统一用 uv，不要用 pip。安装到 venv 时确保先激活或指定 `--python` 参数。
