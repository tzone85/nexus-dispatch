package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RuleSource records where a rule came from: the shipped baseline or a rule the
// agent learned from a confirmed finding (the self-upskilling path).
type RuleSource string

const (
	RuleBaseline RuleSource = "baseline"
	RuleLearned  RuleSource = "learned"
)

// VulnRule is one entry in the security knowledge base: a vulnerability class
// the review should check for, with concrete detection and remediation guidance
// the LLM and the planner standards both consume.
type VulnRule struct {
	ID          string     `json:"id"`                  // "A03:2021" or "CWE-89"
	Category    string     `json:"category,omitempty"`  // OWASP category name
	CWE         string     `json:"cwe,omitempty"`       // primary CWE
	Title       string     `json:"title"`               // short name
	Detection   string     `json:"detection"`           // what to look for
	Remediation string     `json:"remediation"`         // how to fix
	Severity    Severity   `json:"severity"`            // default severity
	Languages   []string   `json:"languages,omitempty"` // empty = all languages
	Source      RuleSource `json:"source"`              // baseline | learned
	AddedAt     string     `json:"added_at,omitempty"`  // RFC3339, learned rules
}

// appliesTo reports whether the rule is relevant to any of the given languages.
// A rule with no Languages restriction applies everywhere.
func (r VulnRule) appliesTo(langs []string) bool {
	if len(r.Languages) == 0 {
		return true
	}
	for _, want := range langs {
		for _, have := range r.Languages {
			if strings.EqualFold(want, have) {
				return true
			}
		}
	}
	return false
}

// KnowledgeBase is the versioned, growable set of vulnerability rules the
// security agent applies. Version bumps on every Add so callers can detect when
// the agent has upskilled.
type KnowledgeBase struct {
	Version int        `json:"version"`
	Rules   []VulnRule `json:"rules"`
}

// Has reports whether a rule with the given ID exists (exact ID match; used for
// Add dedup).
func (kb *KnowledgeBase) Has(id string) bool {
	for _, r := range kb.Rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

// Covers reports whether the given vulnerability-class id is already represented
// in the knowledge base — matching either a rule's ID or its CWE field. An
// OWASP-indexed baseline rule (ID "A03:2021", CWE "CWE-89") therefore covers a
// finding whose class id is "CWE-89", so the agent does not re-learn a class it
// already ships guidance for.
func (kb *KnowledgeBase) Covers(id string) bool {
	for _, r := range kb.Rules {
		if r.ID == id || (r.CWE != "" && r.CWE == id) {
			return true
		}
	}
	return false
}

// Add returns a NEW KnowledgeBase with the rule appended and the version bumped.
// Adding a rule whose ID already exists is a no-op (returns an equivalent copy).
// The receiver is never mutated.
func (kb *KnowledgeBase) Add(rule VulnRule) *KnowledgeBase {
	rules := make([]VulnRule, len(kb.Rules))
	copy(rules, kb.Rules)
	next := &KnowledgeBase{Version: kb.Version, Rules: rules}
	if kb.Has(rule.ID) {
		return next
	}
	next.Rules = append(next.Rules, rule)
	next.Version = kb.Version + 1
	return next
}

// RulesFor returns the rules applicable to the given languages (language-
// agnostic rules always included).
func (kb *KnowledgeBase) RulesFor(langs []string) []VulnRule {
	out := make([]VulnRule, 0, len(kb.Rules))
	for _, r := range kb.Rules {
		if r.appliesTo(langs) {
			out = append(out, r)
		}
	}
	return out
}

// Checklist renders the applicable rules as a markdown checklist suitable for
// injection into an LLM review prompt, a planner standards block, or a coding
// agent's brief.
func (kb *KnowledgeBase) Checklist(langs []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Security knowledge base v%d — vulnerability classes to prevent and detect:\n", kb.Version)
	for _, r := range kb.RulesFor(langs) {
		id := r.ID
		if r.CWE != "" && !strings.Contains(id, r.CWE) {
			id = id + " / " + r.CWE
		}
		fmt.Fprintf(&b, "- [%s] %s (%s): %s — Fix: %s\n",
			strings.ToUpper(r.Severity.String()), r.Title, id, r.Detection, r.Remediation)
	}
	return b.String()
}

// Save writes the knowledge base to path as indented JSON, creating parent dirs.
func (kb *KnowledgeBase) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create knowledge dir: %w", err)
	}
	data, err := json.MarshalIndent(kb, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal knowledge base: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write knowledge base: %w", err)
	}
	return nil
}

// LoadKnowledgeBase reads the knowledge base from path. A missing file returns
// the shipped baseline (not an error) so a first run is always seeded.
func LoadKnowledgeBase(path string) (*KnowledgeBase, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return BaselineKnowledgeBase(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read knowledge base: %w", err)
	}
	var kb KnowledgeBase
	if err := json.Unmarshal(data, &kb); err != nil {
		return nil, fmt.Errorf("parse knowledge base: %w", err)
	}
	return &kb, nil
}

// BaselineKnowledgeBase returns the shipped knowledge base: the OWASP Top 10
// (2021) plus a curated set of high-value CWEs with concrete detection and
// remediation guidance. This is the floor every nxd-built project inherits.
func BaselineKnowledgeBase() *KnowledgeBase {
	rules := []VulnRule{
		{
			ID: "A01:2021", Category: "Broken Access Control", CWE: "CWE-284",
			Title: "Broken access control",
			Detection: "Endpoints/handlers that act on a resource without verifying the caller owns it or has the role; " +
				"missing authorization checks; IDOR (object IDs taken from the request and used without an ownership check).",
			Remediation: "Enforce authorization server-side on every request; deny by default; check ownership before mutating; never trust client-supplied role/identity.",
			Severity:    SeverityHigh,
		},
		{
			ID: "A02:2021", Category: "Cryptographic Failures", CWE: "CWE-327",
			Title: "Cryptographic failures",
			Detection: "Weak/legacy algorithms (MD5, SHA1, DES, ECB), hardcoded keys/IVs, math/rand for tokens, " +
				"secrets stored or transmitted in plaintext, TLS verification disabled.",
			Remediation: "Use modern primitives (AES-GCM, SHA-256+, argon2/bcrypt for passwords); crypto/rand for tokens; never disable TLS verification; keep secrets out of code.",
			Severity:    SeverityHigh,
		},
		{
			ID: "A03:2021", Category: "Injection", CWE: "CWE-89",
			Title: "Injection (SQL/command/template)",
			Detection: "String-concatenated SQL, shell commands built from input, unsanitised template rendering, " +
				"NoSQL query objects built from request data.",
			Remediation: "Parameterised queries / prepared statements; avoid shell — use exec with arg arrays; context-aware output encoding; allowlist validation.",
			Severity:    SeverityCritical,
		},
		{
			ID: "A04:2021", Category: "Insecure Design", CWE: "CWE-657",
			Title: "Insecure design",
			Detection: "Missing rate limiting on auth/expensive endpoints, no input bounds, trust boundaries crossed without validation, " +
				"security-relevant flows lacking a threat model.",
			Remediation: "Apply secure-design patterns: rate limits, quotas, fail-closed defaults, validate at every trust boundary, threat-model the feature.",
			Severity:    SeverityMedium,
		},
		{
			ID: "A05:2021", Category: "Security Misconfiguration", CWE: "CWE-16",
			Title: "Security misconfiguration",
			Detection: "Debug mode in prod, verbose error/stack traces returned to clients, permissive CORS (*), default credentials, " +
				"directory listing, missing security headers.",
			Remediation: "Harden defaults; disable debug in prod; return generic errors to clients; lock down CORS; set security headers (CSP, HSTS, X-Content-Type-Options).",
			Severity:    SeverityMedium,
		},
		{
			ID: "A06:2021", Category: "Vulnerable and Outdated Components", CWE: "CWE-1104",
			Title: "Vulnerable / outdated dependencies",
			Detection: "Dependencies with known CVEs, unpinned versions, abandoned packages, lockfile drift.",
			Remediation: "Run dependency audits (govulncheck, npm audit, pip-audit); pin and update; remove unused deps.",
			Severity:    SeverityHigh,
		},
		{
			ID: "A07:2021", Category: "Identification and Authentication Failures", CWE: "CWE-287",
			Title: "Authentication failures",
			Detection: "Weak password policy, no lockout/rate limit on login, predictable/again-usable session tokens, " +
				"JWT without expiry or signature verification, credentials in URLs.",
			Remediation: "Strong hashing (argon2/bcrypt), rate-limit + lockout, high-entropy session tokens, verify JWT signature + expiry, MFA where appropriate.",
			Severity:    SeverityHigh,
		},
		{
			ID: "A08:2021", Category: "Software and Data Integrity Failures", CWE: "CWE-502",
			Title: "Integrity failures (insecure deserialization, unsigned updates)",
			Detection: "Deserialising untrusted data into objects, loading code/plugins from untrusted sources, " +
				"unsigned/unverified update or CI artifacts.",
			Remediation: "Avoid native deserialization of untrusted input; verify signatures/checksums; pin and verify CI dependencies.",
			Severity:    SeverityHigh,
		},
		{
			ID: "A09:2021", Category: "Security Logging and Monitoring Failures", CWE: "CWE-778",
			Title: "Logging/monitoring failures",
			Detection: "Security events (auth, access-control denials) not logged; OR sensitive data (passwords, tokens, PII) written to logs.",
			Remediation: "Log security-relevant events with context; never log secrets/PII; ensure logs are tamper-evident and monitored.",
			Severity:    SeverityMedium,
		},
		{
			ID: "A10:2021", Category: "Server-Side Request Forgery", CWE: "CWE-918",
			Title: "Server-side request forgery (SSRF)",
			Detection: "Server fetches a URL taken from user input without validation; webhooks/callbacks/image-fetch features.",
			Remediation: "Allowlist destinations; block internal/metadata IP ranges; resolve+validate before connecting; disable redirects to internal hosts.",
			Severity:    SeverityHigh,
		},
		// High-value CWEs with concrete, cross-language detection signatures.
		{
			ID: "CWE-798", CWE: "CWE-798", Category: "Cryptographic Failures",
			Title: "Hardcoded credentials/secrets",
			Detection: "API keys, passwords, tokens, private keys, or DSNs literally present in source, config, or test fixtures.",
			Remediation: "Move secrets to environment variables or a secret manager; rotate any exposed secret; add secret scanning to CI.",
			Severity:    SeverityCritical,
		},
		{
			ID: "CWE-22", CWE: "CWE-22", Category: "Injection",
			Title: "Path traversal",
			Detection: "File paths built from user input without containment; '..' segments reaching the filesystem.",
			Remediation: "Resolve and verify the path stays within an allowed base dir; reject '..'; use safe join helpers.",
			Severity:    SeverityHigh,
		},
		{
			ID: "CWE-79", CWE: "CWE-79", Category: "Injection",
			Title: "Cross-site scripting (XSS)",
			Detection: "User input rendered into HTML/JS without context-aware encoding; dangerouslySetInnerHTML/v-html/innerHTML with untrusted data.",
			Remediation: "Context-aware output encoding; framework auto-escaping; sanitise HTML with a vetted library; set a strict CSP.",
			Severity:    SeverityHigh,
			Languages:   []string{"javascript", "typescript", "python", "php", "ruby", "html"},
		},
	}
	// Stamp the source on every shipped rule in one place so individual literals
	// stay focused on the security content.
	for i := range rules {
		rules[i].Source = RuleBaseline
	}
	return &KnowledgeBase{Version: 1, Rules: rules}
}
