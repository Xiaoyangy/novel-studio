package main

import (
	"fmt"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestSelfCompletionViewCLIRecoversEveryProducerWithoutFirstInput(t *testing.T) {
	for i, producer := range agents.CharacterActivationProducerCandidates(domain.CharacterActivationCyclePolicyV3) {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			opts, st, old := continuationProducerCLIFixture(t, producer)
			cfg, bundle, err := loadCfgBundle(opts)
			publicationArtifactMust(t, err)
			progress, err := st.Progress.Load()
			publicationArtifactMust(t, err)
			before, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			identity, err := buildPipelineProjectAllIdentity(cfg, bundle, st, progress)
			publicationArtifactMust(t, err)
			if identity.Generation.GenerationID != old.Generation.GenerationID || identity.FrozenActivationProducer != producer {
				t.Fatal("existing attempt acquired the new default producer")
			}
			after, err := store.DirectoryContentRoot(st.Dir())
			publicationArtifactMust(t, err)
			if before != after {
				t.Fatal("read-only producer recovery changed old evidence")
			}
		})
	}
}
