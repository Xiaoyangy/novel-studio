package domain

import "strings"

func CharacterActivationPrivateOutcome(proposal CharacterDecisionProposal, resolution CharacterDecisionResolution, state WorldPhysicalStateV2, receipt WorldArbitrationReceipt) (string, error) {
	text, err := CharacterPrivateOutcomeV2(proposal, resolution, state, receipt)
	if err != nil {
		return "", err
	}
	var sent []string
	for _, reception := range receipt.PassiveReceptions {
		if reception.FromAgentID != proposal.AgentID || reception.SourceProposalDigest != proposal.Digest {
			continue
		}
		for _, message := range proposal.Communications {
			if message.ID == reception.CommunicationID {
				sent = append(sent, "向"+message.ToCharacter+"发出"+message.Kind+"："+message.Text)
			}
		}
	}
	if len(sent) > 0 {
		text += "；本人已发出的通信（不代表知道对方收到或同意）：" + strings.Join(sent, "；")
	}
	return text, nil
}
