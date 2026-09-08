package store

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/chenhongyang/novel-studio/internal/domain"
)

func TestProjectedStoreExplicitProtocolRejectsLegacyBeforeAnyIntentWrite(t *testing.T) {
	for _, protocol := range []string{domain.CharacterAgentDecisionProtocolVersion, domain.CharacterAgentDecisionProtocolV2Version} {
		t.Run(protocol, func(t *testing.T) {
			root := t.TempDir()
			projected := NewStore(root).ProjectedV2()
			generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, true)
			generation.CharacterAgentProtocol = protocol
			var err error
			generation.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(generation)
			if err != nil {
				t.Fatal(err)
			}
			if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
				t.Fatal(err)
			}
			cursor, err := projected.InitializeProjectionCursor(generation.GenerationID)
			if err != nil {
				t.Fatal(err)
			}
			before := generationProtocolFilesForTest(t, root)
			if err := projected.SaveProjectedChapterBundle(bundles[0]); err == nil || !strings.Contains(err.Error(), "character protocol") {
				t.Fatalf("legacy save was not rejected at protocol boundary: %v", err)
			}
			if _, err := projected.ProjectChapterAndAdvance(generation.GenerationDigest, generation.ChainTailRoot, registry.RegistryRoot, *cursor, bundles[0], registry); err == nil || !strings.Contains(err.Error(), "character protocol") {
				t.Fatalf("legacy transactional write was not rejected at protocol boundary: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, projectedChapterIntentsDir, generation.GenerationID)); !os.IsNotExist(err) {
				t.Fatalf("rejected protocol persisted a pending intent: %v", err)
			}
			if after := generationProtocolFilesForTest(t, root); !reflect.DeepEqual(before, after) {
				t.Fatal("protocol rejection mutated durable files")
			}
		})
	}
}

func TestProjectedStoreProtocolBindingAlsoProtectsReadAndSeal(t *testing.T) {
	projected := NewStore(t.TempDir()).ProjectedV2()
	generation, source, registry, bundles := projectedStoreV2Fixture(t, 1, true)
	if err := projected.CreateBuildingGeneration(generation, source, registry); err != nil {
		t.Fatal(err)
	}
	if err := projected.SaveProjectedChapterBundle(bundles[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.LoadProjectedChapterBundles(generation.GenerationID); err != nil {
		t.Fatalf("historical metadata-free read changed: %v", err)
	}
	loaded, err := projected.LoadBuildingGeneration(generation.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	loaded.CharacterAgentProtocol = domain.CharacterAgentDecisionProtocolV2Version
	loaded.GenerationDigest, err = domain.ComputePlanningGenerationV2Digest(*loaded)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an inconsistent, but internally rehashed, persisted manifest.
	if err := projected.io.WriteJSON(filepath.Join(projectedBuildingGenerationPath(generation.GenerationID), projectedGenerationManifestFile), *loaded); err != nil {
		t.Fatal(err)
	}
	if _, err := projected.LoadProjectedChapterBundles(generation.GenerationID); err == nil || !strings.Contains(err.Error(), "character protocol") {
		t.Fatalf("read accepted protocol mixture: %v", err)
	}
	if _, err := projected.SealGeneration(generation.GenerationID); err == nil || !strings.Contains(err.Error(), "character protocol") {
		t.Fatalf("seal accepted protocol mixture: %v", err)
	}
}

func generationProtocolFilesForTest(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err == nil {
			result[path] = string(raw)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
