# licode 子代理系统

主 Agent 面对复杂任务时，可把任务拆解成多个相互独立的子任务交给**专门子代理**并行执行，例如「探索代码 → 规划 → 实现」。

## 内置子代理

| 名称 | 职责 | 工具 |
| --- | --- | --- |
| `explorer` | 探索代码库，给出带 文件:行号 的结论 | read_file, list_dir, glob, grep |
| `builder` | 实施改动并用构建/测试验证 | read_file, write_file, list_dir, grep, glob, run_shell |
| `planner` | 制定分步实施计划（不写代码） | 无 |

## 并行调度（DAG）

主 Agent 通过 `dispatch_subagents` 工具一次性提交多个任务：

```json
{
  "tasks": [
    {"name": "t1", "agent": "explorer", "prompt": "找出配置解析代码"},
    {"name": "t2", "agent": "planner", "prompt": "制定修改计划"},
    {"name": "t3", "agent": "builder", "prompt": "实施修改", "depends_on": ["t1", "t2"]}
  ]
}
```

- 相互独立的任务在同一层**并行执行**（goroutine）
- `depends_on` 形成依赖，依赖完成后才执行（DAG）
- 存在依赖环时报告错误
- 全部完成后由主 Agent 汇总

## 开关

设置里「子代理」开启/关闭；`--no-subagents` 启动时禁用。

## 扩展

自定义子代理：在 `.licode/agents/*.md` 或 `~/.licode/agents/*.md` 写：

```markdown
---
name: tester
description: 负责写并运行测试
tools: run_shell, read_file, write_file
---

你是测试子代理，写测试并用 go test 运行……
```

正文为 System Prompt，frontmatter 声明名称、描述、工具。