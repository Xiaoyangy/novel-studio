package host

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
	storepkg "github.com/chenhongyang/novel-studio/internal/store"
	"github.com/voocel/agentcore"
	"sync/atomic"
)

// errorKind classifies a runtime error into a stable, short label for log
// filtering and alert routing. Returns "" when no special tag applies.
//
// err is the live error chain (may be nil after JSON serialization); msg is
// the rendered string fallback used when the chain has been flattened
// (e.g. inside sub-agent JSON results).
func errorKind(err error, msg string) string {
	if err != nil && errors.Is(err, agentcore.ErrProviderStreamIdle) {
		return "stream_idle"
	}
	if msg != "" && agentcore.IsStreamIdleMessage(msg) {
		return "stream_idle"
	}
	return ""
}

// 单调递增的事件 ID 计数器；配合时间戳生成稳定 ID。
var eventIDCounter uint64

func nextEventID() string {
	return fmt.Sprintf("e%d", atomic.AddUint64(&eventIDCounter, 1))
}

// activeCall 记录一次正在进行的调用（TOOL / DISPATCH）的 ID、起点时间与 summary。
// summary 在完成事件时回填进 finish Event，保证 replay（runtime queue）能还原行内容。
type activeCall struct {
	id      string
	start   time.Time
	summary string
	depth   int
}

// observer 订阅 coordinator 事件流并投影到 Host 的输出通道。
// 它是纯观察者,不参与任何控制决策。
type observer struct {
	unsub   func()
	emitEv  func(Event)
	emitD   func(string)
	emitC   func()
	store   *storepkg.Store // 用于 runtime queue 持久化（ReplayQueue 消费）
	agents  map[string]*agentState
	agentMu sync.Mutex

	// aborting 由 Host 在 Abort()/Close() 入口置位、Start/Resume/Continue 清位。
	// 置位期间所有 context-cancel 衍生的错误事件被抑制（既是用户期望，也避免与
	// "用户手动暂停"事件重复）。真实异常（非 cancel）仍照常上报。
	aborting atomic.Bool

	streamThinking        bool
	lastThinkingByAgent   map[string]string          // agent → 最近的累积 thinking 文本（用于提取增量 delta）
	dispatchStarts        map[string]*activeCall     // dispatched agent → 进行中的 DISPATCH 调用
	currentDispatchTarget string                     // 当前正在执行的 subagent 名（handleToolEnd 时 Args 可能为空）
	toolStarts            map[string]*activeCall     // agent → 进行中的 TOOL 调用
	streamExtractors      map[string]*agentExtractor // agent → 当前工具调用 JSON 参数的内容抽取器
	streamArgPrefixes     map[string]string          // agent/tool → 参数流前缀，用于提前识别轻量标签
	streamArgLabels       map[string]string          // agent/tool → 已从参数流提前识别出的展示名
	retryEvents           map[string]string          // retry scope → event ID，用同一行原地更新 (2/7)
	streamHasContent      bool                       // 当前 streamRound 是否已输出过内容（判断是否需要段落分隔）
	streamLastByte        byte                       // 最近一次流式输出的末字节（用于精确补齐换行）
}

// agentExtractor 记录某个 agent 当前正在抽取的工具名与抽取器实例。
// 工具名用于检测"新的工具调用开始了"，避免缓存被上一轮残留污染。
type agentExtractor struct {
	tool       string
	ext        *jsonFieldExtractor
	emittedAny bool // 本 extractor 是否已经产出过内容；用于首次输出前补段落分隔
}

type agentState struct {
	name    string
	state   string
	tool    string
	summary string
	turn    int
	context AgentContextSnapshot
	updated time.Time
}

func newObserver(coordinator *agentcore.Agent, s *storepkg.Store, emitEv func(Event), emitD func(string), emitC func()) *observer {
	o := &observer{
		emitEv:              emitEv,
		emitD:               emitD,
		emitC:               emitC,
		store:               s,
		agents:              make(map[string]*agentState),
		lastThinkingByAgent: make(map[string]string),
		dispatchStarts:      make(map[string]*activeCall),
		toolStarts:          make(map[string]*activeCall),
		streamExtractors:    make(map[string]*agentExtractor),
		streamArgPrefixes:   make(map[string]string),
		streamArgLabels:     make(map[string]string),
		retryEvents:         make(map[string]string),
	}
	o.unsub = coordinator.Subscribe(o.handle)
	return o
}

func (o *observer) finalize() {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	for _, a := range o.agents {
		a.state = "idle"
		a.tool = ""
	}
}

// setAborting 由 Host 在 Abort/Close/Start 等生命周期切换处调用，控制
// "context canceled" 类衍生事件是否需要抑制（避免与"用户手动暂停"重复）。
func (o *observer) setAborting(v bool) { o.aborting.Store(v) }

func (o *observer) retryEventID(scope string, attempt int) string {
	if strings.TrimSpace(scope) == "" {
		scope = "coordinator"
	}
	if o.retryEvents == nil {
		o.retryEvents = make(map[string]string)
	}
	if attempt <= 1 || o.retryEvents[scope] == "" {
		o.retryEvents[scope] = nextEventID()
	}
	return o.retryEvents[scope]
}

// emitAndLog 用于调用类事件的"开始"态：发给 TUI 但不写入 runtime queue，
// 避免 replay 时"开始一行、完成又一行"重复。slog 由 host.emitEvent 统一记录。
func (o *observer) emitAndLog(ev Event) {
	o.emitEv(ev)
}

// persistEvent 把事件写入 runtime queue（slog 由 host.emitEvent 统一记录）。
func (o *observer) persistEvent(ev Event) {
	if o.store == nil || o.store.Runtime == nil {
		return
	}
	priority := domain.RuntimePriorityBackground
	switch ev.Category {
	case "SYSTEM", "ERROR":
		priority = domain.RuntimePriorityControl
	}
	_, _ = o.store.Runtime.AppendQueue(domain.RuntimeQueueItem{
		Time:     ev.Time,
		Kind:     domain.RuntimeQueueUIEvent,
		Priority: priority,
		Category: ev.Category,
		Summary:  ev.Summary,
		Payload:  ev,
	})
}

func (o *observer) updateAgent(name string, fn func(*agentState)) {
	if name == "" {
		return
	}
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	a, ok := o.agents[name]
	if !ok {
		a = &agentState{name: name, state: "idle"}
		o.agents[name] = a
	}
	fn(a)
	a.updated = time.Now()
}

func (o *observer) agentSnapshots() []AgentSnapshot {
	o.agentMu.Lock()
	defer o.agentMu.Unlock()
	snaps := make([]AgentSnapshot, 0, len(o.agents))
	for _, a := range o.agents {
		snaps = append(snaps, AgentSnapshot{
			Name:      a.name,
			State:     a.state,
			Summary:   a.summary,
			Tool:      a.tool,
			Turn:      a.turn,
			Context:   a.context,
			UpdatedAt: a.updated,
		})
	}
	return snaps
}

func agentFromEvent(ev agentcore.Event) string {
	if ev.Progress != nil && ev.Progress.Agent != "" {
		return ev.Progress.Agent
	}
	return "coordinator"
}
