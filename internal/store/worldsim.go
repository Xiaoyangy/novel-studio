package store

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

// WorldSimStore 管理世界模拟（离屏推演）的事实工件：
//   - meta/world_events.jsonl：离屏事件流（append-only）
//   - meta/world_tick.json：世界推演游标
//   - meta/offscreen_agenda.json：角色离屏日程账本
//   - meta/simulation_tiers.json：LOD 模拟分层名单
//
// 全部工件可选：Load 在文件缺失时返回零值/nil，老项目零影响。
type WorldSimStore struct{ io *IO }

// NewWorldSimStore 创建世界模拟存储。
func NewWorldSimStore(io *IO) *WorldSimStore { return &WorldSimStore{io: io} }

const worldEventsPath = "meta/world_events.jsonl"

// AppendWorldEvents 追加一批事件；ID 为空时自动分配（we-<序号>，接续现有条数）。
// 逐条 Validate，任一非法整体拒绝（写前校验，保证账本内全部合法）。
// 同一 tick 的同一事件重复提交时跳过，保证 save_world_tick 在恢复/重试时幂等。
func (s *WorldSimStore) AppendWorldEvents(events []domain.WorldEvent) ([]domain.WorldEvent, error) {
	if len(events) == 0 {
		return nil, nil
	}
	existing, err := s.LoadWorldEvents()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, len(existing)+len(events))
	for _, e := range existing {
		if strings.TrimSpace(e.ID) != "" {
			seen["id:"+strings.TrimSpace(e.ID)] = struct{}{}
		}
		seen["event:"+worldEventDedupKey(e)] = struct{}{}
	}

	next := len(existing) + 1
	toWrite := make([]domain.WorldEvent, 0, len(events))
	for i := range events {
		if err := events[i].Validate(); err != nil {
			return nil, err
		}
		if strings.TrimSpace(events[i].ID) != "" {
			if _, ok := seen["id:"+strings.TrimSpace(events[i].ID)]; ok {
				continue
			}
		}
		key := "event:" + worldEventDedupKey(events[i])
		if _, ok := seen[key]; ok {
			continue
		}
		if events[i].ID == "" {
			events[i].ID = fmt.Sprintf("we-%06d", next)
			next++
		}
		seen["id:"+strings.TrimSpace(events[i].ID)] = struct{}{}
		seen[key] = struct{}{}
		toWrite = append(toWrite, events[i])
	}
	for _, e := range toWrite {
		data, err := json.Marshal(e)
		if err != nil {
			return nil, err
		}
		// AppendLine 不自动补换行，JSONL 语义由调用方保证。
		if err := s.io.AppendLine(worldEventsPath, append(data, '\n')); err != nil {
			return nil, err
		}
	}
	return toWrite, nil
}

// LoadWorldEvents 读取全部事件；文件缺失返回空。损坏行跳过（append-only 日志的容错语义）。
func (s *WorldSimStore) LoadWorldEvents() ([]domain.WorldEvent, error) {
	data, err := s.io.ReadFile(worldEventsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var events []domain.WorldEvent
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e domain.WorldEvent
		if err := json.Unmarshal(line, &e); err != nil {
			continue
		}
		events = append(events, e)
	}
	if err := scanner.Err(); err != nil {
		return events, err
	}
	return events, nil
}

// ResetActivityState 清理活动推演游标与离屏事件流。章节/设定种子不动，
// 用于 --reset-simulation-state 切换 generation 后重新生成 canon 世界 tick。
func (s *WorldSimStore) ResetActivityState() error {
	return s.io.WithWriteLock(func() error {
		if err := s.io.RemoveFileUnlocked(worldEventsPath); err != nil {
			return err
		}
		return s.io.RemoveFileUnlocked("meta/world_tick.json")
	})
}

func worldEventDedupKey(e domain.WorldEvent) string {
	return strings.Join([]string{
		strings.TrimSpace(e.TickID),
		fmt.Sprintf("%d", e.Chapter),
		strings.TrimSpace(e.Summary),
		normalizedWorldEventActors(e.Actors),
	}, "\x1f")
}

func normalizedWorldEventActors(actors []string) string {
	out := make([]string, 0, len(actors))
	for _, actor := range actors {
		actor = strings.TrimSpace(actor)
		if actor != "" {
			out = append(out, actor)
		}
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

// HorizonEvents 返回"已越过地平线"且仍在新鲜窗口内的事件：
// VisibilityChapter ≤ chapter ≤ VisibilityChapter+window。
// 供 novel_context 注入 Writer——正文只写主角能感知到的世界。
func (s *WorldSimStore) HorizonEvents(chapter, window int) ([]domain.WorldEvent, error) {
	all, err := s.LoadWorldEvents()
	if err != nil {
		return nil, err
	}
	var out []domain.WorldEvent
	for _, e := range all {
		if e.VisibilityChapter <= chapter && chapter <= e.VisibilityChapter+window {
			out = append(out, e)
		}
	}
	return out, nil
}

// SaveTick 保存世界推演游标（自动填 UpdatedAt）。
func (s *WorldSimStore) SaveTick(t domain.WorldTick) error {
	if t.UpdatedAt == "" {
		t.UpdatedAt = time.Now().Format(time.RFC3339)
	}
	return s.io.WriteJSON("meta/world_tick.json", t)
}

// LoadTick 读取游标；缺失返回 nil。
func (s *WorldSimStore) LoadTick() (*domain.WorldTick, error) {
	var t domain.WorldTick
	if err := s.io.ReadJSON("meta/world_tick.json", &t); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &t, nil
}

const storyTimeContractPath = "meta/story_time_contract.json"

// SaveStoryTimeContract / LoadStoryTimeContract 读写全书结构化时间合同。
// Save 会重算双摘要；Load 会校验核心承诺、日程和双摘要，避免手改 JSON
// 后让 world tick 在未察觉的情况下使用被篡改的故事日坐标。
func (s *WorldSimStore) SaveStoryTimeContract(contract domain.StoryTimeContract) error {
	finalized, err := domain.FinalizeStoryTimeContract(contract)
	if err != nil {
		return err
	}
	return s.io.WriteJSON(storyTimeContractPath, finalized)
}

func (s *WorldSimStore) LoadStoryTimeContract() (*domain.StoryTimeContract, error) {
	var contract domain.StoryTimeContract
	if err := s.io.ReadJSON(storyTimeContractPath, &contract); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if err := contract.Validate(); err != nil {
		return nil, fmt.Errorf("invalid story time contract: %w", err)
	}
	return &contract, nil
}

// SaveStoryCalendar / LoadStoryCalendar 故事内时间轴基线读写；缺失返回 nil。
func (s *WorldSimStore) SaveStoryCalendar(c domain.StoryCalendar) error {
	return s.io.WriteJSON("meta/story_calendar.json", c)
}

func (s *WorldSimStore) LoadStoryCalendar() (*domain.StoryCalendar, error) {
	var c domain.StoryCalendar
	if err := s.io.ReadJSON("meta/story_calendar.json", &c); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

// SaveAgendaLedger / LoadAgendaLedger 日程账本读写；缺失返回空账本。
func (s *WorldSimStore) SaveAgendaLedger(l domain.OffscreenAgendaLedger) error {
	for _, a := range l.Agendas {
		if err := a.Validate(); err != nil {
			return err
		}
	}
	return s.io.WriteJSON("meta/offscreen_agenda.json", l)
}

func (s *WorldSimStore) LoadAgendaLedger() (domain.OffscreenAgendaLedger, error) {
	var l domain.OffscreenAgendaLedger
	if err := s.io.ReadJSON("meta/offscreen_agenda.json", &l); err != nil {
		if os.IsNotExist(err) {
			return domain.OffscreenAgendaLedger{}, nil
		}
		return domain.OffscreenAgendaLedger{}, err
	}
	return l, nil
}

// SaveSimulationCast / LoadSimulationCast LOD 分层名单读写；缺失返回空名单。
func (s *WorldSimStore) SaveSimulationCast(c domain.SimulationCast) error {
	return s.io.WriteJSON("meta/simulation_tiers.json", c)
}

func (s *WorldSimStore) LoadSimulationCast() (domain.SimulationCast, error) {
	var c domain.SimulationCast
	if err := s.io.ReadJSON("meta/simulation_tiers.json", &c); err != nil {
		if os.IsNotExist(err) {
			return domain.SimulationCast{}, nil
		}
		return domain.SimulationCast{}, err
	}
	return c, nil
}

// SaveEventWeave / LoadEventWeave 事件编织层（Task 078）；缺失返回 nil。
func (s *WorldSimStore) SaveEventWeave(w domain.EventWeave) error {
	if err := w.Validate(); err != nil {
		return err
	}
	return s.io.WriteJSON("meta/event_weave.json", w)
}

func (s *WorldSimStore) LoadEventWeave() (*domain.EventWeave, error) {
	var w domain.EventWeave
	if err := s.io.ReadJSON("meta/event_weave.json", &w); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &w, nil
}
