package sanitize

import (
	"regexp"
	"strings"
)

var (
	htmlTagRe    = regexp.MustCompile(`<[^>]*>`)
	multiSpaceRe = regexp.MustCompile(`\s+`)

	injectionPatterns = []string{
		"ignore previous instructions",
		"ignore all previous",
		"disregard prior",
		"system prompt override",
		"you are now",
		"<|system|>",
		"<|im_start|>",
		"new instructions",
		"override your",
		"forget your instructions",
	}

	secretPatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-ant-[a-zA-Z0-9\-]{20,}`),
		regexp.MustCompile(`sk-[a-zA-Z0-9]{32,}`),
		regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`),
		regexp.MustCompile(`password\s*[:=]\s*"[^"]{4,}"`),
		regexp.MustCompile(`aws_secret_access_key\s*=\s*"[^"]+"`),
		regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9\-_.]{20,}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`-----BEGIN\s+(RSA\s+)?PRIVATE\s+KEY-----`),
	}
)

const MaxContentLen = 2000

func Content(raw string) string {
	stripped := htmlTagRe.ReplaceAllString(raw, " ")
	collapsed := multiSpaceRe.ReplaceAllString(strings.TrimSpace(stripped), " ")
	if len(collapsed) > MaxContentLen {
		return collapsed[:MaxContentLen]
	}
	return collapsed
}

func DetectPromptInjection(content string) bool {
	lower := strings.ToLower(content)
	for _, pattern := range injectionPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func ScanForSecrets(content string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(content) {
			return true
		}
	}
	return false
}
