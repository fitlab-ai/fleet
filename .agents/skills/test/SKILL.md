---
name: test
description: >
  执行项目完整测试流程（编译检查 + 单元测试）。
  当需要运行测试或验证代码质量时使用。
---

# 执行测试

执行项目的完整 Go 验证流程，包括构建、静态检查、单元测试和竞态检测。

## 1. 编译 / 类型检查

```bash
go build -trimpath -o dist/fleet ./cmd/fleet
go vet ./...
```

确认 CLI 构建成功且所有 Go package 均通过静态检查。`dist/fleet` 是本地生成产物，不应提交。

## 2. 运行单元测试（按层级选择）

Fleet 尚未划分独立的快速测试套件；smoke 和 core 均使用完整 Go 测试命令，full 在此基础上追加竞态检测。

### smoke（目标 <5s）

```bash
go test ./...
```

适用场景：
- code-task 内循环
- 保存即跑 / 频繁反馈
- 仅断言项目结构、配置、模板契约

### core（目标 <15s）

```bash
go test ./...
```

适用场景：
- pre-commit hook（自动调用）
- 写 code.md / code-r{N}.md 报告前的最终验证
- 推送 PR 前的本地把关

### full（完整测试套件）

```bash
go test ./...
go test -race ./...
```

适用场景：
- release / tag 前
- CI
- main 合并前的最终把关

`go test ./...` 自动覆盖仓库内所有 Go package，包括 `tests/compat`。新增的 `*_test.go` 文件只要位于模块 package 中，就会被完整测试命令纳入。

## 3. 输出结果

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
