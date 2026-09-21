---
name: test-integration
description: >
  执行项目集成测试流程。
  当需要运行项目集成测试流程时使用。
---

# 运行集成测试

执行项目的集成测试流程，进行端到端验证。

## 1. 验证构建产物

在运行集成测试前确保项目已构建。

```bash
go build -trimpath -o dist/fleet ./cmd/fleet
```

如果构建产物不存在，提示用户先执行 test 技能。

## 2. 运行集成测试

```bash
go test ./internal/app ./internal/backend ./internal/runtime
```

这些 package 覆盖控制面到数据面适配器的诊断、配置、进程生命周期和资源所有权契约。
其中真实 sing-box 配置检查在未安装二进制时会显式 skip，必须作为环境缺口报告，不能记为集成验证通过。
涉及并发、生命周期、代理或文件状态时追加 `go test -race ./internal/app ./internal/backend ./internal/runtime`。

## 3. 输出结果

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

> 渲染下一步前先读取 `.agents/rules/next-step-output.md`，仅为已选场景调用统一 helper，并将 stdout 填入 `{next-step-commands}`。

使用 `agent-infra-internal agent-client next-steps --skill commit` 生成本场景的 `{next-step-commands}`。

```
下一步 - 提交代码：
{next-step-commands}
```

## 注意事项

1. **前置条件**：必须先成功构建 Fleet CLI；构建失败时先执行 test 技能排查
2. **技术栈**：只执行 Go 构建和 Go 测试命令
3. **验证范围**：执行 app、backend 和 runtime 的现有行为测试
4. **环境**：集成测试通常耗时较长；请耐心等待
5. **清理**：测试完成后不要提交 `dist/fleet`
