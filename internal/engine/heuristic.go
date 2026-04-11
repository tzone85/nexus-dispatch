package engine

import (
	"fmt"
	"regexp"
	"strings"
)

var (
	conjunctionRe     = regexp.MustCompile(`\b(and|with|plus)\b|[,;]`)
	complexityWordRe  = regexp.MustCompile(`(?i)\b(auth|oauth|migration|real-time|realtime|integration|security|payment|encryption|websocket|streaming|database|cache|queue)\b`)
	simplicityWordRe  = regexp.MustCompile(`(?i)\b(simple|basic|add field|rename|update text|typo|fix|tweak)\b`)
	pluralIndicatorRe = regexp.MustCompile(`(?i)\b(providers|endpoints|pages|services|routes|models|handlers|components|modules|tables|views)\b`)
)

var fibValues = []int{1, 2, 3, 5, 8, 13}

func QuickEstimate(requirement string) []StoryEstimate {
	if strings.TrimSpace(requirement) == "" {
		return []StoryEstimate{{Title: "Implement requirement", Complexity: 3, Role: "junior"}}
	}

	conjunctions := conjunctionRe.FindAllStringIndex(requirement, -1)
	baseCount := maxInt(1, len(conjunctions)+1)

	pluralMatches := pluralIndicatorRe.FindAllString(requirement, -1)
	if len(pluralMatches) > 0 {
		baseCount += len(pluralMatches)
	}

	complexHits := len(complexityWordRe.FindAllString(requirement, -1))
	simpleHits := len(simplicityWordRe.FindAllString(requirement, -1))
	baseComplexity := 3 + complexHits - simpleHits
	baseComplexity = clampToFib(baseComplexity)

	stories := make([]StoryEstimate, 0, baseCount)
	for i := 0; i < baseCount; i++ {
		complexity := baseComplexity
		if i == 0 && baseCount > 2 {
			complexity = clampToFib(baseComplexity - 1)
		}
		if i == baseCount-1 && baseCount > 1 {
			complexity = clampToFib(baseComplexity + 1)
		}

		role := "junior"
		if complexity > 5 {
			role = "senior"
		} else if complexity > 3 {
			role = "intermediate"
		}

		stories = append(stories, StoryEstimate{
			Title:      generateTitle(i, baseCount),
			Complexity: complexity,
			Role:       role,
		})
	}

	return stories
}

func clampToFib(n int) int {
	if n <= 1 {
		return 1
	}
	if n >= 13 {
		return 13
	}
	best := 1
	for _, f := range fibValues {
		if f <= n {
			best = f
		}
	}
	return best
}

func generateTitle(index, total int) string {
	if total == 1 {
		return "Implement requirement"
	}
	if index == 0 {
		return "Setup and scaffolding"
	}
	if index == total-1 {
		return "Integration and testing"
	}
	return fmt.Sprintf("Implementation phase %d", index)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
