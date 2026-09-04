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

> 渲染下一步前先读取 `.agents/rules/next-step-output.md`，仅为已选场景调用统一 helper，并将 stdout 填入 `{next-step-commands}`。

使用 `agent-infra-internal agent-client next-steps --skill commit` 生成本场景的 `{next-step-commands}`。

```
下一步 - 提交代码：
{next-step-commands}
```

## 注意事项

1. **前置条件**：必须先成功构建 Fleet CLI；构建失败时先执行 test 技能排查
2. **技术栈**：只执行 Go 构建和 Go 测试命令
3. **兼容性矩阵**：`tests/compat` 是迁移兼容性验证入口，不得直接执行遗留测试源文件
4. **超时**：竞态与兼容性测试可能耗时较长，请耐心等待
5. **清理**：测试完成后不要提交 `dist/fleet`
