package agents

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/modelinput"
	"github.com/chenhongyang/novel-studio/internal/store"
	"github.com/chenhongyang/novel-studio/internal/testutil"
	"github.com/chenhongyang/novel-studio/internal/tools"
)

func TestCommunicationAddressingActualToolsAndNextPrivateView(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(map[bool]string{false: "sleeping", true: "active"}[active], func(t *testing.T) {
			f := testutil.CharacterPassiveReception(t, false, true)
			st := store.NewStore(t.TempDir())
			selectionMust(t, st.Init())
			selectionMust(t, st.Progress.Init("通信回归", 1))
			f.Inputs.Stimulus.Sources = append(f.Inputs.Stimulus.Sources, domain.CharacterCommunicationAddressingPolicyV1)
			var err error
			f.Inputs.Stimulus, err = domain.FinalizeWorldStimulusPacket(f.Inputs.Stimulus)
			selectionMust(t, err)
			session, err := domain.NewCharacterActivationSession(f.Inputs.Stimulus.GenerationID, 1, "sha256:"+strings.Repeat("a", 64), *f.Inputs.Stimulus.PhysicalState, 0, 4)
			selectionMust(t, err)
			proofs, err := st.CharacterAgents.ForActivationCycle(session)
			selectionMust(t, err)
			selectionMust(t, proofs.SaveRegistrySnapshot(f.Inputs.Stimulus.GenerationID, 1, f.Inputs.Registry))
			selectionMust(t, proofs.SaveStimulus(f.Inputs.Stimulus))
			var proposals []domain.CharacterDecisionProposal
			for i := range f.Inputs.Observations {
				o := &f.Inputs.Observations[i]
				o.StimulusDigest = f.Inputs.Stimulus.Digest
				o.Sources = append(o.Sources, domain.CharacterCommunicationAddressingPolicyV1)
				o.KnownFacts = append(o.KnownFacts, domain.CharacterAgentFact{ID: "perceived-person", Kind: "known", Text: "眼前有一位正在听我说话的人", Visibility: "private"})
				*o, err = domain.FinalizeCharacterObservationPacket(*o)
				selectionMust(t, err)
				selectionMust(t, proofs.SaveObservation(*o))
				if o.AgentID != f.Proposal.AgentID && !active {
					continue
				}
				p := f.Proposal
				p.AgentID, p.Character, p.ObservationDigest, p.Location = o.AgentID, o.Character, o.Digest, o.Location
				p.CurrentGoal, p.Pressure = o.CurrentGoal, o.Pressure
				if o.AgentID == f.Proposal.AgentID {
					p.Communications[0].ToCharacter = ""
					p.Communications[0].RecipientHint = "眼前正在听我说话的人"
					p.Communications[0].Text = "请问这里是什么地方？"
					p.Communications[0].KnowledgeRefs = []string{"perceived-person"}
				} else {
					p.Communications = nil
					p.Decision, p.DecisionReason, p.IntendedAction = "等候", "听取实际消息", "在现场等待"
					p.KnowledgeRefs = []string{"perceived-person"}
				}
				raw, _ := json.Marshal(p)
				var args map[string]any
				selectionMust(t, json.Unmarshal(raw, &args))
				for _, key := range []string{"version", "generation_id", "chapter", "round", "agent_id", "character", "observation_digest", "submitted_at", "digest"} {
					delete(args, key)
				}
				raw, _ = json.Marshal(args)
				codec, e := modelinput.NewScopedArtifactReferenceCodecV1(modelinput.KindCharacterObservation, *o)
				selectionMust(t, e)
				codec, e = modelinput.WithScopedCommunicationReferencesV1(codec)
				selectionMust(t, e)
				underlying, e := tools.NewSubmitCharacterActivationDecisionTool(st, session, *o)
				selectionMust(t, e)
				tool, e := newScopedReferenceTool(underlying, codec)
				selectionMust(t, e)
				_, e = tool.Execute(t.Context(), raw)
				selectionMust(t, e)
				saved, e := proofs.LoadProposal(o.GenerationID, o.Chapter, o.Round, o.AgentID)
				selectionMust(t, e)
				proposals = append(proposals, *saved)
				for j := range f.Inputs.Activation.Entries {
					entry := &f.Inputs.Activation.Entries[j]
					if entry.AgentID == o.AgentID {
						entry.State, entry.Reasons, entry.ObservationDigest = "active", []string{"scene_appearance"}, o.Digest
					}
				}
			}
			f.Inputs.Activation, err = domain.FinalizeCharacterAgentActivation(f.Inputs.Activation)
			selectionMust(t, err)
			selectionMust(t, proofs.SaveRegistrySnapshot(f.Inputs.Stimulus.GenerationID, 1, f.Inputs.Registry))
			selectionMust(t, proofs.SaveStimulus(f.Inputs.Stimulus))
			selectionMust(t, proofs.SaveActivation(f.Inputs.Activation))
			sender := proposals[0]
			for _, p := range proposals {
				if p.AgentID == f.Proposal.AgentID {
					sender = p
				}
			}
			reception := f.Receipt.PassiveReceptions[0]
			f.Receipt.PassiveReceptions = nil
			f.Receipt.CommunicationReceptions = []domain.CharacterCommunicationReceptionV1{{ToAgentID: reception.ToAgentID, FromAgentID: sender.AgentID, SourceProposalDigest: sender.Digest, CommunicationID: sender.Communications[0].ID, DeliveredAtDay: reception.DeliveredAtDay, Channel: "in_person", RecipientResolution: "unique", EvidenceRefs: []string{"perceived-person"}}}
			f.Receipt.Resolutions[0].ProposalDigest = sender.Digest
			f.Receipt.ResourceSettlements[0].EvidenceRefs = []string{sender.Digest}
			for _, p := range proposals {
				if p.AgentID != sender.AgentID {
					r := f.Receipt.Resolutions[0]
					r.AgentID, r.Character, r.ProposalDigest = p.AgentID, p.Character, p.Digest
					r.Decision, r.IntendedAction, r.ActionOrder = p.Decision, p.IntendedAction, 2
					for _, actor := range f.Inputs.Stimulus.PhysicalState.Actors {
						if actor.AgentID == p.AgentID {
							post := actor
							r.PostState = &post
						}
					}
					f.Receipt.Resolutions = append(f.Receipt.Resolutions, r)
				}
			}
			raw, _ := json.Marshal(f.Receipt)
			var args map[string]any
			selectionMust(t, json.Unmarshal(raw, &args))
			for _, key := range []string{"version", "generation_id", "chapter", "round", "stimulus_digest", "activation_digest", "proposal_digests", "generated_at", "digest"} {
				delete(args, key)
			}
			args["time_window"] = "本轮"
			raw, _ = json.Marshal(args)
			arbiter, err := tools.NewResolveCharacterActivationTool(st, session, f.Inputs.Stimulus, f.Inputs.Activation, proposals, "sha256:"+strings.Repeat("a", 64), nil, 1)
			selectionMust(t, err)
			_, err = arbiter.Execute(t.Context(), raw)
			selectionMust(t, err)
			r, err := proofs.LoadArbitration(sender.GenerationID, 1, 1)
			selectionMust(t, err)
			state, err := domain.ApplyArbitrationPhysicalStateV2(*r, f.Inputs.Stimulus, proposals...)
			selectionMust(t, err)
			memories, err := projectCharacterActivationMemoriesResolved(f.Inputs.Memories, domain.CharacterActivationCycle{GenerationID: sender.GenerationID, Chapter: 1}, *r, proposals, state)
			selectionMust(t, err)
			for _, memory := range memories {
				selectionMust(t, st.CharacterAgents.SaveProjectedMemory(memory))
			}
			next := f.Inputs.Stimulus
			next.PhysicalState = &state
			next, err = domain.FinalizeWorldStimulusPacket(next)
			selectionMust(t, err)
			for _, record := range f.Inputs.Registry.Entries {
				profile := characterAgentProfile{Record: record, Character: domain.Character{Name: record.Character, Tier: record.Tier, InitialState: &domain.CharacterInitialState{Location: "船上", CurrentGoal: "等候", Pressure: "等待消息", KnownFacts: []string{"本人在现场"}}}}
				o, e := buildCharacterObservation(st, sender.GenerationID, 1, profile, next, domain.ProjectedPlanningContextV2{}, r.GeneratedAt)
				selectionMust(t, e)
				codec, e := modelinput.NewScopedArtifactReferenceCodecV1(modelinput.KindCharacterObservation, o)
				selectionMust(t, e)
				codec, e = modelinput.WithScopedCommunicationReferencesV1(codec)
				selectionMust(t, e)
				wire, _, e := modelinput.EncodeCharacterMemoryModelViewV1(codec.ModelView())
				selectionMust(t, e)
				view, e := modelinput.DecodeCharacterMemoryModelViewV1(wire)
				selectionMust(t, e)
				peer := "甲"
				if record.Character == "甲" {
					peer = "乙"
				}
				if strings.Contains(string(view.Body), peer) {
					t.Fatalf("%s received unlearned peer canonical name through final model view: %s", record.Character, view.Body)
				}
				if !strings.Contains(string(view.Body), sender.Communications[0].Text) {
					t.Fatalf("%s lost exact sent/received text", record.Character)
				}
				if record.AgentID == reception.ToAgentID {
					found := false
					for _, fact := range o.KnownFacts {
						if fact.Kind == "received_information" && strings.Contains(fact.Text, sender.Communications[0].Text) {
							found = true
						}
					}
					if !found {
						t.Fatal("real next observation lacks the host-derived communication")
					}
				}
			}
		})
	}
}

func TestCommunicationAddressingProducerIsDistinctAndOldFrozenBytesRemain(t *testing.T) {
	if got := characterActivationProtocolV3MemoryTextDigest(); got != "sha256:fa859a7265b165e699b4ffec034a6b53a848d6b968d6702a5d3744dd8e44eee2" {
		t.Fatalf("historical memory producer changed: %s", got)
	}
	fresh := characterActivationProtocolForPolicy(domain.CharacterActivationCyclePolicyV3)
	for _, producer := range CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		policies := characterActivationV3PoliciesForProducer(producer)
		if domain.HasCharacterCommunicationAddressingPolicyV1(policies) != (producer == fresh) {
			t.Fatal("old producer acquired new communication semantics")
		}
		if characterActivationProtocolForStimulus(domain.WorldStimulusPacket{Sources: policies}) != producer {
			t.Fatal("frozen source inventory changed producer identity")
		}
	}
	t.Logf("frozen old location producer=%s; new=%s", characterActivationProtocolV3HostLocationDigest(), fresh)
}
