package domain

import "fmt"

func artifactKnownReactivationViewsV1(views []CharacterArtifactViewV1) []CharacterArtifactViewV1 {
	var result []CharacterArtifactViewV1
	for _, view := range views {
		if view.KnowledgeKind != "unread" {
			result = append(result, continuationCloneV1(view))
		}
	}
	return result
}

func FormatCharacterArtifactKnowledgeV1(knowledge CharacterArtifactKnowledgeV1) []string {
	verb := map[string]string{"authored": "本人实际写入的派生文档", "read": "本人实际读到的派生文档", "signed": "本人实际签认的派生文档"}[knowledge.Kind]
	if verb == "" {
		return nil
	}
	parts := []string{fmt.Sprintf("%s：%s；版本%s；状态%s；实际时刻T+%.12g分钟；当时位置%s；派生声明不自动证明世界真相或独立互证", verb, knowledge.ResourceID, knowledge.VersionDigest, knowledge.Status, knowledge.AtDay*1440, knowledge.Placement.Location)}
	for _, claim := range knowledge.Claims {
		parts = append(parts, artifactClaimStatementV1(claim))
	}
	for _, signature := range knowledge.Signatures {
		parts = append(parts, fmt.Sprintf("实际签认声明：%s于T+%.12g分钟签署版本%s，范围%s；不证明被转述事项为独立真相", signature.Signer, signature.AtDay*1440, signature.VersionDigest, signature.Scope))
	}
	return parts
}
