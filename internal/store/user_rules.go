package store

import (
	"os"

	"github.com/chenhongyang/novel-studio/internal/rules"
)

// UserRulesStore 管理本书归一化后的用户规则快照（meta/user_rules.json）。
//
// 运行时唯一事实源：novel_context 注入与 commit_chapter 检查都只读这一份，
// 不再反复读 rules 文件（避免漂移与双读者发散）。快照由开书/导入/刷新时归一化生成。
type UserRulesStore struct{ io *IO }

func NewUserRulesStore(io *IO) *UserRulesStore { return &UserRulesStore{io: io} }

// Load 读取 meta/user_rules.json。不存在时返回 nil（调用方据此惰性生成）。
func (s *UserRulesStore) Load() (*rules.Snapshot, error) {
	s.io.mu.RLock()
	defer s.io.mu.RUnlock()
	var snap rules.Snapshot
	if err := s.io.ReadJSONUnlocked("meta/user_rules.json", &snap); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return &snap, nil
}

// Save 保存快照。
func (s *UserRulesStore) Save(snap *rules.Snapshot) error {
	s.io.mu.Lock()
	defer s.io.mu.Unlock()
	return s.io.WriteJSONUnlocked("meta/user_rules.json", snap)
}
