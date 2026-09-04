---
name: upgrade-dependency
description: >
  升级项目依赖到新版本并验证。
  当需要升级某个依赖并验证改动时使用。
---

# 升级依赖

升级 `go.mod` 中的 Go module，并进行整理、构建和测试验证。

## 执行流程

### 1. 解析参数

从参数中提取：包名、原版本、新版本。

### 2. 查找依赖位置

在 `go.mod` 中确认目标 module 及当前版本，并检查 `go.sum` 中对应的校验记录。

### 3. 更新版本

使用 Go 工具链更新指定 module：

```bash
go get {module}@{version}
```

不得手工只修改 `go.mod` 中的版本字符串；让 Go 同步依赖图和 `go.sum`。

### 4. 整理依赖

```bash
go mod tidy
```

检查 `go.mod` 和 `go.sum` 的 diff，确认没有无关 module 变化。

### 5. 验证构建

```bash
go build -trimpath -o dist/fleet ./cmd/fleet
go vet ./...
```

### 6. 运行测试

```bash
go test ./...
go test -race ./...
```

### 7. 输出结果

报告：
- 修改的文件
- 构建状态（通过/失败）
- 测试状态（通过/失败）
- 发现的任何弃用警告或破坏性变更

建议下一步：

> 渲染下一步前先读取 `.agents/rules/next-step-output.md`，仅为已选场景调用统一 helper，并将 stdout 填入 `{next-step-commands}`。

使用 `agent-infra-internal agent-client next-steps --skill commit` 生成本场景的 `{next-step-commands}`。

```
下一步 - 提交代码：
{next-step-commands}
```

## 注意事项

1. **禁止自动提交**：不要自动提交变更
2. **主版本升级**：警告潜在的破坏性变更
3. **测试失败**：报告失败详情并等待用户决定
4. **校验文件**：必须让 `go mod tidy` 同步 `go.sum`
5. **传递依赖**：检查 indirect module 是否发生预期外变化

## 错误处理

- 包未找到：提示 "Package {name} not found in dependency files"
- 构建失败：输出错误并建议检查破坏性变更
- 测试失败：输出测试错误并建议查看迁移指南
