# 子代理系统方案

> 状态：已实现（`internal/agent/subagent.go`），本文描述设计与使用方式。

## 目标

主 Agent 面对复杂任务时，把工作拆解成多个专门子任务交给**专门的子代理**
并行执行，例如「先探索代码 → 再制定计划 → 最后实施修改」。子代理之间通过
**DAG 依赖**约束先后关系。

## 核心概念

### 1. 子代理（SubAgent）

每个子代理是一个**独立的 Agent 实例**：

- 拥有自己的 System Prompt（角色/目标/行为约束）
- 拥有自己的工具集（可以是默认工具的**子集**）
- 拥有独立的会话历史（互不串扰）
- 共用同一个 LLMClient（同一个 AI 提供商）

内置子代理：

| 名称 | 职责 | 工具 |
| --- | --- | --- |
| `explorer` | 探索代码库，给出带 文件:行号 的结论 | read_file, list_dir, glob, grep |
| `builder` | 实施改动并用构建/测试验证 | read_file, write_file, list_dir, grep, glob, run_shell |
| `planner` | 制定分步实施计划（不写代码） | 无 |

### 2. 任务（Task）

```json
{
  "name": "t1",
  "agent": "explorer",
  "prompt": "找出配置解析相关代码",
  "depends_on": []
}
```

- `name`：任务唯一标识，供其它任务引用
- `agent`：使用哪个子代理
- `depends_on`：依赖的任务名列表，依赖任务完成后当前任务才能启动

### 3. DAG 调度（Scheduler）

调度算法：

1. 校验：任务名唯一、子代理存在、依赖引用有效
2. 分层：每一轮选出「依赖全部完成」的任务作为当前层
3. 并行：同层任务用 goroutine 并行执行（WaitGroup 同步）
4. 循环：执行完一层再选下一层，直到全部完成
5. 死锁检测：若某轮没有任何可执行任务但仍有剩余 → 报告依赖环

```
    ┌─ t1(explorer) ──────────┐
    │          ┌─ t3(builder)  ── 汇总
    └─ t2(planner) ───────────┘
        depends_on: t1 ──► t3
```

### 4. 与主 Agent 的集成

主 Agent 注册 `dispatch_subagents` 工具（参数：`tasks` 数组，含 `depends_on`）。
主 Agent 决定拆解策略 → 工具执行调度 → 返回各任务 JSON 结果 → 主 Agent 汇总成最终回答。

## 扩展

新增子代理只需实现 `agent.SubAgentSpec`：

```go
agent.SubAgentSpec{
    Name:   "tester",
    Prompt: "你是测试子代理…",
    Tools:  []string{"run_shell", "read_file"},
    Client: client,
}
```

再通过 `agent.RegisterSubAgents(specs)` 挂到主 Agent 上即可，`dispatch_subagents`
工具会自动把这些名字暴露给模型。

## 演进方向

- 子代理结果与当前文件/上下文关联的细粒度合并
- 调度超时、资源上限、失败重试
- 可视化 DAG 执行进度（Web 端）