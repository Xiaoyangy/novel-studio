package agents

import (
	"fmt"
	"strings"

	"github.com/chenhongyang/novel-studio/internal/bootstrap"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

const characterAgentSystemPromptV2 = characterAgentSystemPrompt + `

物理后态协议 v2：resource_views 仅代表你知道、收到、上次观测或估计的资源信息，不是世界真实余额。unaware 资源不会出现，未知数量不得补全。可以在同一次提案中提交有 knowledge_refs 来源的 resource_estimates、明确拟进行的 resource_measurements，以及发给既有角色的 resource_reports；估计和报告不保证等于实际数量。location 是你作决定时的起点，不能用打算前往的目的地替换。
多步与条件动作属于同一次决定，不必拆成多个章节。把拟传达的信息、请求、承诺、条件响应的语义要点写入communications，不锁定正文台词。resource_reads可引用可见resource_id；若打算“对方本轮实际递来材料就当场读”，用incoming_delivery_from声明来源人，不猜对方隐藏文档ID。承诺送来不等于已送达，拿到封袋/看外清单也不等于已解封读到里面文件。
src_来源句柄只证明你已经看到的受限视图或事实，不公开底层作者说明，不是文档读取权限。只能依据resource_views中实际展示的access/perception及明确已知事实判断；unknown资源不会因持有来源句柄变成已知内容或精确数量。knowledge_refs引用普通事实/记忆的id，或resource_views已列出的evidence_refs；其他Sources审计标签不产生新的可引用事实。`

const characterSelfExperiencePromptV2 = `
本人经历协议：self_experiences和task_progress是已经实际裁决的本人经历与累计有效工时，续做同一作业复用task_id并保持action/总目标/unit，不重置已完成进度。经历是有界展示，未列出的旧经历不等于未发生；当前未完成任务的累计值看task_progress。knowledge_refs可引用self_experiences.id，不把task_id本身当新事实。每轮self_tasks至少一项本人明确选择的任务；未执行的部分会如实记not_started/blocked，不把意图冒充结果。work申报已知总目标分钟数，不自行申报累计已完成值或把剩余量改成新总目标；carry/place明确本人有处置权限的resource_id。人在终点不代表工具已随身过去，只有明确执行的搬运才能形成known_placement；place目前只支持本章起点的归放。资源名称只是静态安全标签，真实可知放置/携带状态看known_placement，不把旧动态描述当当前事实。达到有效工时不自动等于检查结果合格；阅读或测量仍需相应resource_reads/resource_measurements声明，self_tasks本身不授予文档内容或余额。`

const worldArbiterSystemPromptV2 = worldArbiterSystemPrompt + `

物理后态协议 v2：world_stimulus.physical_state 是唯一物理前态，resources 为全世界按 resource_id 唯一的真实余额。顶层 resource_settlements 每个 resource_id 最多一条；多角色共享同一资源不能重复扣款或各自复制余额。known 数量必须 before+delta=after，未知真实余额保持 null；没有单位的定性资源不造数量。每条 resolution 必须提交 post_state，分别给出实际落点和资源访问/感知引用；原 proposal 的起点、decision 和 intended_action 保持原样。
真实物理余额与角色感知严格分开：perception 不因 actual_after 变化而自动刷新。新的 estimated 值只能来自该角色原提案的有来源估计，新的实测值须有原提案明确请求且实际执行的可见测量机制；收到的他人报告/票据标示按 reported 保留来源，不等于世界真值。角色接收既有资源可以获得新引用和访问权，但不能凭新引用获知未测量精确余额。ImmediateResult/StateAfter 是世界侧描述，不得拿它们自证角色已经获知隐藏余额。
使用post_state.resource_updates只提交真正变化的引用/感知，缺省或[]继承宿主前态，不要每章重抄全部名称/单位/旧感知。若使用完整resources，[]是明确清空且不得同时给resource_updates；actor身份可由宿主绑定。
同一次决定允许多步与条件动作。逐项裁决角色原提案communications、resource_reads等是否实际执行；resource_deliveries分别注明送达的信息与访问权，承诺将取资料不等于资料已经交接。实际听见的信息/承诺、按声明条件实际读到的文档内容必须放入接收者post_state.received_facts，绑定原通信/原文档source_id；text等可交宿主从原始源补全。语义信息不是固定正文台词。incoming_delivery_from只允许读本轮确实送达且可访问的具体材料，不能扫发送者所有私文档；看封袋外清单不等于打开内部原件，不能把当前余额或事后动机补进旧文书。
src_来源句柄只认证该角色已经展示的受限资源视图/access/perception或已知事实。不得拿句柄对应的world原始作者ref反推其正文已被角色获知；角色知识只看Observation实际可见内容。世界来源说明仍属于作者态，不能给unknown/未读资源补出已知数量。`

const worldArbiterSelfExperiencePromptV2 = `
本人经历协议：逐项裁决原提案self_tasks，在对应resolution.self_executions给task_id/status和位于story_time内的实际start_day/end_day。work累计有效分钟由宿主计算，同人work区间不重叠；准备、移动、等待、交谈不能冒充另一个任务的有效工时。不能仅因角色终点改变就推断工具已携带：carry/place必须原提案明确声明且实际completed，shared查阅不授予搬运权；place仅裁决在本章起点明确归放，不猜中途位置。post_state的self_experiences/task_progress/known_placement由宿主从执行回执物化，不得自行填总进度或自由写本人经历。现有known_placement与已裁决本人经历优先于旧perceived_name里的动态措辞，不能用“仍在棚里整理”等旧描述抹去实际携带/工时；静态perceived_label不写余额、地点或进度。`

func CharacterAgentProtocolDigestForVersion(version string) string {
	switch version {
	case domain.CharacterAgentDecisionProtocolVersion:
		return CharacterAgentProtocolDigest()
	case "", "legacy":
		return "legacy"
	case domain.CharacterAgentDecisionProtocolV2Version:
		submit := tools.NewSubmitCharacterDecisionTool(nil, domain.CharacterObservationPacket{Version: domain.CharacterObservationV2Version, Sources: []string{domain.CharacterSelfExperiencePolicyV2}})
		resolve := tools.NewResolveChapterWorldTool(nil, domain.WorldStimulusPacket{Version: domain.WorldStimulusPacketV2Version, Sources: []string{domain.CharacterSelfExperiencePolicyV2}}, domain.CharacterAgentActivation{}, nil, "", nil, 1)
		digest, err := domain.DeterministicPlanningHash(struct {
			Version                   string         `json:"version"`
			Observation               string         `json:"observation"`
			SourceRefPolicy           string         `json:"source_ref_policy"`
			SelfExperiencePolicy      string         `json:"self_experience_policy"`
			SelfModelViewPolicy       string         `json:"self_model_view_policy"`
			ModelInputIntegrityPolicy string         `json:"model_input_integrity_policy"`
			ObligationPolicy          string         `json:"obligation_policy"`
			CharacterPrompt           string         `json:"character_prompt"`
			ArbiterPrompt             string         `json:"arbiter_prompt"`
			SubmitSchema              map[string]any `json:"submit_schema"`
			ResolveSchema             map[string]any `json:"resolve_schema"`
		}{version, "physical-state.v2-truth-perception-separation", domain.CharacterSourceRefPolicyV2, domain.CharacterSelfExperiencePolicyV2, characterSelfModelViewPolicyV2, modelinput.ExactAgentPacketPolicy, "source-qualified-character-obligations.v1", characterAgentSystemPromptV2 + characterSelfExperiencePromptV2, worldArbiterSystemPromptV2 + worldArbiterSelfExperiencePromptV2, submit.Schema(), resolve.Schema()})
		if err != nil {
			return ""
		}
		return "sha256:" + digest
	default:
		return ""
	}
}

func characterProtocolForStimulus(stimulus domain.WorldStimulusPacket) string {
	if stimulus.Version == domain.WorldStimulusPacketV2Version {
		return domain.CharacterAgentDecisionProtocolV2Version
	}
	return domain.CharacterAgentDecisionProtocolVersion
}

// Persisted generation identity is authoritative. A known v1 marker must never
// fall through into the unrelated monolithic legacy implementation.
func bindProjectedCharacterProtocol(cfg *bootstrap.Config, persisted string) error {
	persisted = strings.TrimSpace(persisted)
	if persisted == "" || persisted == "legacy" {
		cfg.CharacterAgents.Protocol = "legacy"
		return nil
	}
	if persisted != domain.CharacterAgentDecisionProtocolVersion && persisted != domain.CharacterAgentDecisionProtocolV2Version {
		return fmt.Errorf("project-all unsupported character protocol %q; create a new generation through the formal pipeline", persisted)
	}
	if selected := cfg.CharacterAgentsProtocolVersion(); selected != persisted {
		return fmt.Errorf("project-all generation character protocol %s differs from configured %s; old evidence is unchanged, start a new generation rather than mixing protocols", persisted, selected)
	}
	if persisted == domain.CharacterAgentDecisionProtocolV2Version {
		cfg.CharacterAgents.Protocol = "v2"
	} else {
		cfg.CharacterAgents.Protocol = "v1"
	}
	return nil
}
