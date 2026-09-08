package domain

import (
	"encoding/json"
	"fmt"
	"strings"
)

const ArcRehearsalVersion = "arc-rehearsal.v1"

// Rehearsal is an author-side conditional forecast, never an execution receipt.
type ArcRehearsalInput struct {
	Version               string                       `json:"version"`
	ProtocolDigest        string                       `json:"protocol_digest,omitempty"`
	ArcID                 string                       `json:"arc_id"`
	ArcFirstChapter       int                          `json:"arc_first_chapter"`
	ArcLastChapter        int                          `json:"arc_last_chapter"`
	BaseCanonChapter      int                          `json:"base_canon_chapter"`
	BaseCanonRoot         string                       `json:"base_canon_root"`
	SourceRoot            string                       `json:"source_root"`
	SourceFiles           map[string]string            `json:"source_files"`
	Outline               []OutlineEntry               `json:"outline"`
	CharacterObservations []CharacterObservationPacket `json:"character_observations"`
	WorldRules            []WorldRule                  `json:"world_rules"`
	WorldCodex            *WorldCodex                  `json:"world_codex"`
	BookWorld             *BookWorld                   `json:"book_world"`
	WorldState            *WorldPhysicalStateV2        `json:"world_state"`
	HardContracts         []string                     `json:"hard_contracts"`
	UserRules             json.RawMessage              `json:"user_rules"`
	AcceptedSummaries     []ChapterSummary             `json:"accepted_summaries,omitempty"`
	AcceptedEvidence      map[int]string               `json:"accepted_evidence,omitempty"`
	InputDigest           string                       `json:"input_digest"`
}

type ArcRehearsalChapter struct {
	Chapter              int      `json:"chapter"`
	ConditionalForecast  string   `json:"conditional_forecast"`
	Assumptions          []string `json:"assumptions"`
	CausalLinks          []string `json:"causal_links"`
	TimeResourceChecks   []string `json:"time_resource_checks"`
	AcceptedSourceDigest string   `json:"accepted_source_digest,omitempty"`
}

type ArcRehearsalCharacterConflict struct {
	Character          string   `json:"character"`
	CurrentGoal        string   `json:"current_goal"`
	Conflicts          []string `json:"conflicts"`
	ConditionalChoices []string `json:"conditional_choices"`
}

type ArcRehearsalContractCheck struct {
	Contract   string   `json:"contract"`
	Assessment string   `json:"assessment"` // plausible / conditional / unresolved / infeasible_prediction
	Conditions []string `json:"conditions"`
}

type ArcRehearsalMaterialCheck struct {
	Operation        string   `json:"operation"`
	RequiresReadable bool     `json:"requires_readable"`
	ResourceRefs     []string `json:"resource_refs"`
	Status           string   `json:"status"` // available / missing / unclear / not_required
	Explanation      string   `json:"explanation"`
}

type ArcRehearsalBody struct {
	Summary            string                          `json:"summary"`
	Chapters           []ArcRehearsalChapter           `json:"chapters"`
	CharacterConflicts []ArcRehearsalCharacterConflict `json:"character_conflicts"`
	ContractChecks     []ArcRehearsalContractCheck     `json:"contract_checks"`
	MaterialChecks     []ArcRehearsalMaterialCheck     `json:"material_checks"`
	UnresolvedItems    []string                        `json:"unresolved_items"`
}

// These identifiers are filled from the actual observed model response and
// existing usage hooks by the runner; the submission schema cannot author them.
type ArcRehearsalCall struct {
	Role           string   `json:"role"`
	Provider       string   `json:"provider"`
	Model          string   `json:"model"`
	UsageIDs       []string `json:"usage_ids"`
	ToolCallID     string   `json:"tool_call_id"`
	ResponseDigest string   `json:"response_digest"`
}

type ArcRehearsalDraft struct {
	Version     string           `json:"version"`
	Authority   string           `json:"authority"`
	InputDigest string           `json:"input_digest"`
	Body        ArcRehearsalBody `json:"body"`
	Call        ArcRehearsalCall `json:"call"`
	DraftDigest string           `json:"draft_digest"`
}

type ArcRehearsalReport struct {
	Version          string           `json:"version"`
	Authority        string           `json:"authority"`
	ArcID            string           `json:"arc_id"`
	ArcFirstChapter  int              `json:"arc_first_chapter"`
	ArcLastChapter   int              `json:"arc_last_chapter"`
	BaseCanonChapter int              `json:"base_canon_chapter"`
	BaseCanonRoot    string           `json:"base_canon_root"`
	SourceRoot       string           `json:"source_root"`
	InputDigest      string           `json:"input_digest"`
	DraftDigest      string           `json:"draft_digest"`
	ReadyForDetail   bool             `json:"ready_for_detail"`
	Body             ArcRehearsalBody `json:"body"`
	Call             ArcRehearsalCall `json:"call"`
	ReportDigest     string           `json:"report_digest"`
}

func FinalizeArcRehearsalInput(input ArcRehearsalInput) (ArcRehearsalInput, error) {
	if input.Version == "" {
		input.Version = ArcRehearsalVersion
	}
	if input.Version != ArcRehearsalVersion || strings.TrimSpace(input.ArcID) == "" || input.ArcFirstChapter < 1 || input.ArcLastChapter < input.ArcFirstChapter || input.BaseCanonChapter < input.ArcFirstChapter-1 || input.BaseCanonChapter >= input.ArcLastChapter || !characterSourceDigestPatternV2.MatchString(input.BaseCanonRoot) || !characterSourceDigestPatternV2.MatchString(input.SourceRoot) {
		return input, fmt.Errorf("arc rehearsal requires exact arc, accepted prefix and source identities")
	}
	// Missing is a historical input written before policy-bound rehearsal.
	// Such artifacts remain verifiable; new runners bind their actual policy.
	if input.ProtocolDigest != "" && !characterSourceDigestPatternV2.MatchString(input.ProtocolDigest) {
		return input, fmt.Errorf("arc rehearsal has an invalid protocol digest")
	}
	if len(input.Outline) != input.ArcLastChapter-input.ArcFirstChapter+1 || len(input.CharacterObservations) == 0 || len(input.HardContracts) == 0 || input.WorldState == nil {
		return input, fmt.Errorf("arc rehearsal input lacks complete arc slots, current characters, hard contracts or world state")
	}
	for i, chapter := range input.Outline {
		if chapter.Chapter != input.ArcFirstChapter+i {
			return input, fmt.Errorf("arc rehearsal outline must cover every ordered chapter")
		}
	}
	if err := ValidateWorldPhysicalStateV2(*input.WorldState); err != nil {
		return input, err
	}
	if len(input.SourceFiles) == 0 {
		return input, fmt.Errorf("rehearsal requires explicit frozen source-file hashes")
	}
	for name, digest := range input.SourceFiles {
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.HasPrefix(name, "meta/planning/arc_rehearsals") || !characterSourceDigestPatternV2.MatchString(digest) {
			return input, fmt.Errorf("invalid rehearsal source-file binding")
		}
	}
	seen := map[string]bool{}
	for _, observation := range input.CharacterObservations {
		valid, err := FinalizeCharacterObservationPacket(observation)
		if err != nil || !samePhysicalValueV2(valid, observation) || observation.Chapter != input.BaseCanonChapter+1 || seen[observation.Character] {
			return input, fmt.Errorf("rehearsal requires exact host current-character observations")
		}
		seen[observation.Character] = true
	}
	summaries := map[int]string{}
	for _, summary := range input.AcceptedSummaries {
		summaries[summary.Chapter] = summary.Summary
	}
	for chapter := input.ArcFirstChapter; chapter <= input.BaseCanonChapter; chapter++ {
		if summaries[chapter] == "" || !characterSourceDigestPatternV2.MatchString(input.AcceptedEvidence[chapter]) {
			return input, fmt.Errorf("rehearsal lacks accepted evidence for historical chapter %d", chapter)
		}
	}
	input.InputDigest = ""
	digest, err := DeterministicPlanningHash(input)
	if err != nil {
		return input, err
	}
	input.InputDigest = "sha256:" + digest
	return input, nil
}

func ValidateArcRehearsalBody(input ArcRehearsalInput, body ArcRehearsalBody) error {
	if strings.TrimSpace(body.Summary) == "" || len(body.Chapters) != len(input.Outline) || len(body.MaterialChecks) == 0 {
		return fmt.Errorf("rehearsal requires a substantive conditional forecast for the whole arc and explicit material checks")
	}
	for i, chapter := range body.Chapters {
		if chapter.Chapter != input.ArcFirstChapter+i || strings.TrimSpace(chapter.ConditionalForecast) == "" || len(nonEmptyWorldStrings(chapter.Assumptions)) == 0 || len(nonEmptyWorldStrings(chapter.CausalLinks)) == 0 || len(nonEmptyWorldStrings(chapter.TimeResourceChecks)) == 0 {
			return fmt.Errorf("rehearsal chapter %d lacks explicit conditions, causal links or time/resource checks", chapter.Chapter)
		}
		if chapter.Chapter <= input.BaseCanonChapter {
			for _, summary := range input.AcceptedSummaries {
				if summary.Chapter == chapter.Chapter && (chapter.AcceptedSourceDigest != input.AcceptedEvidence[chapter.Chapter] || chapter.ConditionalForecast != summary.Summary) {
					return fmt.Errorf("rehearsal cannot rewrite accepted chapter %d", chapter.Chapter)
				}
			}
		} else if chapter.AcceptedSourceDigest != "" {
			return fmt.Errorf("future rehearsal chapter cannot claim accepted evidence")
		}
	}
	characters := map[string]string{}
	for _, o := range input.CharacterObservations {
		characters[o.Character] = o.CurrentGoal
	}
	for _, c := range body.CharacterConflicts {
		goal, ok := characters[c.Character]
		if !ok || c.CurrentGoal != goal || len(nonEmptyWorldStrings(c.Conflicts)) == 0 || len(nonEmptyWorldStrings(c.ConditionalChoices)) == 0 {
			return fmt.Errorf("rehearsal character conflicts must preserve each current goal and mark choices conditional")
		}
		delete(characters, c.Character)
	}
	if len(characters) != 0 {
		return fmt.Errorf("rehearsal omitted a current principal character")
	}
	contracts := map[string]bool{}
	for _, text := range input.HardContracts {
		contracts[text] = true
	}
	for _, c := range body.ContractChecks {
		if !contracts[c.Contract] || len(nonEmptyWorldStrings(c.Conditions)) == 0 {
			return fmt.Errorf("rehearsal contract checks must cover exact input contracts with conditions")
		}
		switch c.Assessment {
		case "plausible", "conditional", "unresolved", "infeasible_prediction":
		default:
			return fmt.Errorf("invalid speculative contract assessment")
		}
		delete(contracts, c.Contract)
	}
	if len(contracts) != 0 {
		return fmt.Errorf("rehearsal omitted a hard contract")
	}
	resources := map[string]WorldResourceBalanceV2{}
	for _, r := range input.WorldState.Resources {
		resources[r.ResourceID] = r
	}
	for _, m := range body.MaterialChecks {
		if strings.TrimSpace(m.Operation) == "" || strings.TrimSpace(m.Explanation) == "" {
			return fmt.Errorf("material checks require a named operation and a source-gap explanation")
		}
		switch m.Status {
		case "available", "missing", "unclear", "not_required":
		default:
			return fmt.Errorf("invalid rehearsal material status")
		}
		if m.RequiresReadable && (m.Status == "not_required" || (m.Status == "available" && len(m.ResourceRefs) == 0)) {
			return fmt.Errorf("read-dependent operation cannot claim availability without an existing readable resource")
		}
		for _, ref := range m.ResourceRefs {
			r, ok := resources[ref]
			if !ok {
				return fmt.Errorf("rehearsal cannot invent resource reference %q", ref)
			}
			if m.Status == "available" && m.RequiresReadable && len(r.ReadableFacts) == 0 {
				return fmt.Errorf("rehearsal resource %q has no readable facts", ref)
			}
		}
	}
	raw, _ := json.Marshal(body)
	if len(raw) > 256*1024 {
		return fmt.Errorf("arc rehearsal exceeds its bounded report size")
	}
	return nil
}

func validArcRehearsalCall(call ArcRehearsalCall, role string) bool {
	if call.Role != role || call.Provider == "" || call.Model == "" || len(call.UsageIDs) == 0 || call.ToolCallID == "" || !characterSourceDigestPatternV2.MatchString(call.ResponseDigest) {
		return false
	}
	seen := map[string]bool{}
	for _, id := range call.UsageIDs {
		if id == "" || strings.TrimSpace(id) != id || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}

func FinalizeArcRehearsalDraft(input ArcRehearsalInput, draft ArcRehearsalDraft) (ArcRehearsalDraft, error) {
	if err := ValidateArcRehearsalBody(input, draft.Body); err != nil {
		return draft, err
	}
	if !validArcRehearsalCall(draft.Call, "architect") {
		return draft, fmt.Errorf("rehearsal draft lacks actual Architect response/usage sources")
	}
	draft.Version, draft.Authority, draft.InputDigest = ArcRehearsalVersion, "speculative", input.InputDigest
	draft.DraftDigest = ""
	digest, err := DeterministicPlanningHash(draft)
	draft.DraftDigest = "sha256:" + digest
	return draft, err
}

func FinalizeArcRehearsalReport(input ArcRehearsalInput, draft ArcRehearsalDraft, report ArcRehearsalReport) (ArcRehearsalReport, error) {
	valid, err := FinalizeArcRehearsalDraft(input, draft)
	if err != nil || !samePhysicalValueV2(valid, draft) {
		return report, fmt.Errorf("rehearsal review lacks its exact verified draft")
	}
	if err := ValidateArcRehearsalBody(input, report.Body); err != nil {
		return report, err
	}
	for _, prior := range draft.Body.MaterialChecks {
		found := false
		for _, current := range report.Body.MaterialChecks {
			if current.Operation == prior.Operation {
				found = true
				if prior.RequiresReadable && !current.RequiresReadable {
					return report, fmt.Errorf("review cannot remove a declared readable-material dependency")
				}
			}
		}
		if !found {
			return report, fmt.Errorf("review omitted draft material check %q", prior.Operation)
		}
	}
	if !validArcRehearsalCall(report.Call, "world_arbiter") {
		return report, fmt.Errorf("rehearsal review lacks actual Arbiter response/usage sources")
	}
	for _, draftID := range draft.Call.UsageIDs {
		for _, reviewID := range report.Call.UsageIDs {
			if draftID == reviewID {
				return report, fmt.Errorf("rehearsal draft and review cannot reuse one model-call usage identity")
			}
		}
	}
	report.Version, report.Authority = ArcRehearsalVersion, "speculative"
	report.ArcID, report.ArcFirstChapter, report.ArcLastChapter = input.ArcID, input.ArcFirstChapter, input.ArcLastChapter
	report.BaseCanonChapter, report.BaseCanonRoot, report.SourceRoot = input.BaseCanonChapter, input.BaseCanonRoot, input.SourceRoot
	report.InputDigest, report.DraftDigest = input.InputDigest, draft.DraftDigest
	report.ReadyForDetail = true
	for _, check := range report.Body.ContractChecks {
		if check.Assessment == "unresolved" || check.Assessment == "infeasible_prediction" {
			report.ReadyForDetail = false
		}
	}
	for _, check := range report.Body.MaterialChecks {
		if check.Status == "missing" || check.Status == "unclear" {
			report.ReadyForDetail = false
		}
	}
	report.ReportDigest = ""
	digest, err := DeterministicPlanningHash(report)
	report.ReportDigest = "sha256:" + digest
	return report, err
}
