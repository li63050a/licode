// Package logx 提供可选的结构化 JSON 日志（trace_id 串联 Agent -> Tool 调用链）。
// 通过环境变量 LICODE_JSON_LOG=1 开启，便于线上检索与链路追踪。
package logx

import (
	"encoding/json"
	"log"
	"os"
	"sync/atomic"
	"time"
)

var (
	jsonMode = os.Getenv("LICODE_JSON_LOG") == "1"
	seq      int64
)

type entry struct {
	Ts  string `json:"ts"`
	Lv  string `json:"lv"`
	Msg string `json:"msg"`
	// TraceID 一次 Agent 运行的调用链标识。
	TraceID string         `json:"trace_id,omitempty"`
	Agent   string         `json:"agent,omitempty"`
	Tool    string         `json:"tool,omitempty"`
	Args    any            `json:"args,omitempty"`
	Out     string         `json:"out,omitempty"`
	Fields  map[string]any `json:"fields,omitempty"`
}

// NewTraceID 生成一个进程内自增的短 trace id。
func NewTraceID() string {
	return string(rune('A'+int(atomic.AddInt64(&seq, 1)-1)%26)) + "-" + itoa(atomic.LoadInt64(&seq))
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Enabled 报告是否开启了 JSON 日志模式。
func Enabled() bool { return jsonMode }

func emit(e entry) {
	if !jsonMode {
		return
	}
	e.Ts = time.Now().Format(time.RFC3339Nano)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	log.Print(string(b))
}

// AgentStart 记录一次 agent 运行开始。
func AgentStart(traceID, agent string) {
	emit(entry{Lv: "info", Msg: "agent_start", TraceID: traceID, Agent: agent})
}

// ToolCall 记录一次工具调用（入参与输出）。
func ToolCall(traceID, tool, argsRaw, out string) {
	emit(entry{Lv: "info", Msg: "tool_call", TraceID: traceID, Tool: tool, Args: argsRaw, Out: truncate(out)})
}

// AgentError 记录 agent 运行错误。
func AgentError(traceID, agent, errMsg string) {
	emit(entry{Lv: "error", Msg: "agent_error", TraceID: traceID, Agent: agent, Out: errMsg})
}

func truncate(s string) string {
	const max = 1000
	if len(s) > max {
		return s[:max] + "...(truncated)"
	}
	return s
}
