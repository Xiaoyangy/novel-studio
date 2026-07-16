package domain

// WritingFeature 是可启用/停用/组合的写法特征。
// 它来自用户规则、拆书结论、已写正文提炼或手工维护，不再只是 prompt 里的一段说明。
type WritingFeature struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Category    string   `json:"category"` // prose / dialogue / pacing / anti_ai / taboo / structure
	Description string   `json:"description"`
	Enabled     bool     `json:"enabled"`
	Weight      int      `json:"weight,omitempty"`
	Rules       []string `json:"rules,omitempty"`
	SampleIDs   []string `json:"sample_ids,omitempty"`
	Source      string   `json:"source,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// WritingSample 保存原文样本或本书正文片段的短锚点。
// 样本用于试写/比对语感，生成时只能模仿结构和节奏，不能搬运原句。
type WritingSample struct {
	ID        string   `json:"id"`
	Source    string   `json:"source,omitempty"`
	Chapter   int      `json:"chapter,omitempty"`
	FeatureID string   `json:"feature_id,omitempty"`
	Text      string   `json:"text"`
	Tags      []string `json:"tags,omitempty"`
	Hash      string   `json:"hash,omitempty"`
}

// WritingFeedback 保存审阅中沉淀出的历史写法反馈。
// 完整审阅事实仍在 reviews/，这里保存的是可跨章复用的写法提醒和提炼规则。
type WritingFeedback struct {
	ID         string `json:"id"`
	Chapter    int    `json:"chapter,omitempty"`
	Scope      string `json:"scope,omitempty"`
	Dimension  string `json:"dimension,omitempty"`
	Severity   string `json:"severity,omitempty"`
	Category   string `json:"category,omitempty"`
	Signal     string `json:"signal"`
	Evidence   string `json:"evidence,omitempty"`
	Suggestion string `json:"suggestion,omitempty"`
	Rule       string `json:"rule,omitempty"`
	Source     string `json:"source,omitempty"`
	CreatedAt  string `json:"created_at,omitempty"`
	UpdatedAt  string `json:"updated_at,omitempty"`
}

// WritingPreset 是一组可复用写法组合，可绑定到全书、卷弧或章节任务。
type WritingPreset struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	FeatureIDs  []string `json:"feature_ids"`
	Scope       string   `json:"scope,omitempty"` // book / volume / arc / chapter / trial
	Description string   `json:"description,omitempty"`
}

// WritingAssetLibrary 是本书的长期写法资产库。
type WritingAssetLibrary struct {
	Version      int               `json:"version"`
	Features     []WritingFeature  `json:"features"`
	Samples      []WritingSample   `json:"samples,omitempty"`
	Feedback     []WritingFeedback `json:"feedback,omitempty"`
	Presets      []WritingPreset   `json:"presets,omitempty"`
	Bindings     []WritingBinding  `json:"bindings,omitempty"`
	Compiled     *WritingCompiled  `json:"compiled,omitempty"`
	LastCompiled string            `json:"last_compiled,omitempty"`
}

// WritingBinding 声明某个写法组合绑定到哪个范围。
type WritingBinding struct {
	Scope     string `json:"scope"` // book / volume / arc / chapter / trial
	Volume    int    `json:"volume,omitempty"`
	Arc       int    `json:"arc,omitempty"`
	Chapter   int    `json:"chapter,omitempty"`
	PresetID  string `json:"preset_id,omitempty"`
	FeatureID string `json:"feature_id,omitempty"`
}

// WritingCompiled 是给生成/检测/修正链路直接消费的轻量写法引擎输出。
type WritingCompiled struct {
	EnabledFeatures []WritingFeature  `json:"enabled_features"`
	ActiveRules     []string          `json:"active_rules"`
	AntiAIRules     []string          `json:"anti_ai_rules,omitempty"`
	Taboos          []string          `json:"taboos,omitempty"`
	Samples         []WritingSample   `json:"samples,omitempty"`
	Feedback        []WritingFeedback `json:"feedback,omitempty"`
	Trace           []string          `json:"trace,omitempty"`
}

// BookWorld 是从 world_rules 升级出的本书世界资产。
// 规则仍是硬边界，BookWorld 负责地图、地点、势力和可复用上下文切片。
type BookWorld struct {
	Version      int            `json:"version"`
	Name         string         `json:"name,omitempty"`
	Summary      string         `json:"summary,omitempty"`
	Places       []WorldPlace   `json:"places,omitempty"`
	Routes       []WorldRoute   `json:"routes,omitempty"`
	Factions     []WorldFaction `json:"factions,omitempty"`
	MapNotes     []string       `json:"map_notes,omitempty"`
	LastSyncedAt string         `json:"last_synced_at,omitempty"`

	// ProtagonistPosition 主角在矛盾网中的位置一句话（被哪几对冲突卷入）。
	ProtagonistPosition string `json:"protagonist_position,omitempty"`
	// VisionPillars 视觉核心（看起来是什么样）；WorldPillars 世界核心（如何运作）。
	VisionPillars *VisionPillars `json:"vision_pillars,omitempty"`
	WorldPillars  *WorldPillars  `json:"world_pillars,omitempty"`
}

// ValidateFactionRelations 返回 relation.Target 指向不存在势力的警告列表
// （供 diag / lint 使用，不阻塞保存）。
func (w BookWorld) ValidateFactionRelations() []string {
	known := make(map[string]struct{}, len(w.Factions)*2)
	for _, f := range w.Factions {
		if f.ID != "" {
			known[f.ID] = struct{}{}
		}
		if f.Name != "" {
			known[f.Name] = struct{}{}
		}
		for _, alias := range f.Aliases {
			if alias != "" {
				known[alias] = struct{}{}
			}
		}
	}
	var issues []string
	for _, f := range w.Factions {
		for _, rel := range f.Relations {
			if rel.Target == "" {
				continue
			}
			if _, ok := known[rel.Target]; !ok {
				issues = append(issues, "势力 "+f.Name+" 的关系指向不存在的目标: "+rel.Target)
			}
		}
	}
	return issues
}

type WorldPlace struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind,omitempty"`
	Description string   `json:"description,omitempty"`
	Rules       []string `json:"rules,omitempty"`
	Factions    []string `json:"factions,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

type WorldRoute struct {
	From        string `json:"from"`
	To          string `json:"to"`
	Description string `json:"description,omitempty"`
	Risk        string `json:"risk,omitempty"`

	// TravelDays 该路线的旅行天数（常规方式）。GM 推演角色移动、
	// 换算消息 visibility_chapter 的依据。
	TravelDays float64 `json:"travel_days,omitempty"`
}

// FactionClock 势力进度钟（Blades in the Dark 模式）：每个势力的长期目标
// 用一个 N 段钟跟踪，GM 在弧边界世界 tick 时拨钟；走满即触发 Consequence
// （转化为离屏事件）。被忽略的势力下次 tick 一次性补拨。
type FactionClock struct {
	Segments    int    `json:"segments"`              // 总段数（常用 4/6/8）
	Progress    int    `json:"progress"`              // 已走段数
	Consequence string `json:"consequence,omitempty"` // 走满后世界发生什么
	Pace        string `json:"pace,omitempty"`        // 推进速率提示："每弧 1-2 段"
}

// Tick 拨钟 n 段（封顶不溢出）；返回拨后是否走满。
func (c *FactionClock) Tick(n int) bool {
	if c == nil || c.Segments <= 0 {
		return false
	}
	c.Progress += n
	if c.Progress > c.Segments {
		c.Progress = c.Segments
	}
	if c.Progress < 0 {
		c.Progress = 0
	}
	return c.Progress >= c.Segments
}

// IsComplete 钟是否已走满。
func (c *FactionClock) IsComplete() bool {
	return c != nil && c.Segments > 0 && c.Progress >= c.Segments
}

type WorldFaction struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Aliases   []string          `json:"aliases,omitempty"`
	Goal      string            `json:"goal,omitempty"`
	Resources []string          `json:"resources,omitempty"`
	Relations []FactionRelation `json:"relations,omitempty"`
	Tags      []string          `json:"tags,omitempty"`

	// 矛盾网语义：立场（对主角）、内部张力、核心价值观。
	Stance          string   `json:"stance,omitempty"`           // friendly / neutral / hostile / unknown
	InternalTension string   `json:"internal_tension,omitempty"` // 势力内部矛盾（内讧是转折燃料）
	CoreValues      []string `json:"core_values,omitempty"`

	// Clock 势力进度钟：Goal 的推进状态（Blades 式），世界 tick 时拨动。
	Clock *FactionClock `json:"clock,omitempty"`
}

type FactionRelation struct {
	Target string `json:"target"`
	Kind   string `json:"kind"` // ally / rival / owner / debtor / hidden
	Note   string `json:"note,omitempty"`

	// 矛盾网语义：冲突的类型与当前烈度状态。
	ConflictType  string `json:"conflict_type,omitempty"`  // 种族 / 权力 / 法律 / 经济 / 信仰 / 资源
	ConflictState string `json:"conflict_state,omitempty"` // open_war / cold_war / truce / hidden_hostility / alliance
}

const CurrentRAGIndexSchemaVersion = 4

// RAGIndexState 记录本地/RAG 后端索引状态。Embedding 与 Qdrant 写入并发由 Config 控制。
type RAGIndexState struct {
	SchemaVersion   int            `json:"schema_version,omitempty"`
	Config          RAGIndexConfig `json:"config"`
	Chunks          []RAGChunk     `json:"chunks,omitempty"`
	ChunkHashes     []string       `json:"chunk_hashes,omitempty"`
	SanitizedDigest string         `json:"sanitized_digest,omitempty"`
	UpdatedAt       string         `json:"updated_at,omitempty"`
}

// CraftRecallNeed is a deterministic, review-derived request for reusable prose
// technique. Topic is deliberately engine-authored rather than copied from the
// rewrite brief so project names and negative examples cannot steer retrieval.
type CraftRecallNeed struct {
	ID          string   `json:"id"`
	Field       string   `json:"field"`
	Topic       string   `json:"topic"`
	TriggerRefs []string `json:"trigger_refs,omitempty"`
}

// CraftRecallReceiptHit is the auditable, method-only view of one selected RAG
// chunk. Text is intentionally absent: plans and drafts receive only a compact
// summary plus an immutable source reference, never raw benchmark prose.
type CraftRecallReceiptHit struct {
	Ref         string   `json:"ref"`
	ChunkID     string   `json:"chunk_id"`
	ChunkHash   string   `json:"chunk_hash"`
	SourcePath  string   `json:"source_path"`
	SourceKind  string   `json:"source_kind"`
	Facet       string   `json:"facet,omitempty"`
	Summary     string   `json:"summary,omitempty"`
	Score       float64  `json:"score"`
	UsageStages []string `json:"usage_stages,omitempty"`
}

type CraftRecallReceiptAttempt struct {
	Need           CraftRecallNeed         `json:"need"`
	Hits           []CraftRecallReceiptHit `json:"hits,omitempty"`
	NoMaterial     bool                    `json:"no_material"`
	FilteredCount  int                     `json:"filtered_count,omitempty"`
	FilteredReason map[string]int          `json:"filtered_reason,omitempty"`
}

// CraftRecallReceipt binds a rewrite-time technique recall to the exact canon
// generation, committed body, rewrite brief and RAG snapshot that produced it.
// Enforcement is set only by the new automatic preflight; historical plans that
// have no matching receipt remain valid.
type CraftRecallReceipt struct {
	Version            int                         `json:"version"`
	ID                 string                      `json:"id"`
	Chapter            int                         `json:"chapter"`
	Stage              string                      `json:"stage"`
	GenerationID       string                      `json:"generation_id,omitempty"`
	RewriteBodyPath    string                      `json:"rewrite_body_path"`
	RewriteBodySHA256  string                      `json:"rewrite_body_sha256"`
	RewriteBriefPath   string                      `json:"rewrite_brief_path"`
	RewriteBriefSHA256 string                      `json:"rewrite_brief_sha256"`
	IndexIdentity      string                      `json:"index_identity"`
	IndexUpdatedAt     string                      `json:"index_updated_at,omitempty"`
	PayloadSHA256      string                      `json:"payload_sha256"`
	Enforcement        bool                        `json:"enforcement"`
	CreatedAt          string                      `json:"created_at"`
	Attempts           []CraftRecallReceiptAttempt `json:"attempts,omitempty"`
}

type RAGPendingUpserts struct {
	Chunks    []RAGChunk `json:"chunks"`
	LastError string     `json:"last_error,omitempty"`
	UpdatedAt string     `json:"updated_at,omitempty"`
}

type RAGIndexConfig struct {
	EmbeddingConcurrency   int    `json:"embedding_concurrency"`
	QdrantWriteConcurrency int    `json:"qdrant_write_concurrency"`
	VectorBatchSize        int    `json:"vector_batch_size,omitempty"`
	Collection             string `json:"collection,omitempty"`
	EmbeddingProvider      string `json:"embedding_provider,omitempty"`
	EmbeddingModel         string `json:"embedding_model,omitempty"`
	VectorStore            string `json:"vector_store,omitempty"`
	VectorDimension        int    `json:"vector_dimension,omitempty"`
	QdrantURL              string `json:"qdrant_url,omitempty"`
}

type RAGChunk struct {
	ID         string         `json:"id"`
	SourcePath string         `json:"source_path"`
	SourceKind string         `json:"source_kind"` // deconstruction / knowledge / chapter_summary_facts / note
	Facet      string         `json:"facet,omitempty"`
	ParentID   string         `json:"parent_id,omitempty"`
	Hash       string         `json:"hash"`
	Context    string         `json:"context,omitempty"` // chunk 所属作品/章节/小节等局部上下文，参与 embedding 与本地检索
	Text       string         `json:"text,omitempty"`
	Summary    string         `json:"summary,omitempty"`
	Keywords   []string       `json:"keywords,omitempty"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// RetrievalTrace 记录召回为什么命中，便于后端追踪和诊断。
type RetrievalTrace struct {
	Query      string              `json:"query"`
	QueryTerms []string            `json:"query_terms,omitempty"`
	Strategy   string              `json:"strategy,omitempty"`
	MaxResults int                 `json:"max_results,omitempty"`
	Matches    []RetrievalTraceHit `json:"matches"`
	CreatedAt  string              `json:"created_at,omitempty"`
}

type RetrievalTraceHit struct {
	ChunkID       string   `json:"chunk_id"`
	ContentSHA256 string   `json:"content_sha256,omitempty"`
	Score         float64  `json:"score"`
	Reasons       []string `json:"reasons,omitempty"`
	SourcePath    string   `json:"source_path,omitempty"`
	Facet         string   `json:"facet,omitempty"`
	SourceKind    string   `json:"source_kind,omitempty"`
	Context       string   `json:"context,omitempty"`
}

// RAGVectorStore 是本地持久化向量索引。它不是最终后端抽象，
// 但让 embedding + 向量召回在没有外部 Qdrant 服务时也能稳定跑通。
type RAGVectorStore struct {
	Config    RAGIndexConfig   `json:"config"`
	Points    []RAGVectorPoint `json:"points"`
	UpdatedAt string           `json:"updated_at,omitempty"`
}

type RAGVectorPoint struct {
	ID        string         `json:"id"`
	Hash      string         `json:"hash"`
	Vector    []float32      `json:"vector"`
	Payload   map[string]any `json:"payload,omitempty"`
	Chunk     RAGChunk       `json:"chunk"`
	UpdatedAt string         `json:"updated_at,omitempty"`
}

// ResourceClaim 记录角色/势力/地点的资源状态。
// status=booked 是已入账事实；status=pending 是待确认提案，不得作为正文既成事实写入。
type ResourceClaim struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Owner        string   `json:"owner,omitempty"`
	Kind         string   `json:"kind,omitempty"`
	Status       string   `json:"status"` // booked / pending / rejected / spent
	Risk         string   `json:"risk,omitempty"`
	Evidence     string   `json:"evidence,omitempty"`
	Chapter      int      `json:"chapter,omitempty"`
	Participants []string `json:"participants,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

type ResourceLedger struct {
	Version int             `json:"version"`
	Claims  []ResourceClaim `json:"claims"`
}

type ResourceAudit struct {
	Participants []string        `json:"participants,omitempty"`
	Booked       []ResourceClaim `json:"booked,omitempty"`
	Pending      []ResourceClaim `json:"pending,omitempty"`
	Warnings     []string        `json:"warnings,omitempty"`
}
