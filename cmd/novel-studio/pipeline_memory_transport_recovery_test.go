package main

import (
	"testing"

	"github.com/chenhongyang/novel-studio/internal/agents"
	"github.com/chenhongyang/novel-studio/internal/domain"
	"github.com/chenhongyang/novel-studio/internal/store"
)

func TestMemoryTextTransportCLIRecoversFrozenInitialIntentWithoutUpgrade(t *testing.T) {
	const historical = "sha256:bdd8b0797a5c4f7648e3a825814cf079141f2be555f80e240b011f316b2cc8c3"
	if agents.CharacterActivationProtocolWithProducer(domain.CharacterActivationCyclePolicyV3, historical) != historical {
		t.Fatal("BDD8 implementation is not executable")
	}
	opts, st, old := continuationProducerCLIFixture(t, historical)
	cfg, bundle, err := loadCfgBundle(opts)
	publicationArtifactMust(t, err)
	progress, err := st.Progress.Load()
	publicationArtifactMust(t, err)
	before, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	resumed, err := buildPipelineProjectAllIdentity(cfg, bundle, store.NewStore(st.Dir()), progress)
	publicationArtifactMust(t, err)
	if resumed.Generation.GenerationID != old.Generation.GenerationID || resumed.FrozenActivationProducer != historical {
		t.Fatal("transport silently upgraded existing initial-intent generation")
	}
	after, err := store.DirectoryContentRoot(st.Dir())
	publicationArtifactMust(t, err)
	if after != before {
		t.Fatal("read-only producer recovery wrote old evidence")
	}
	fresh, err := buildPipelineProjectAllIdentityForPreflight(cfg, bundle, st, progress, true)
	publicationArtifactMust(t, err)
	if fresh.Generation.GenerationID == old.Generation.GenerationID || fresh.FrozenActivationProducer == historical {
		t.Fatal("explicit fresh generation did not select the new transport")
	}
}
