package agent

// Score captures the outcome of a single agent completing a single story.
type Score struct {
	AgentID     string
	StoryID     string
	Quality     int // 1-5 (QA pass rate proxy)
	Reliability int // 1-5 (stuck/escalation rate proxy)
	DurationS   int // seconds to complete story
}

// AgentReputation aggregates an agent's historical performance.
type AgentReputation struct {
	AgentID        string
	TotalStories   int
	AvgQuality     float64
	AvgReliability float64
	AvgDurationS   float64
}

// ComputeReputation calculates aggregate reputation metrics from a slice
// of individual scores. Returns a zero-value AgentReputation for empty input.
func ComputeReputation(scores []Score) AgentReputation {
	if len(scores) == 0 {
		return AgentReputation{}
	}

	rep := AgentReputation{
		AgentID:      scores[0].AgentID,
		TotalStories: len(scores),
	}

	var totalQ, totalR, totalD int
	for _, s := range scores {
		totalQ += s.Quality
		totalR += s.Reliability
		totalD += s.DurationS
	}

	n := float64(len(scores))
	rep.AvgQuality = float64(totalQ) / n
	rep.AvgReliability = float64(totalR) / n
	rep.AvgDurationS = float64(totalD) / n

	return rep
}

// OverallScore computes a weighted score (0-100) for ranking agents.
// Weights: Quality 50%, Reliability 30%, Speed 20%.
func (r AgentReputation) OverallScore() float64 {
	if r.TotalStories == 0 {
		return 0
	}

	// Quality and Reliability are 1-5, normalize to 0-100.
	qualityNorm := (r.AvgQuality / 5.0) * 100
	reliabilityNorm := (r.AvgReliability / 5.0) * 100

	// Speed: lower duration is better. Cap at 3600s, inverse relationship.
	speedNorm := 100.0
	if r.AvgDurationS > 0 {
		capped := r.AvgDurationS
		if capped > 3600 {
			capped = 3600
		}
		speedNorm = (1.0 - capped/3600.0) * 100
	}

	return qualityNorm*0.5 + reliabilityNorm*0.3 + speedNorm*0.2
}
