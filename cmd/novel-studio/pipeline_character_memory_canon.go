package main

import (
	"fmt"
	"regexp"
)

var pipelineCharacterMemoryCanonPath = regexp.MustCompile(`^meta/character_agents/(registry\.json|memory/[A-Za-z0-9][A-Za-z0-9._:-]*\.json)$`)
var pipelineCharacterMemoryCanonDigest = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func validatePipelineCharacterMemoryCanonOverride(rel, digest string) error {
	if !pipelineCharacterMemoryCanonPath.MatchString(rel) {
		return fmt.Errorf("character memory publication cannot override canonical path %q", rel)
	}
	if digest != "" && !pipelineCharacterMemoryCanonDigest.MatchString(digest) {
		return fmt.Errorf("character memory publication has invalid file digest for %q", rel)
	}
	return nil
}
