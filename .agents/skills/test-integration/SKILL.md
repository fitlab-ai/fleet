---
name: test-integration
description: >
  执行项目集成测试流程。
  当需要运行项目集成测试流程时使用。
---

# 运行集成测试

执行 Fleet 的 Go 兼容性测试流程，验证迁移后的 CLI 行为契约。

## 1. 验证构建产物

先构建 CLI：

```bash
go build -trimpath -o dist/fleet ./cmd/fleet
```

如果构建失败，停止并报告错误。

## 2. 运行兼容性测试

```bash
go test ./tests/compat/...
```

该 package 验证 Go 实现与迁移兼容性矩阵保持一致。不得直接执行遗留测试源文件。

## 3. 运行完整竞态测试

```bash
go test -race ./...
```

## 4. 输出结果

报告结果：
- 运行/通过/失败的测试数
- 环境问题（如有）
- 失败详情（如有）

## 失败处理

如果测试失败：
- 输出失败详情
- 检查环境问题（端口占用、服务未运行等）
- 不要自动修复 —— 等待用户决定

## 后续步骤

测试通过后，建议提交变更：

> **重要**：以下「下一步」中列出的所有 TUI 命令格式必须完整输出，不要只展示当前 AI 代理对应的格式。如果 `.agents/.airc.json` 中配置了自定义 TUI（`customTUIs`），读取每个工具的 `name` 和 `invoke`，按同样格式补充对应命令行（`${skillName}` 替换为技能名，`${projectName}` 替换为项目名）。

```
下一步 - 提交代码：
  - Claude Code / OpenCode：/commit
  - Gemini CLI：/fleet:commit
  - Codex CLI：$commit
```

## 注意事项

1. **前置条件**：必须先成功构建 Fleet CLI
2. **技术栈**：只执行 Go 构建和 Go 测试命令
3. **兼容性矩阵**：`tests/compat` 是迁移兼容性验证入口
4. **清理**：测试完成后不要提交 `dist/fleet`
