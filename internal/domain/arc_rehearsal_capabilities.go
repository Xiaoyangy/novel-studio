package domain

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"
)

const ArcRehearsalCapabilityPolicyV1 = "arc-rehearsal-execution-capabilities.v1"

// Host-built API inventory, not evidence that an operation has happened. The
// existing WorldState is the sole resource/actor inventory. No background actor
// is inferred from a mechanism's prose actor_scope.
type ArcRehearsalExecutionCapabilitiesV1 struct {
	Policy            string   `json:"policy"`
	CharacterProtocol string   `json:"character_protocol"`
	ActivationPolicy  string   `json:"activation_policy"`
	ProducerDigest    string   `json:"producer_digest"`
	ActionKinds       []string `json:"action_kinds"`
	ResourceKinds     []string `json:"resource_kinds"`
	RecipientKinds    []string `json:"recipient_kinds"`
}

// Key is report-local, never a resource ID. ArtifactRef refers to an earlier
// artifact_write key (an expected version); it does not create a world object.
type ArcRehearsalCapabilityRequirementV1 struct {
	Key            string                         `json:"key"`
	Kind           string                         `json:"kind"`
	ActorRef       string                         `json:"actor_ref"`
	RecipientRef   string                         `json:"recipient_ref,omitempty"`
	ResourceRefs   []string                       `json:"resource_refs,omitempty"`
	MechanismRefs  []string                       `json:"mechanism_refs,omitempty"`
	ArtifactRef    string                         `json:"artifact_ref,omitempty"`
	DependsOn      []string                       `json:"depends_on,omitempty"`
	MaterialInputs []CharacterWorkMaterialInputV1 `json:"material_inputs,omitempty"`
}

func BuildArcRehearsalExecutionCapabilitiesV1(protocol, policy, producer string) (ArcRehearsalExecutionCapabilitiesV1, error) {
	p := ArcRehearsalExecutionCapabilitiesV1{Policy: ArcRehearsalCapabilityPolicyV1, CharacterProtocol: protocol, ActivationPolicy: policy, ProducerDigest: producer, RecipientKinds: []string{"existing_character_actor"}}
	if protocol != CharacterAgentDecisionProtocolV2Version || !characterSourceDigestPatternV2.MatchString(producer) {
		return p, fmt.Errorf("rehearsal capability profile requires an exact supported physical character producer")
	}
	p.ActionKinds = []string{"communication", "resource_delivery", "resource_measurement", "resource_read", "resource_use", "self_work"}
	p.ResourceKinds = []string{"existing_numeric_resource", "existing_qualitative_resource", "existing_readable_resource"}
	switch policy {
	case "": // Physical one-shot v2: no operational-result or artifact protocol.
	case CharacterActivationCyclePolicy, CharacterActivationCyclePolicyV2:
		p.ActionKinds = append(p.ActionKinds, "operational_observation")
	case CharacterActivationCyclePolicyV3:
		p.ActionKinds = append(p.ActionKinds, "operational_observation", "artifact_write", "artifact_read", "artifact_sign")
		p.ResourceKinds = append(p.ResourceKinds, "existing_work_artifact", "expected_work_artifact")
	default:
		// In particular V4 is not aliased to V3. A future adapter must supply
		// and verify its own actual background execution/recipient inventory.
		return p, fmt.Errorf("unsupported rehearsal execution capability policy %q", policy)
	}
	slices.Sort(p.ActionKinds)
	slices.Sort(p.ResourceKinds)
	return p, nil
}

func validateArcRehearsalExecutionCapabilitiesV1(p *ArcRehearsalExecutionCapabilitiesV1) error {
	if p == nil { // Historical bytes retain the historical validation path.
		return nil
	}
	want, err := BuildArcRehearsalExecutionCapabilitiesV1(p.CharacterProtocol, p.ActivationPolicy, p.ProducerDigest)
	if err != nil || !samePhysicalValueV2(want, *p) {
		return fmt.Errorf("rehearsal execution capability inventory is not the exact host profile")
	}
	return nil
}

func rehearsalCapabilityKeyV1(s string) bool {
	return physicalIdentityV2(s) && len(s) <= 128 && !strings.HasPrefix(s, "res_") && !strings.HasPrefix(s, "sha256:")
}

func validateArcRehearsalCapabilitiesV1(input ArcRehearsalInput, body ArcRehearsalBody) (resultErr error) {
	materialIndex, requirementIndex := -1, -1
	var diagnosticRequirement ArcRehearsalCapabilityRequirementV1
	defer func() {
		if resultErr == nil || materialIndex < 0 {
			return
		}
		path := fmt.Sprintf("material_checks[%d]", materialIndex)
		if requirementIndex >= 0 {
			path += fmt.Sprintf(".capability_requirements[%d] key=%s kind=%s", requirementIndex,
				rehearsalCapabilityDiagnosticValueV1(diagnosticRequirement.Key), rehearsalCapabilityDiagnosticValueV1(diagnosticRequirement.Kind))
			if diagnosticRequirement.ArtifactRef != "" {
				path += " artifact_ref=" + rehearsalCapabilityDiagnosticValueV1(diagnosticRequirement.ArtifactRef)
			}
		}
		// Context only: preserve the original rejection and its unwrap chain.
		// Put location first so bounded tool logs retain the actionable pointer.
		resultErr = fmt.Errorf("%s: %w", path, resultErr)
	}()
	if input.ExecutionCapabilities == nil {
		for i, m := range body.MaterialChecks {
			if len(m.CapabilityRequirements) != 0 {
				materialIndex, requirementIndex, diagnosticRequirement = i, 0, m.CapabilityRequirements[0]
				return fmt.Errorf("execution dependencies require the capability-bound rehearsal policy")
			}
		}
		return nil
	}
	if err := validateArcRehearsalExecutionCapabilitiesV1(input.ExecutionCapabilities); err != nil {
		return err
	}
	if input.WorldState == nil || len(body.MaterialChecks) > 64 {
		return fmt.Errorf("rehearsal capability validation requires bounded checks and world state")
	}
	resources := map[string]WorldResourceBalanceV2{}
	actors := map[string]bool{}
	executors := map[string]bool{}
	for _, r := range input.WorldState.Resources {
		resources[r.ResourceID] = r
	}
	for _, a := range input.WorldState.Actors {
		actors[a.AgentID] = true
	}
	for _, o := range input.CharacterObservations {
		if !actors[o.AgentID] {
			return fmt.Errorf("capability executor lacks an existing world actor")
		}
		executors[o.AgentID] = true
	}
	mechanisms := map[string]bool{}
	if input.WorldCodex != nil {
		for _, m := range input.WorldCodex.Mechanisms {
			mechanisms[m.ID] = CodexMechanismVisibility(m) != "secret" && m.CharacterView != nil
		}
	}
	type priorRequirement struct {
		value     ArcRehearsalCapabilityRequirementV1
		available bool
		creator   string
	}
	prior := map[string]priorRequirement{}
	operations := map[string]bool{}
	allocated := map[string]float64{}
	for i, m := range body.MaterialChecks {
		materialIndex, requirementIndex = i, -1
		if operations[m.Operation] || len(m.CapabilityRequirements) > 16 {
			return fmt.Errorf("material operations must be unique with at most 16 capability dependencies")
		}
		operations[m.Operation] = true
		if m.Status == "available" && len(m.CapabilityRequirements) == 0 {
			return fmt.Errorf("available operation %q needs explicit executable capability dependencies; mechanism prose is not an observation/recipient API", m.Operation)
		}
		if m.Status == "not_required" && (len(m.CapabilityRequirements) != 0 || len(m.ResourceRefs) != 0) {
			return fmt.Errorf("not_required cannot hide declared capability dependencies")
		}
		covered := map[string]bool{}
		readDependency := false
		for j, r := range m.CapabilityRequirements {
			requirementIndex, diagnosticRequirement = j, r
			if !rehearsalCapabilityKeyV1(r.Key) || prior[r.Key].value.Key != "" || len(prior) >= 128 || len(r.DependsOn) > 16 || len(r.ResourceRefs) > 16 || len(r.MechanismRefs) > 16 || len(r.MaterialInputs) > 16 {
				return fmt.Errorf("capability dependency has duplicate, invalid or unbounded identity")
			}
			if r.Kind != "unsupported" && !slices.Contains(input.ExecutionCapabilities.ActionKinds, r.Kind) {
				return fmt.Errorf("capability %q is not supported by the frozen execution profile", r.Kind)
			}
			for _, list := range [][]string{r.ResourceRefs, r.MechanismRefs, r.DependsOn} {
				if len(normalizeV2Strings(list)) != len(list) {
					return fmt.Errorf("capability dependency contains duplicate/empty references")
				}
			}
			for _, dep := range r.DependsOn {
				p, ok := prior[dep]
				if !ok || m.Status == "available" && !p.available {
					return fmt.Errorf("capability %q depends on a missing, later or unavailable step %q", r.Key, dep)
				}
			}
			for _, id := range r.ResourceRefs {
				if _, ok := resources[id]; !ok {
					return fmt.Errorf("capability cannot invent resource %q; declare the missing target without an ID", id)
				}
				covered[id] = true
			}
			for _, id := range r.MechanismRefs {
				if !mechanisms[id] {
					return fmt.Errorf("capability references a non-public or undefined mechanism %q", id)
				}
			}
			creator := ""
			if r.ArtifactRef != "" {
				p, ok := prior[r.ArtifactRef]
				if !ok || p.value.Kind != "artifact_write" || !slices.Contains(r.DependsOn, r.ArtifactRef) || len(r.ResourceRefs) != 0 {
					return fmt.Errorf("future artifact must reference an earlier declared write via depends_on, never a current resource ID")
				}
				creator = p.creator
			}
			readDependency = readDependency || r.Kind == "resource_read" || r.Kind == "artifact_read"
			// Missing/unclear is an honest report of unsupported actor/target.
			// It cannot become available transitively or ReadyForDetail.
			if m.Status != "available" {
				prior[r.Key] = priorRequirement{value: r, creator: creator}
				continue
			}
			if r.Kind == "unsupported" || !executors[r.ActorRef] || r.RecipientRef != "" && !actors[r.RecipientRef] {
				return fmt.Errorf("available capability %q requires existing character actors; background duties/recipients have no enabled execution adapter", r.Key)
			}
			if (r.Kind == "communication" || r.Kind == "resource_delivery") != (r.RecipientRef != "") || r.RecipientRef == r.ActorRef {
				return fmt.Errorf("communication/delivery requires an explicit distinct existing recipient, other actions cannot claim one")
			}
			if r.Kind != "artifact_write" && len(r.MaterialInputs) != 0 {
				return fmt.Errorf("only future artifact creation allocates materials")
			}
			if r.Kind != "artifact_write" && r.Kind != "artifact_read" && r.Kind != "artifact_sign" && r.Kind != "resource_delivery" && r.ArtifactRef != "" {
				return fmt.Errorf("this action cannot consume a future artifact version")
			}
			single := len(r.ResourceRefs) == 1
			var resource WorldResourceBalanceV2
			if single {
				resource = resources[r.ResourceRefs[0]]
			}
			switch r.Kind {
			case "resource_read":
				if !single || resource.Artifact != nil || len(resource.ReadableFacts) == 0 || !m.RequiresReadable {
					return fmt.Errorf("resource_read requires one existing readable document and requires_readable=true")
				}
			case "resource_measurement":
				if !single || resource.Unit == "" || resource.ActualAmount == nil || len(r.MechanismRefs) == 0 {
					return fmt.Errorf("resource_measurement requires an existing numeric target and public mechanism; a ruler name or mechanism alone is not a target")
				}
			case "operational_observation":
				if !single || !operationalResourceV1(resource) || len(r.MechanismRefs) == 0 {
					return fmt.Errorf("operational_observation requires one existing qualitative non-document resource and public mechanism; it cannot certify quantities or overall safety")
				}
			case "resource_use":
				if len(r.ResourceRefs) == 0 {
					return fmt.Errorf("resource_use requires existing physical inputs")
				}
			case "self_work", "communication":
				// Only an own task's execution or a sourced report/request, not
				// permission to author newly observed external facts.
			case "resource_delivery":
				if !single && r.ArtifactRef == "" {
					return fmt.Errorf("delivery requires one actual resource or earlier expected artifact")
				}
			case "artifact_write":
				if !single && r.ArtifactRef == "" { // New expected artifact.
					if len(r.ResourceRefs) != 0 || len(r.MaterialInputs) == 0 {
						return fmt.Errorf("new expected artifact requires existing material allocation, not an invented resource ID")
					}
					creator = r.ActorRef
					seenMaterials := map[string]bool{}
					for _, material := range r.MaterialInputs {
						v, ok := resources[material.ResourceID]
						if !ok || v.Unit == "" || v.ActualAmount == nil || seenMaterials[material.ResourceID] || material.Amount <= 0 || math.IsNaN(material.Amount) || math.IsInf(material.Amount, 0) {
							return fmt.Errorf("expected artifact requires distinct existing finite positive material inputs")
						}
						seenMaterials[material.ResourceID] = true
						allocated[material.ResourceID] += material.Amount
						if allocated[material.ResourceID] > *v.ActualAmount {
							return fmt.Errorf("expected artifacts overallocate existing materials")
						}
						covered[material.ResourceID] = true
					}
				} else {
					if single && resource.Artifact != nil {
						creator = resource.Artifact.CreatorAgentID
					}
					if creator != r.ActorRef || len(r.MaterialInputs) != 0 {
						return fmt.Errorf("artifact revision requires its actual/expected author and no repeated allocation")
					}
				}
			case "artifact_read", "artifact_sign":
				if single && resource.Artifact != nil {
					creator = resource.Artifact.CreatorAgentID
				}
				if creator == "" || r.Kind == "artifact_read" && !m.RequiresReadable {
					return fmt.Errorf("artifact read/sign requires an actual artifact or earlier expected write, not an ordinary document")
				}
			}
			if r.ArtifactRef != "" {
				access := creator == r.ActorRef
				read := creator == r.ActorRef
				for _, dep := range r.DependsOn {
					p := prior[dep].value
					access = access || p.Kind == "resource_delivery" && p.ArtifactRef == r.ArtifactRef && p.RecipientRef == r.ActorRef
					read = read || p.Kind == "artifact_read" && p.ArtifactRef == r.ArtifactRef && p.ActorRef == r.ActorRef
				}
				if !access || r.Kind == "artifact_sign" && !read {
					return fmt.Errorf("future artifact use requires preceding recipient access, and signing requires own reading of that expected version")
				}
			}
			prior[r.Key] = priorRequirement{value: r, available: true, creator: creator}
		}
		requirementIndex = -1 // Remaining checks concern the material as a whole.
		if m.Status == "available" {
			if m.RequiresReadable && !readDependency {
				return fmt.Errorf("readable dependency lacks a document/artifact read capability")
			}
			for _, id := range m.ResourceRefs {
				if !covered[id] {
					return fmt.Errorf("material resource %q lacks an executable dependency", id)
				}
			}
		}
	}
	return nil
}

func rehearsalCapabilityDiagnosticValueV1(value string) string {
	quoted, _ := json.Marshal(value)
	if len(quoted) <= 160 {
		return string(quoted)
	}
	// Malformed oversized keys must not turn feedback into a payload echo.
	// A hash/length is explicitly not a truncated key to copy into a revision.
	return fmt.Sprintf("<omitted utf8_bytes=%d sha256=%x>", len(value), sha256.Sum256([]byte(value)))
}
