package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestProjectAllPhysicalAcceptedCanonRestartsWithoutProjectedSidecars(t *testing.T) {
	opts, st, identity := projectAllCmdTestInstallThreeChapterCLIProjectionWithMutator(t, func(chapter int, artifacts *agents.ProjectedChapterArtifacts) {
		if chapter != 1 {
			return
		}
		generation, _ := projectAllCmdTestGenerationAndRegistry(t, 3)
		generation.GenerationID = artifacts.WorldSimulation.GenerationID
		physical, _, _ := projectAllPhysicalArtifacts(t, generation, true)
		*artifacts = *physical
	}, "v2")
	if identity.Generation.CharacterAgentProtocol != domain.CharacterAgentDecisionProtocolV2Version || identity.Generation.FirstProjectedChapter != 1 || identity.Generation.LastProjectedChapter != 1 {
		t.Fatal("physical acceptance fixture must bind a wholly v2 current generation")
	}
	if err := pipelineSeal(opts, pipelineFlags{}); err != nil {
		t.Fatal(err)
	}
	t.Run("sealed evidence alone is not promotion", func(t *testing.T) {
		probe := store.NewStore(t.TempDir())
		if err := copyProjectAllWorkspace(st.Dir(), probe.Dir()); err != nil {
			t.Fatal(err)
		}
		if err := copyProjectAllWorkspace(filepath.Join(st.Dir(), "meta/planning"), filepath.Join(probe.Dir(), "meta/planning")); err != nil {
			t.Fatal(err)
		}
		bundles, err := probe.ProjectedV2().LoadProjectedChapterBundles(identity.Generation.GenerationID)
		if err != nil || len(bundles) != 1 {
			t.Fatal(err)
		}
		bundle := bundles[0]
		if err := probe.SaveChapterWorldSimulation(bundle.ChapterWorldSimulation); err != nil {
			t.Fatal(err)
		}
		if err := probe.Drafts.SaveChapterPlan(bundle.ChapterPlan); err != nil {
			t.Fatal(err)
		}
		if err := probe.RAG.SaveRAGFactReceipt(*bundle.RAGFactReceipt); err != nil {
			t.Fatal(err)
		}
		if err := probe.RAG.SaveCraftRecallReceipt(*bundle.CraftRecallReceipt); err != nil {
			t.Fatal(err)
		}
		if _, err := probe.Checkpoints.AppendArtifact(domain.ChapterScope(1), "chapter_world_simulation", "meta/chapter_simulations/001.json"); err != nil {
			t.Fatal(err)
		}
		if _, err := probe.Checkpoints.AppendArtifact(domain.ChapterScope(1), "plan", "drafts/01.plan.json"); err != nil {
			t.Fatal(err)
		}
		if err := tools.ValidateCurrentChapterRenderPlanForExecution(probe, 1); err == nil || (!strings.Contains(err.Error(), "character-agent") && !strings.Contains(err.Error(), "grounding")) {
			t.Fatalf("sealed-only simulation bypassed promoted protocol gate: %v", err)
		}
	})
	if err := pipelinePromote(opts, pipelineFlags{Start: 1, End: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, 1); err != nil {
		t.Fatalf("exact promoted evidence cannot validate current plan: %v", err)
	}
	for _, mode := range []string{"generation", "chapter", "receipt"} {
		t.Run("promoted binding rejects wrong "+mode, func(t *testing.T) {
			path := filepath.Join(st.Dir(), "meta/planning/v2/realization_cursor.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(raw)) })
			var cursor domain.RealizationCursorV2
			if err := json.Unmarshal(raw, &cursor); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "generation":
				cursor.ActiveGenerationID = "pg2_wrong_generation"
			case "chapter":
				cursor.ActivePromotedChapter = 2
			case "receipt":
				cursor.ActivePromotionReceiptDigest = projectAllCmdTestDigest("wrong-promotion")
			}
			cursor.CursorDigest, err = domain.ComputeRealizationCursorV2Digest(cursor)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := writePipelinePlanningJSON(path, cursor); err != nil {
				t.Fatal(err)
			}
			if err := tools.ValidateCurrentChapterRenderPlanForExecution(st, 1); err == nil {
				t.Fatal("wrong promoted cursor authorized the simulation")
			}
		})
	}
	if _, err := st.LoadAcceptedCharacterAgentBundle(1); err == nil {
		t.Fatal("promoted but unaccepted physical state became canon")
	}
	if _, err := agents.BuildCharacterObservationsForProjectedState(st, "pg2_unaccepted_next", 2, domain.ProjectedPlanningContextV2{}); err == nil {
		t.Fatal("unaccepted promotion became next chapter's canonical physical baseline")
	}
	frozen, _, err := loadAndVerifyPipelineFrozenPlan(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	binding, err := validatePipelineSealedRenderBinding(st, frozen, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range binding.Bundle.CharacterAgentEvidence.Registry.Entries {
		memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
		if err != nil {
			t.Fatal(err)
		}
		if memory != nil && memory.LastAcceptedChapter > 0 {
			t.Fatal("promotion alone canonized an unaccepted decision")
		}
		// A discarded generation's projected memory is deliberately tempting
		// but must never be copied into this acceptance or the next generation.
		if err := st.CharacterAgents.SaveProjectedMemory(domain.CharacterAgentMemory{AgentID: actor.AgentID, Character: actor.Character, State: "projected", GenerationID: "pg2_abandoned_physical", Facts: []domain.CharacterAgentMemoryFact{{ID: "abandoned", Chapter: 1, Kind: "projected", Text: "废弃代次独有秘密", SourceDigest: "sha256:abandoned"}}}); err != nil {
			t.Fatal(err)
		}
	}
	body := "第一章\n\n主角来到B，收好了尚未再次测量的设备。"
	projectAllCmdTestWriteFile(t, filepath.Join(st.Dir(), "chapters/01.md"), body)
	bodySHA, err := pipelineRequiredFileSHA(st.Dir(), "chapters/01.md")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := st.Checkpoints.Append(domain.ChapterScope(1), "commit", "chapters/01.md", bodySHA)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, len([]rune(body)), "choice", "main"); err != nil {
		t.Fatal(err)
	}
	match := &pipelineSealedActualDeltaMatch{ActualDelta: binding.Bundle.ProjectedDelta, ProjectionMatch: true, Complete: true, ObligationsSatisfied: binding.Bundle.ObligationsConsumed}
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	actualCanonRoot, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	committedRawRoot, err := pipelineCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	committedTransactionRoot := domain.PlanningV2DigestPrefix + committedRawRoot
	t.Run("prepared memory is not acceptance", func(t *testing.T) {
		preview := projectAllCmdTestOutcome(t, binding.Bundle, binding.Promotion, 1)
		preview.ChapterBodySHA256 = bodySHA
		preview.CommitCheckpointSeq = commit.Seq
		preview.ActualCanonRoot = actualCanonRoot
		preview.ReceiptDigest, err = domain.ComputeActualOutcomeReceiptV2Digest(preview)
		if err != nil {
			t.Fatal(err)
		}
		before, err := store.DirectoryContentRoot(filepath.Join(st.Dir(), "meta/character_agents"))
		if err != nil {
			t.Fatal(err)
		}
		bundleBefore, _ := json.Marshal(binding.Bundle)
		if err := preparePipelineAcceptedCharacterMemoryPublication(st, binding.Bundle, preview); err != nil {
			t.Fatal(err)
		}
		bundleAfter, _ := json.Marshal(binding.Bundle)
		if string(bundleBefore) != string(bundleAfter) {
			t.Fatal("pure memory candidate mutated immutable bundle evidence")
		}
		manifest, err := st.LoadCharacterMemoryPublication(preview.ReceiptDigest)
		if err != nil || manifest == nil {
			t.Fatal(err)
		}
		if err := st.ApplyPreparedCharacterMemoryPublication(manifest); err == nil {
			t.Fatal("prepared memory was applied before any accepted outcome")
		}
		// Record writes the immutable outcome only; unlike Save it does not
		// advance the realization cursor and therefore grants no canon write.
		if _, err := st.ProjectedV2().RecordActualOutcomeReceipt(preview); err != nil {
			t.Fatal(err)
		}
		if err := st.ApplyPreparedCharacterMemoryPublication(manifest); err == nil {
			t.Fatal("recorded-only outcome was mistaken for accepted cursor")
		}
		if after, _ := store.DirectoryContentRoot(filepath.Join(st.Dir(), "meta/character_agents")); after != before {
			t.Fatal("rejected pre-accept application wrote canonical files")
		}
	})
	outcome, err := acceptPipelineSealedRenderOutcome(st, binding, commit, bodySHA, actualCanonRoot, match)
	if err != nil {
		t.Fatal(err)
	}
	afterCanonRoot, err := pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	if afterCanonRoot == "" {
		t.Fatal("accepted canon root is empty")
	}
	publishedRawRoot, err := pipelineCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineCommittedCanonWithMemoryPublication(st.Dir(), frozen, bodySHA, commit, committedTransactionRoot, publishedRawRoot); err != nil {
		t.Errorf("fully published memory broke exact committed transaction recovery: %v", err)
	}
	t.Run("committed recovery remains exact body and publication bound", func(t *testing.T) {
		wrongCommit := *commit
		wrongCommit.Seq++
		wrongBundle := *frozen
		wrongBundle.ProjectedBundleDigest = projectAllCmdTestDigest("another-bundle")
		for _, test := range []struct {
			name string
			plan *pipelineFrozenPlan
			body string
			cp   *domain.Checkpoint
			root string
		}{
			{"body", frozen, projectAllCmdTestDigest("other-body"), commit, committedTransactionRoot},
			{"commit", frozen, bodySHA, &wrongCommit, committedTransactionRoot},
			{"bundle", &wrongBundle, bodySHA, commit, committedTransactionRoot},
			{"before-root", frozen, bodySHA, commit, projectAllCmdTestDigest("other-committed-root")},
		} {
			if err := validatePipelineCommittedCanonWithMemoryPublication(st.Dir(), test.plan, test.body, test.cp, test.root, publishedRawRoot); err == nil {
				t.Fatalf("committed recovery ignored %s mismatch", test.name)
			}
		}
	})
	acceptedCursor, err := st.ProjectedV2().LoadRealizationCursor()
	if err != nil {
		t.Fatal(err)
	}
	if err := validatePipelineProjectAllLiveCanonForPromotion(st.Dir(), progress, st.ProjectedV2(), acceptedCursor, &binding.Generation); err != nil {
		t.Errorf("receipt-derived accepted memory cannot authorize next promotion: %v", err)
	}
	if err := validatePipelineCharacterMemoryBeforeSource(st, progress); err != nil {
		t.Errorf("accepted memory cannot form a verified next source snapshot: %v", err)
	}
	acceptedBundle, err := st.LoadAcceptedCharacterAgentBundle(1)
	if err != nil || acceptedBundle == nil {
		t.Fatalf("cannot load sealed accepted physical baseline: %v", err)
	}
	readRoot, err := store.DirectoryContentRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.LoadAcceptedCharacterAgentBundle(1); err != nil {
		t.Fatal(err)
	}
	if afterRead, err := store.DirectoryContentRoot(st.Dir()); err != nil || afterRead != readRoot {
		t.Fatalf("accepted evidence lookup was not read-only: %v", err)
	}
	t.Run("body drift fails closed", func(t *testing.T) {
		path := filepath.Join(st.Dir(), "chapters/01.md")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(before)) })
		projectAllCmdTestWriteFile(t, path, string(before)+"这句话未被接受。")
		if _, err := st.LoadAcceptedCharacterAgentBundle(1); err == nil {
			t.Fatal("changed body reused accepted physical result")
		}
	})
	t.Run("abandoned generation cannot pose as canon", func(t *testing.T) {
		before, err := st.LoadChapterWorldSimulation(1)
		if err != nil || before == nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := st.SaveChapterWorldSimulation(*before); err != nil {
				t.Fatal(err)
			}
		})
		forged := *before
		forged.GenerationID = "pg2_abandoned_physical"
		if err := st.SaveChapterWorldSimulation(forged); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadAcceptedCharacterAgentBundle(1); err == nil {
			t.Fatal("unaccepted generation reused another generation's acceptance")
		}
	})
	if _, err := st.LoadAcceptedCharacterAgentBundle(2); err == nil {
		t.Fatal("unaccepted chapter reused a predecessor physical result")
	}
	t.Run("canonical memory drift remains forbidden", func(t *testing.T) {
		actor := acceptedBundle.CharacterAgentEvidence.Registry.Entries[0]
		memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
		if err != nil || memory == nil {
			t.Fatal(err)
		}
		path := filepath.Join(st.Dir(), "meta/character_agents/memory", actor.AgentID+".json")
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(before)) })
		memory.Facts = append(memory.Facts, domain.CharacterAgentMemoryFact{ID: "forged-accepted", Chapter: 1, Kind: "accepted_decision_outcome", Text: "无来源的秘密", SourceDigest: outcome.ReceiptDigest, Accepted: true})
		if err := st.CharacterAgents.SaveCanonicalMemory(*memory); err != nil {
			t.Fatal(err)
		}
		if err := validatePipelineProjectAllLiveCanonForPromotion(st.Dir(), progress, st.ProjectedV2(), acceptedCursor, &binding.Generation); err == nil {
			t.Fatal("unattested memory change was ignored by canon validation")
		}
		if err := validatePipelineCharacterMemoryBeforeSource(st, progress); err == nil {
			t.Fatal("a new source snapshot absorbed unverified canonical memory")
		}
	})
	for _, actor := range binding.Bundle.CharacterAgentEvidence.Registry.Entries {
		memory, err := st.CharacterAgents.LoadCanonicalMemory(actor.AgentID)
		if err != nil || memory == nil {
			t.Fatalf("accepted canonical memory missing: %v", err)
		}
		if memory.LastAcceptedChapter != 1 {
			t.Fatalf("accepted memory chapter mismatch: %+v", memory)
		}
		raw, _ := json.Marshal(memory)
		if strings.Contains(string(raw), "废弃代次独有秘密") || strings.Contains(string(raw), "11.8") || strings.Contains(string(raw), "作者私有实量") || strings.Contains(string(raw), "未读到的作者私有记录") {
			t.Fatalf("unauthorized truth entered canonical memory: %s", raw)
		}
		if actor.Character == "主角" && (!strings.Contains(string(raw), "我提醒你：A通往B。") || !strings.Contains(string(raw), "铭牌记载：检修期曾换过阀门。")) {
			t.Fatalf("accepted delivered/read knowledge was discarded: %s", raw)
		}
		if actor.Character != "主角" && strings.Contains(string(raw), "铭牌记载：检修期曾换过阀门。") {
			t.Fatal("recipient-only document knowledge entered another actor's memory")
		}
		if len(memory.Facts) != 1 || !memory.Facts[0].Accepted || memory.Facts[0].SourceDigest != outcome.ReceiptDigest {
			t.Fatalf("memory is not bound to actual acceptance: %+v", memory)
		}
	}
	// Crash recovery repeats the acceptance hook; it must not append the same
	// private outcome again or rewrite canonical bytes.
	beforeReplay, _ := store.DirectoryContentRoot(filepath.Join(st.Dir(), "meta/character_agents"))
	binding.Outcome = outcome
	if _, err := acceptPipelineSealedRenderOutcome(st, binding, commit, bodySHA, actualCanonRoot, match); err != nil {
		t.Fatal(err)
	}
	if afterReplay, _ := store.DirectoryContentRoot(filepath.Join(st.Dir(), "meta/character_agents")); afterReplay != beforeReplay {
		t.Fatal("accepted memory recovery was not idempotent")
	}
	t.Run("prepared partial memory publication recovers exactly", func(t *testing.T) {
		actor := acceptedBundle.CharacterAgentEvidence.Registry.Entries[0]
		path := filepath.Join(st.Dir(), "meta/character_agents/memory", actor.AgentID+".json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(original)) })
		// The file was absent in this exact prepared before-state. Restore
		// that one state to emulate interruption between two atomic writes.
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		partialRawRoot, err := pipelineCanonRoot(st.Dir(), progress)
		if err != nil {
			t.Fatal(err)
		}
		if err := validatePipelineCommittedCanonWithMemoryPublication(st.Dir(), frozen, bodySHA, commit, committedTransactionRoot, partialRawRoot); err != nil {
			t.Fatalf("prepared partial publication blocked committed transaction recovery: %v", err)
		}
		if err := validatePipelineAcceptedCharacterMemoryPublication(st, outcome, true); err == nil {
			t.Fatal("partially published memory was considered complete")
		}
		if _, err := acceptPipelineSealedRenderOutcome(st, binding, commit, bodySHA, actualCanonRoot, match); err != nil {
			t.Fatalf("exact partial publication did not recover: %v", err)
		}
		restored, err := os.ReadFile(path)
		if err != nil || string(restored) != string(original) {
			t.Fatalf("recovery changed accepted memory rather than restoring it: %v", err)
		}
	})
	t.Run("concurrent stores apply once", func(t *testing.T) {
		manifest, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
		if err != nil || manifest == nil {
			t.Fatal(err)
		}
		actor := acceptedBundle.CharacterAgentEvidence.Registry.Entries[0]
		path := filepath.Join(st.Dir(), "meta/character_agents/memory", actor.AgentID+".json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(original)) })
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		errs := make(chan error, 4)
		for i := 0; i < 4; i++ {
			go func() { errs <- store.NewStore(st.Dir()).ApplyPreparedCharacterMemoryPublication(manifest) }()
		}
		for i := 0; i < 4; i++ {
			if err := <-errs; err != nil {
				t.Fatal(err)
			}
		}
		if after, _ := store.DirectoryContentRoot(filepath.Join(st.Dir(), "meta/character_agents")); after != beforeReplay {
			t.Fatal("concurrent application rewrote bytes or incremented memory version twice")
		}
	})
	t.Run("resigned publication cannot invent accepted knowledge", func(t *testing.T) {
		manifest, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest)
		if err != nil || manifest == nil {
			t.Fatal(err)
		}
		manifestPath := filepath.Join(st.Dir(), "meta/planning/v2/character_memory_publications", outcome.ReceiptDigest+".json")
		originalManifest, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, manifestPath, string(originalManifest)) })
		changed := false
		for i := range manifest.Files {
			file := &manifest.Files[i]
			if !strings.Contains(filepath.ToSlash(file.Path), "/memory/") {
				continue
			}
			path := filepath.Join(st.Dir(), file.Path)
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(original)) })
			var memory domain.CharacterAgentMemory
			if err := json.Unmarshal(file.After, &memory); err != nil {
				t.Fatal(err)
			}
			memory.Facts[0].Text = "重新签名也不能把未授权秘密写入角色。"
			memory, err = domain.FinalizeCharacterAgentMemory(memory)
			if err != nil {
				t.Fatal(err)
			}
			file.After, err = json.MarshalIndent(memory, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			file.AfterSHA256 = pipelineBytesSHA(file.After)
			projectAllCmdTestWriteFile(t, path, string(file.After))
			changed = true
			break
		}
		if !changed {
			t.Fatal("fixture has no published actor memory")
		}
		manifest.AfterCanonRoot, err = pipelineCharacterMemoryCanonRoot(st, manifest.AfterOverrides())
		if err != nil {
			t.Fatal(err)
		}
		manifest.PublicationDigest = ""
		raw, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		manifest.PublicationDigest, err = domain.ComputePlanningV2JSONDigest(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writePipelinePlanningJSON(manifestPath, manifest); err != nil {
			t.Fatal(err)
		}
		if _, err := st.LoadCharacterMemoryPublication(outcome.ReceiptDigest); err == nil || !strings.Contains(err.Error(), "deterministic") {
			t.Fatalf("self-consistent forged after-state was not rejected by independent derivation: %v", err)
		}
		if err := validatePipelineCharacterMemoryBeforeSource(st, progress); err == nil {
			t.Fatal("resigned invented knowledge became a new generation source")
		}
	})
	t.Run("memory recovery rejects unrelated canon mutation", func(t *testing.T) {
		path := filepath.Join(st.Dir(), "characters.json")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(original)) })
		projectAllCmdTestWriteFile(t, path, string(original)+"\n")
		before, err := store.DirectoryContentRoot(st.Dir())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := acceptPipelineSealedRenderOutcome(st, binding, commit, bodySHA, actualCanonRoot, match); err == nil {
			t.Fatal("memory override hid an unrelated character source mutation")
		}
		if after, _ := store.DirectoryContentRoot(st.Dir()); after != before {
			t.Fatal("rejected publication recovery rewrote canonical files")
		}
	})
	t.Run("next generation shadow keeps frozen accepted baseline", func(t *testing.T) {
		generation, registry := projectAllPhysicalNextGeneration(t, st, *acceptedBundle)
		workspace, err := preparePipelineProjectAllWorkspace(st.Dir(), generation.GenerationID, 1, false)
		if err != nil {
			t.Fatal(err)
		}
		shadow := store.NewStore(workspace)
		baseline, err := shadow.LoadProjectAllAcceptedCharacterBaseline(generation.GenerationID, 2, generation.BaseStateRoot)
		if err != nil || baseline == nil {
			t.Fatalf("new shadow lacks accepted baseline: %v", err)
		}
		if baseline.SourceBundleDigest != acceptedBundle.BundleDigest || baseline.SourceOutcomeDigest != outcome.ReceiptDigest {
			t.Fatal("shadow froze another accepted source")
		}
		if sim, err := shadow.LoadChapterWorldSimulation(1); err != nil || sim != nil {
			t.Fatalf("test did not exercise sanitized shadow: %v", err)
		}
		projected, err := domain.DeriveProjectedPlanningContextV2(generation, nil, registry, 2)
		if err != nil {
			t.Fatal(err)
		}
		observations, err := agents.BuildCharacterObservationsForProjectedState(shadow, generation.GenerationID, 2, projected)
		if err != nil {
			t.Fatal(err)
		}
		for _, observation := range observations {
			raw, _ := json.Marshal(observation)
			if observation.Location != "B" || strings.Contains(string(raw), "11.8") || strings.Contains(string(raw), "废弃代次独有秘密") || !strings.Contains(string(raw), "铭牌记载：检修期曾换过阀门。") {
				t.Fatalf("shadow did not consume exact accepted private baseline: %s", raw)
			}
		}
		if _, err := preparePipelineProjectAllWorkspace(st.Dir(), generation.GenerationID, 1, false); err != nil {
			t.Fatalf("unchanged workspace could not resume: %v", err)
		}
		t.Run("model views use frozen shadow not current live", func(t *testing.T) {
			liveBody := filepath.Join(st.Dir(), "chapters/01.md")
			original, err := os.ReadFile(liveBody)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { projectAllCmdTestWriteFile(t, liveBody, string(original)) })
			projectAllCmdTestWriteFile(t, liveBody, "此时live已改变，冻结的shadow不能回读它。")
			views, err := agents.BuildCharacterObservationsForProjectedState(shadow, generation.GenerationID, 2, projected)
			if err != nil || len(views) == 0 {
				t.Fatalf("frozen shadow tried to reload live acceptance: %v", err)
			}
			for _, view := range views {
				if view.Location != "B" {
					t.Fatalf("frozen accepted state changed: %+v", view)
				}
			}
		})
		if _, err := shadow.LoadProjectAllAcceptedCharacterBaseline("pg2_other_generation", 2, generation.BaseStateRoot); err == nil {
			t.Fatal("baseline crossed generation")
		}
		if _, err := shadow.LoadProjectAllAcceptedCharacterBaseline(generation.GenerationID, 3, generation.BaseStateRoot); err == nil {
			t.Fatal("baseline reset a later chapter to accepted opening state")
		}
		if _, err := shadow.LoadProjectAllAcceptedCharacterBaseline(generation.GenerationID, 2, projectAllCmdTestDigest("wrong-root")); err == nil {
			t.Fatal("baseline ignored exact source state root")
		}
		path := filepath.Join(workspace, store.ProjectAllAcceptedCharacterBaselinePath)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { projectAllCmdTestWriteFile(t, path, string(original)) })
		baseline.PhysicalState.Actors[0].Location = "伪造位置"
		if _, err := writePipelinePlanningJSON(path, baseline); err != nil {
			t.Fatal(err)
		}
		if _, err := shadow.LoadProjectAllAcceptedCharacterBaseline(generation.GenerationID, 2, generation.BaseStateRoot); err == nil {
			t.Fatal("tampered frozen baseline accepted")
		}
		if _, err := preparePipelineProjectAllWorkspace(st.Dir(), generation.GenerationID, 1, false); err == nil {
			t.Fatal("resume silently replaced a tampered baseline")
		}
	})
	// There are no projected sidecars in live after ordinary bundle promotion.
	// A new arc must recover physical truth from accepted immutable evidence,
	// not from an abandoned workspace or manually repopulated initial state.
	observations, err := agents.BuildCharacterObservationsForProjectedState(st, "pg2_next_arc", 2, domain.ProjectedPlanningContextV2{})
	if err != nil {
		t.Fatalf("accepted physical canon cannot seed the next generation without projected sidecars: %v", err)
	}
	for _, observation := range observations {
		if observation.Location != "B" {
			t.Fatalf("accepted physical position lost: %+v", observation)
		}
		raw, _ := json.Marshal(observation)
		if strings.Contains(string(raw), "废弃代次独有秘密") || strings.Contains(string(raw), "11.8") {
			t.Fatalf("next generation imported unauthorized memory: %s", raw)
		}
		if observation.Character == "主角" && (!strings.Contains(string(raw), "我提醒你：A通往B。") || !strings.Contains(string(raw), "铭牌记载：检修期曾换过阀门。")) {
			t.Fatalf("next generation lost accepted received knowledge: %s", raw)
		}
		if strings.Contains(string(raw), "未读到的作者私有记录") {
			t.Fatal("unread physical catalog content leaked to next generation")
		}
	}
}

func projectAllPhysicalNextGeneration(t *testing.T, st *store.Store, accepted domain.ProjectedChapterBundle) (domain.PlanningGenerationV2, domain.ObligationRegistryV2) {
	t.Helper()
	generation, registry := projectAllCmdTestGenerationAndRegistry(t, 2)
	progress, err := st.Progress.Load()
	if err != nil {
		t.Fatal(err)
	}
	generation.BaseCanonRoot, err = pipelineProjectAllLiveCanonRoot(st.Dir(), progress)
	if err != nil {
		t.Fatal(err)
	}
	generation.BaseCanonChapter = 1
	generation.BaseStateRoot = accepted.ProjectedPostStateRoot
	generation.FirstProjectedChapter = 2
	generation.LastProjectedChapter = 3
	generation.ExpectedChapterCount = 2
	generation.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	generation.ParentGenerationID = accepted.GenerationID
	generation.GenerationID, err = domain.DerivePlanningGenerationAttemptV2ID(generation.BaseCanonRoot, generation.StableOutlineRoot, generation.PlanningDependencyRoot, generation.RandomSeedContractRoot, "")
	if err != nil {
		t.Fatal(err)
	}
	registry.GenerationID = generation.GenerationID
	registry.FirstChapter = 2
	registry.LastChapter = 3
	registry.RegistryRoot, err = domain.ComputeObligationRegistryV2Root(registry)
	if err != nil {
		t.Fatal(err)
	}
	generation.ObligationRegistryRoot = registry.RegistryRoot
	generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
	if err != nil {
		t.Fatal(err)
	}
	foundation, err := pipelineProjectAllFoundationSnapshotRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	rag, err := pipelineProjectAllRAGSnapshotRoot(st.Dir())
	if err != nil {
		t.Fatal(err)
	}
	source := domain.PlanningSourceSnapshotV2{Version: domain.PlanningSourceSnapshotV2Version, GenerationID: generation.GenerationID, BaseCanonChapter: 1, BaseCanonRoot: generation.BaseCanonRoot, BaseStateRoot: generation.BaseStateRoot, StableOutlineRoot: generation.StableOutlineRoot, PlanningDependencyRoot: generation.PlanningDependencyRoot, RandomSeedContractRoot: generation.RandomSeedContractRoot, FoundationSnapshotRoot: foundation, RAGSnapshotRoot: rag, CapturedAt: generation.CreatedAt}
	source.SnapshotDigest, err = domain.ComputePlanningSourceSnapshotV2Digest(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ProjectedV2().CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	return generation, registry
}
