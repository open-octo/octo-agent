# Handoff: Unified Agent Tool Refactor

## 状态

**进行中** — 核心代码完成，server 测试失败。

## 已完成

1. **新工具** `internal/tools/agent.go` — 统一的 `Agent` 工具，替换：
   - `launch_agent` → `Agent`（`run_in_background` 控制同步/异步）
   - `send_message` → 删除（统一用 `Agent`）
   - `agent_status` → 删除
   - `kill_agent` → 删除
   - `explore_agent`/`plan_agent`/`general_agent`/`code_review_agent` → `Agent` 的 `subagent_type` 参数

2. **预设定义** `internal/tools/agent_presets.go` — 内置 4 种 agent 类型

3. **Spawner 接口提取** `internal/tools/spawner.go` — 从 `launch_agent.go` 提取

4. **Registry 更新** — `AgentTool` 替换旧工具，删除 `statusKillOn` 逻辑

5. **测试更新** — 大部分测试已适配新工具名

## 当前问题

`TestServerRunsSubAgentSynchronously` 失败：
- 期望 3 次 sender 调用（parent → child → parent）
- 实际 2 次调用
- 子 agent 的同步路径没有正确把结果反馈给 parent 的下一轮

## 失败测试位置

```
internal/server/subagent_test.go:91
```

测试场景：
1. Parent 调用 `Agent` 工具（同步模式）
2. 子 agent 应该 inline 执行并返回结果
3. Parent 应该看到子 agent 的结果后继续下一轮

实际：子 agent 执行了，但 parent 直接返回了子 agent 的结果，没有继续 parent 的下一轮。

## 可能原因

1. `AgentTool.Execute` 同步路径返回的 `tool_result` 格式问题
2. `runLoop` 处理 `tool_result` 后，下一轮 send 没有正确触发
3. 子 agent 的 `RunStream` 调用消耗了 sender 的第二个 reply，但 parent 没有拿到第三个 reply

## 待办

- [ ] 修复 `TestServerRunsSubAgentSynchronously`
- [ ] 修复 `TestBuildEnvContext_GitStateForRepo`（无关的 git 测试失败）
- [ ] 运行完整测试套件
- [ ] 提交 PR

## 分支

`feat/unified-agent-tool`（在 `/tmp/octo-agent-refactor` worktree 中）

## 设计文档

`dev-docs/unified-agent-tool.md`
