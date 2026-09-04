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

Fleet 尚未划分独立的快速测试套件；三层测试是可选的反馈速度优化，如果测试套件较小，所有层级都可以映射到同一个完整测试命令。因此 smoke 和 core 均使用完整 Go 测试命令，full 在此基础上追加竞态检测。

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

如果项目尚未分层，smoke / core / full 可以全部使用完整测试命令；分层不是使用协作工作流的前置条件。

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

> 渲染下一步前先读取 `.agents/rules/next-step-output.md`，仅为已选场景调用统一 helper，并将 stdout 填入 `{next-step-commands}`。

使用 `agent-infra-internal agent-client next-steps --skill commit` 生成本场景的 `{next-step-commands}`。

```
下一步 - 提交代码：
{next-step-commands}
```
