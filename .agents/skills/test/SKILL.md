---
name: test
description: >
  执行项目完整测试流程（编译检查 + 单元测试）。
  当需要运行测试或验证代码质量时使用。
---

# 执行测试

执行项目的完整 Go 验证流程，包括构建、静态检查、单元测试和竞态检测。

## 1. 构建

```bash
go build -trimpath -o dist/fleet ./cmd/fleet
```

确认 CLI 构建成功。`dist/fleet` 是本地生成产物，不应提交。

## 2. 静态检查

```bash
go vet ./...
```

确认所有 Go package 均通过静态检查。

## 3. 运行测试

先运行完整测试：

```bash
go test ./...
```

再使用竞态检测器复核：

```bash
go test -race ./...
```

`go test ./...` 自动覆盖仓库内所有 Go package，包括 `tests/compat`。新增的 `*_test.go` 文件只要位于模块 package 中，就会被完整测试命令纳入。

## 4. 输出结果

报告测试结果摘要：
- 运行的总测试数
- 通过数量
- 失败数量（包含每个失败的详情）
- 测试覆盖率（如已配置）

## 失败处理

如果测试失败：
- 输出失败详情和建议的修复方向
- 不要自动修复代码 —— 等待用户决定

## 后续步骤

测试通过后，建议提交变更：

> **重要**：以下「下一步」中列出的所有 TUI 命令格式必须完整输出，不要只展示当前 AI 代理对应的格式。如果 `.agents/.airc.json` 中配置了自定义 TUI（`customTUIs`），读取每个工具的 `name` 和 `invoke`，按同样格式补充对应命令行（`${skillName}` 替换为技能名，`${projectName}` 替换为项目名）。

```
下一步 - 提交代码：
  - Claude Code / OpenCode：/commit
  - Gemini CLI：/fleet:commit
  - Codex CLI：$commit
```
