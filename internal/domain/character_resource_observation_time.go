package domain

import (
	"fmt"
	"math"
	"sort"
)

const CharacterResourceObservationTimePolicyV1 = "character-resource-observation-time.v1"

func HasCharacterResourceObservationTimePolicyV1(sources []string) bool {
	return physicalContainsRefV2(sources, CharacterResourceObservationTimePolicyV1)
}

func validateResourceMeasurementTaskV1(measurement ResourceMeasurementV2, proposal CharacterDecisionProposal, sources []string) error {
	if !HasCharacterResourceObservationTimePolicyV1(sources) {
		if measurement.TaskID != "" {
			return fmt.Errorf("measurement task_id requires %s", CharacterResourceObservationTimePolicyV1)
		}
		return nil
	}
	if !physicalIdentityV2(measurement.TaskID) {
		return fmt.Errorf("resource measurement requires the owner's original work task_id")
	}
	matched := 0
	for _, task := range proposal.SelfTasks {
		if task.TaskID == measurement.TaskID {
			if task.Kind != "work" || !physicalContainsRefV2(task.ResourceIDs, measurement.ResourceID) {
				return fmt.Errorf("resource measurement task_id must name the owner's work on that resource")
			}
			matched++
		}
	}
	if matched != 1 {
		return fmt.Errorf("resource measurement must bind exactly one original owner task")
	}
	return nil
}

func resourceObservationDayInWindowV1(day *float64, receipt WorldArbitrationReceipt) bool {
	clock := receipt.StoryTime
	return physicalAmountV2(day) && clock != nil &&
		physicalAmountV2(&clock.StartDay) && physicalAmountV2(&clock.EndDay) &&
		clock.EndDay >= clock.StartDay && *day >= clock.StartDay && *day <= clock.EndDay
}

func validateResourceSettlementTimeV1(settlement ResourceSettlementV2, receipt WorldArbitrationReceipt, enabled bool) error {
	if !enabled {
		if settlement.StartDay != nil || settlement.EndDay != nil {
			return fmt.Errorf("resource settlement times require %s", CharacterResourceObservationTimePolicyV1)
		}
		return nil
	}
	if settlement.Delta != nil && *settlement.Delta == 0 && settlement.StartDay == nil && settlement.EndDay == nil {
		return nil // No change needs no fabricated execution interval.
	}
	if !resourceObservationDayInWindowV1(settlement.StartDay, receipt) || !resourceObservationDayInWindowV1(settlement.EndDay, receipt) || *settlement.StartDay > *settlement.EndDay {
		return fmt.Errorf("resource %s settlement requires a non-reversed actual interval inside story_time", settlement.ResourceID)
	}
	return nil
}

// The existing one-settlement-per-resource contract remains in force. Splitting
// physical consumption into more segments also affects artifact and continuation
// contracts; this policy deliberately does not authorize that wider change.
func applyResourceObservationSettlementsV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, after *WorldPhysicalStateV2, resourceIndex map[string]int, requireRefs func([]string) error, proposals []CharacterDecisionProposal) error {
	enabled := HasCharacterResourceObservationTimePolicyV1(stimulus.Sources)
	for _, proposal := range proposals {
		for _, measurement := range proposal.ResourceMeasurements {
			if err := validateResourceMeasurementTaskV1(measurement, proposal, stimulus.Sources); err != nil {
				return err
			}
		}
	}
	if err := validateResourceObservationTimeTransitionV1(receipt, before, enabled); err != nil {
		return err
	}
	settled := map[string]bool{}
	for _, settlement := range receipt.ResourceSettlements {
		index, known := resourceIndex[settlement.ResourceID]
		if !known || settled[settlement.ResourceID] {
			return fmt.Errorf("resource %q is unknown or settled more than once", settlement.ResourceID)
		}
		settled[settlement.ResourceID] = true
		if err := requireRefs(settlement.EvidenceRefs); err != nil {
			return fmt.Errorf("resource_settlements[%s].evidence_refs: %w", settlement.ResourceID, err)
		}
		if settlement.Delta == nil || math.IsNaN(*settlement.Delta) || math.IsInf(*settlement.Delta, 0) || after.Resources[index].Unit == "" {
			return fmt.Errorf("resource settlement requires a finite delta and a quantitative unit")
		}
		if err := validateResourceSettlementTimeV1(settlement, receipt, enabled); err != nil {
			return err
		}
		actual := before.Resources[index].ActualAmount
		if actual == nil {
			if settlement.Before != nil || settlement.After != nil {
				return fmt.Errorf("unknown resource %q cannot acquire an invented before/after amount", settlement.ResourceID)
			}
			continue
		}
		if !physicalAmountV2(settlement.Before) || !physicalAmountV2(settlement.After) || !physicalAmountsCloseV2(*settlement.Before, *actual) || !physicalAmountsCloseV2(*actual+*settlement.Delta, *settlement.After) {
			return fmt.Errorf("resource %q settlement does not conserve its host before + delta = after", settlement.ResourceID)
		}
		after.Resources[index].ActualAmount = physicalNumberCopyV2(settlement.After)
	}
	return nil
}

func validateResourceObservationTimeTransitionV1(receipt WorldArbitrationReceipt, before WorldPhysicalStateV2, enabled bool) error {
	previous := map[string]map[string]ResourcePerceptionV2{}
	for _, actor := range before.Actors {
		previous[actor.AgentID] = map[string]ResourcePerceptionV2{}
		for _, holding := range actor.Resources {
			if !enabled && holding.Perception.ObservedAtDay != nil {
				return fmt.Errorf("observed_at_day requires %s", CharacterResourceObservationTimePolicyV1)
			}
			if enabled && holding.Perception.ObservedAtDay != nil && (receipt.StoryTime == nil || !physicalAmountV2(&receipt.StoryTime.StartDay) || !physicalAmountV2(holding.Perception.ObservedAtDay) || *holding.Perception.ObservedAtDay > receipt.StoryTime.StartDay) {
				return fmt.Errorf("resource %s prior observed_at_day is later than the actual cycle start", holding.ResourceID)
			}
			previous[actor.AgentID][holding.ResourceID] = holding.Perception
		}
	}
	for _, resolution := range receipt.Resolutions {
		if resolution.PostState == nil {
			continue
		}
		for _, holding := range resolution.PostState.Resources {
			p := holding.Perception
			old := previous[resolution.AgentID][holding.ResourceID]
			if !enabled {
				if p.ObservedAtDay != nil {
					return fmt.Errorf("observed_at_day requires %s", CharacterResourceObservationTimePolicyV1)
				}
				continue
			}
			if sameCharacterResourcePerceptionV2(resolution.AgentID, old, p) {
				continue // Static old observations need no invented timestamp.
			}
			if p.Kind == "reported" {
				// ResourceReportV2 carries no sender measurement time. A new
				// report must not attach this owner's old measurement timestamp
				// (or the report's arrival time) to a different source/value.
				if p.ObservedAtDay != nil {
					return fmt.Errorf("resource %s new reported perception must omit observed_at_day", holding.ResourceID)
				}
				continue
			}
			if p.Kind == "last_observed" {
				continue // The numeric authorization below checks its actual task.
			}
			// An estimate/downgrade retains the original measurement age; it
			// does not turn a new estimate into a new physical observation.
			if !samePhysicalNumberV2(old.ObservedAtDay, p.ObservedAtDay) {
				return fmt.Errorf("resource %s downgrade/estimate must preserve the original observed_at_day", holding.ResourceID)
			}
		}
	}
	return nil
}

func authorizeResourceObservationAtTimeV1(receipt WorldArbitrationReceipt, stimulus WorldStimulusPacket, before WorldPhysicalStateV2, resolution CharacterDecisionResolution, previous CharacterResourceHoldingV2, existed bool, holding CharacterResourceHoldingV2, proposal CharacterDecisionProposal) bool {
	p := holding.Perception
	if !existed || holding.PerceivedUnit != previous.PerceivedUnit || !resourceObservationDayInWindowV1(p.ObservedAtDay, receipt) {
		return false
	}
	for _, measurement := range proposal.ResourceMeasurements {
		if measurement.ResourceID != holding.ResourceID || !physicalContainsRefV2(proposal.MechanismRefs, measurement.MechanismRef) || !physicalContainsRefV2(resolution.MechanismRefs, measurement.MechanismRef) || validateResourceMeasurementTaskV1(measurement, proposal, stimulus.Sources) != nil {
			continue
		}
		matches := 0
		for _, execution := range resolution.SelfExecutions {
			if execution.TaskID == measurement.TaskID {
				if !selfExecutionActiveV2(execution.Status) || !resourceObservationDayInWindowV1(execution.StartDay, receipt) || !resourceObservationDayInWindowV1(execution.EndDay, receipt) || *execution.StartDay >= *execution.EndDay || *p.ObservedAtDay != *execution.EndDay {
					return false
				}
				matches++
			}
		}
		if matches != 1 {
			return false
		}
		amount, unit, exact := resourceAmountAtObservationTimeV1(receipt, before, holding.ResourceID, *p.ObservedAtDay)
		return exact && previous.PerceivedUnit == unit && samePhysicalNumberV2(p.Amount, amount)
	}
	return false
}

func resourceAmountAtObservationTimeV1(receipt WorldArbitrationReceipt, before WorldPhysicalStateV2, id string, at float64) (*float64, string, bool) {
	var resource WorldResourceBalanceV2
	found := false
	for _, candidate := range before.Resources {
		if candidate.ResourceID == id {
			resource, found = candidate, true
			break
		}
	}
	if !found || resource.ActualAmount == nil {
		return nil, "", false
	}
	for _, settlement := range receipt.ResourceSettlements {
		if settlement.ResourceID != id || settlement.Delta == nil || *settlement.Delta == 0 {
			continue
		}
		if validateResourceSettlementTimeV1(settlement, receipt, true) != nil {
			return nil, "", false
		}
		if at < *settlement.StartDay || (at == *settlement.StartDay && *settlement.StartDay < *settlement.EndDay) {
			return resource.ActualAmount, resource.Unit, true
		}
		if at >= *settlement.EndDay {
			return settlement.After, resource.Unit, settlement.After != nil
		}
		return nil, "", false // No interpolation inside an unresolved change interval.
	}
	return resource.ActualAmount, resource.Unit, true
}

func canonicalResourceObservationSettlementsV1(settlements []ResourceSettlementV2) []ResourceSettlementV2 {
	if settlements == nil {
		return nil
	}
	out := append([]ResourceSettlementV2(nil), settlements...)
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out
}
