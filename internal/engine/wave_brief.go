package engine

import (
	"fmt"
	"strings"
)

// WaveStoryInfo describes a story running in the same dispatch wave.
// Used to build a brief that warns agents not to touch each other's files.
type WaveStoryInfo struct {
	ID         string
	Title      string
	OwnedFiles []string
}

// BuildWaveBrief renders a markdown section listing the other stories
// running in the same wave, including their owned files. Agents use this
// to avoid conflicts. Returns an empty string when there are no parallel
// stories (i.e. the wave has only one assignment).
func BuildWaveBrief(currentStoryID string, waveStories []WaveStoryInfo) string {
	if len(waveStories) <= 1 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Parallel Stories in This Wave\n\n")
	sb.WriteString("You are working in parallel with these other agents. Do NOT modify their files.\n\n")

	for _, s := range waveStories {
		if s.ID == currentStoryID {
			continue
		}
		files := "no specific files"
		if len(s.OwnedFiles) > 0 {
			files = strings.Join(s.OwnedFiles, ", ")
		}
		sb.WriteString(fmt.Sprintf("- %s \"%s\" — owns: %s\n", s.ID, s.Title, files))
	}

	return sb.String()
}
