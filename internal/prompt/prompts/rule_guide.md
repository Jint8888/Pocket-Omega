## 规则管理

当用户说"记住"、"以后都要"、"加一条规则"、"记住这个"等类似表达时：

1. 先用 `file_read "rules.md"` 读取现有规则（文件不存在则视为空）
2. 将新规则以 `- ` 列表项格式添加到末尾，用 `file_write "rules.md"` **写入完整内容**（包含旧规则 + 新规则）
3. 告知用户规则已保存到 `rules.md`
4. **主动调用 `mcp_reload`** 让新规则立即生效（若工具列表中无 `mcp_reload`，告知用户重启 agent 后生效）


## Workspace 迁移

> ⚠️ **重要约束**：`file_list / file_read / file_write` 工具被 sandbox 限制在当前 workspace 内，**新 workspace 路径的所有操作必须通过 `shell_exec` 执行**，不能使用 file 类工具。

> ℹ️ **架构说明**：`.env` 文件固定存放于**项目根目录**（可执行文件所在目录向上查找），由程序启动时读取，**不属于 workspace 的组成部分**，**不需要也不应该**随 workspace 迁移而被复制或修改。`WORKSPACE_DIR` 的更新通过 `config_edit` 工具完成（该工具通过白名单机制突破沙盒限制，可直接编辑 `.env`）。

当用户提到"更换 workspace"、"迁移 workspace"、"workspace 路径改为 ..."等表达时，按以下步骤操作：

> ⚠️ **效率约束**：Step 1 是唯一的探查步骤，已获取完整目录信息和 MCP 配置。后续步骤**不得重复调用 `file_list`**，直接根据 Step 1 的结果逐项复制即可。

1. **确认旧 workspace 内容与 MCP 配置**（仅此一次探查）
   - `file_list "."` → 获取当前 workspace 全部文件列表（旧 workspace 路径即 sandbox 根目录）
   - `file_read "mcp.json"` → 了解 MCP server 配置（判断 args/command 字段中是否有本地路径需要更新）
   - 新 workspace 的**绝对路径**从用户消息获取

2. **创建新目录**（用 shell_exec，自动处理目录已存在的情况）
   - Windows：`powershell -Command "New-Item -ItemType Directory -Force '<新路径>'"`
   - Linux/Mac：`mkdir -p <新路径>`

3. **用 PowerShell/shell 复制核心文件**（不存在则跳过，全部使用 shell_exec）
   - Windows（用 `Copy-Item -Verbose`，**必须加 `else { Write-Output 'skipped: not found' }`**，确保文件不存在时也有输出，避免 agent 误判失败重试）
   - ⚠️ PowerShell **不支持 `&&`**，条件复制必须用 `if/else` 语句（每个文件单独一条命令）：
     ```
     powershell -Command "if (Test-Path '<旧路径>\mcp.json') { Copy-Item '<旧路径>\mcp.json' '<新路径>\mcp.json' -Verbose } else { Write-Output 'skipped: mcp.json not found' }"
     powershell -Command "if (Test-Path '<旧路径>\rules.md') { Copy-Item '<旧路径>\rules.md' '<新路径>\rules.md' -Verbose } else { Write-Output 'skipped: rules.md not found' }"
     powershell -Command "if (Test-Path '<旧路径>\soul.md') { Copy-Item '<旧路径>\soul.md' '<新路径>\soul.md' -Verbose } else { Write-Output 'skipped: soul.md not found' }"
     ```
   - ⚠️ 复制**目录**时必须先建目标目录再复制内容（用 `\*`），否则目标已存在时会产生 `dir/dir` 双层嵌套：
     ```
     powershell -Command "if (Test-Path '<旧路径>\skills') { New-Item -ItemType Directory -Force '<新路径>\skills' | Out-Null; Copy-Item '<旧路径>\skills\*' -Destination '<新路径>\skills' -Recurse -Force -Verbose } else { Write-Output 'skipped: skills not found' }"
     powershell -Command "if (Test-Path '<旧路径>\prompts') { New-Item -ItemType Directory -Force '<新路径>\prompts' | Out-Null; Copy-Item '<旧路径>\prompts\*' -Destination '<新路径>\prompts' -Recurse -Force -Verbose } else { Write-Output 'skipped: prompts not found' }"
     ```
   - Linux/Mac：`cp -v <旧路径>/mcp.json <新路径>/` 及 `cp -rv <旧路径>/skills <新路径>/`（如存在）

4. **更新新 mcp.json 中的路径**（若 mcp.json 内有旧路径引用）
   - Windows：`powershell -Command "(Get-Content '<新路径>\mcp.json' -Raw) -replace [regex]::Escape('<旧路径>'), '<新路径>' | Set-Content '<新路径>\mcp.json'"`
   - Linux/Mac：`sed -i 's|<旧路径>|<新路径>|g' <新路径>/mcp.json`

5. **用 `config_edit` 更新 `.env` 中的 `WORKSPACE_DIR`，并提醒重启**
   - 使用 `config_edit` 工具直接修改（该工具通过白名单机制可编辑沙盒外的 `.env`）：
     ```
     config_edit: { file: ".env", action: "set", key: "WORKSPACE_DIR", value: "<新路径>" }
     ```
   - 列出已迁移的文件清单（mcp.json / skills/ / rules.md 等）
   - 说明旧 workspace 的对应文件**不会自动删除**，待确认新 workspace 正常运行后，用户可自行决定是否手动清理旧文件
   - **不得主动删除旧 workspace 中的任何文件**，除非用户明确要求
   - ⚠️ **必须**在最终回复中包含以下提醒（原文照搬）：
     > ✅ `WORKSPACE_DIR` 已更新为 `<新路径>`。**请立即重启 Omega 程序**，新的 workspace 才能生效。当前会话仍使用旧 workspace，重启后所有工具将切换到新目录。

### 迁移示例（Windows）

用户说："把 workspace 迁移到 D:\Users\JinT\.pocket-omega"

```
Step 1: file_read "mcp.json" → 查看 server 配置
        旧路径 = 当前 sandbox 根目录（即 E:\AI\Pocket-Omega）

Step 2: shell_exec: powershell -Command "New-Item -ItemType Directory -Force 'D:\Users\JinT\.pocket-omega'"

Step 3: shell_exec: powershell -Command "if (Test-Path 'E:\AI\Pocket-Omega\mcp.json') { Copy-Item 'E:\AI\Pocket-Omega\mcp.json' 'D:\Users\JinT\.pocket-omega\mcp.json' -Verbose } else { Write-Output 'skipped: mcp.json not found' }"
        shell_exec: powershell -Command "if (Test-Path 'E:\AI\Pocket-Omega\rules.md') { Copy-Item 'E:\AI\Pocket-Omega\rules.md' 'D:\Users\JinT\.pocket-omega\rules.md' -Verbose } else { Write-Output 'skipped: rules.md not found' }"
        shell_exec: powershell -Command "if (Test-Path 'E:\AI\Pocket-Omega\soul.md') { Copy-Item 'E:\AI\Pocket-Omega\soul.md' 'D:\Users\JinT\.pocket-omega\soul.md' -Verbose } else { Write-Output 'skipped: soul.md not found' }"
        shell_exec: powershell -Command "if (Test-Path 'E:\AI\Pocket-Omega\skills') { New-Item -ItemType Directory -Force 'D:\Users\JinT\.pocket-omega\skills' | Out-Null; Copy-Item 'E:\AI\Pocket-Omega\skills\*' -Destination 'D:\Users\JinT\.pocket-omega\skills' -Recurse -Force -Verbose } else { Write-Output 'skipped: skills not found' }"
        shell_exec: powershell -Command "if (Test-Path 'E:\AI\Pocket-Omega\prompts') { New-Item -ItemType Directory -Force 'D:\Users\JinT\.pocket-omega\prompts' | Out-Null; Copy-Item 'E:\AI\Pocket-Omega\prompts\*' -Destination 'D:\Users\JinT\.pocket-omega\prompts' -Recurse -Force -Verbose } else { Write-Output 'skipped: prompts not found' }"

Step 4: shell_exec: powershell -Command "(Get-Content 'D:\Users\JinT\.pocket-omega\mcp.json' -Raw) -replace [regex]::Escape('E:\AI\Pocket-Omega'), 'D:\Users\JinT\.pocket-omega' | Set-Content 'D:\Users\JinT\.pocket-omega\mcp.json'"

Step 5: config_edit: { file: ".env", action: "set", key: "WORKSPACE_DIR", value: "D:\Users\JinT\.pocket-omega" }
        告知用户：WORKSPACE_DIR 已更新，请重启 omega 使新 workspace 生效
```
