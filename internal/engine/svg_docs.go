package engine

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// Factory rule: architecture and sequence diagrams ship as REAL rendered SVG
// (valid <svg> XML that GitHub renders inline), never as Mermaid code fences or
// .mmd files. The scribe story instructs the coding agent to honour this, but
// agents frequently emit a ```mermaid block anyway. This post-merge loop is the
// deterministic backstop: it generates each diagram, validates it as well-formed
// SVG, and retries with the validation error fed back until it is valid — so a
// completed requirement always leaves renderable SVG diagrams, by construction.

// svgMaxAttempts bounds the validate-and-retry loop per diagram.
const svgMaxAttempts = 3

// diagramSpec describes one SVG diagram the factory guarantees.
type diagramSpec struct {
	filename string // path relative to repo root, e.g. "docs/architecture.svg"
	kind     string // "architecture" | "sequence"
	brief    string // what the diagram must depict
}

// factoryDiagrams is the standard diagram set every project documents.
var factoryDiagrams = []diagramSpec{
	{
		filename: "docs/architecture.svg",
		kind:     "architecture",
		brief:    "the system architecture: the main components/packages/services and how they depend on each other and on external systems",
	},
	{
		filename: "docs/sequence.svg",
		kind:     "sequence",
		brief:    "a sequence diagram of the primary end-to-end user flow, showing the actors/components as lifelines and the ordered messages between them",
	},
}

// mermaidSignatures are tokens that betray a Mermaid diagram masquerading as a
// diagram file. Their presence means the model ignored the SVG requirement.
var mermaidSignatures = []string{
	"```", "graph td", "graph lr", "graph rl", "graph bt",
	"flowchart", "sequencediagram", "classdiagram", "erdiagram",
	"statediagram", "gantt", "journey", "mermaid",
}

// validateSVG enforces the factory rule: content must be a single, well-formed
// SVG document (renders on GitHub), not a Mermaid fence or arbitrary text. It is
// a pure function — no I/O — so the boundary is unit-pinned.
func validateSVG(content string) error {
	s := strings.TrimSpace(content)
	if s == "" {
		return fmt.Errorf("empty content")
	}

	lower := strings.ToLower(s)
	for _, sig := range mermaidSignatures {
		if strings.Contains(lower, sig) {
			return fmt.Errorf("contains non-SVG/Mermaid signature %q — diagrams must be rendered SVG, not Mermaid or fenced code", sig)
		}
	}

	if !strings.HasPrefix(s, "<?xml") && !strings.HasPrefix(s, "<svg") {
		return fmt.Errorf("must begin with <?xml or <svg, got %q", firstRunes(s, 24))
	}
	if !strings.Contains(lower, "<svg") || !strings.Contains(lower, "</svg>") {
		return fmt.Errorf("missing <svg>…</svg> root element")
	}
	if !strings.Contains(lower, "xmlns") {
		return fmt.Errorf("missing xmlns namespace — GitHub will not render an SVG without it")
	}

	// Well-formedness + the document element must be <svg>.
	dec := xml.NewDecoder(strings.NewReader(s))
	sawSVGRoot := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("not well-formed XML: %w", err)
		}
		if start, ok := tok.(xml.StartElement); ok && !sawSVGRoot {
			if strings.ToLower(start.Name.Local) != "svg" {
				return fmt.Errorf("root element is <%s>, must be <svg>", start.Name.Local)
			}
			sawSVGRoot = true
		}
	}
	if !sawSVGRoot {
		return fmt.Errorf("no <svg> start element found")
	}
	return nil
}

// firstRunes returns up to n runes of s for compact error messages.
func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// extractSVG pulls an SVG document out of a model response that may be wrapped
// in prose or code fences. Returns the substring from the first <svg (or the
// <?xml that precedes it) through the final </svg>. Empty if no SVG is present.
func extractSVG(resp string) string {
	s := stripMarkdownFences(resp)
	lower := strings.ToLower(s)
	open := strings.Index(lower, "<svg")
	if open < 0 {
		return strings.TrimSpace(s)
	}
	// Keep a leading XML declaration if it immediately precedes the <svg.
	if decl := strings.LastIndex(lower[:open], "<?xml"); decl >= 0 {
		open = decl
	}
	closeTag := strings.LastIndex(lower, "</svg>")
	if closeTag < 0 {
		return strings.TrimSpace(s[open:])
	}
	return strings.TrimSpace(s[open : closeTag+len("</svg>")])
}

// generateSVGDiagram asks the model for one diagram and loops, feeding the
// validation error back, until the output is valid SVG or attempts run out.
func generateSVGDiagram(ctx context.Context, client llm.Client, model string, spec diagramSpec, reqTitle, projectInfo, fileTree string) (string, error) {
	base := fmt.Sprintf(`Author a %s diagram for a software project as a single, self-contained, rendered SVG file.

The diagram must depict: %s.

PROJECT: %s

PROJECT MANIFEST (truncated):
%s

FILE TREE (truncated):
%s

HARD REQUIREMENTS — this is a software-factory rule:
- Output ONLY raw SVG XML. Start with <svg xmlns="http://www.w3.org/2000/svg" ...> (an optional <?xml ...?> declaration first is fine) and end with </svg>.
- It MUST be valid, well-formed XML that GitHub renders inline.
- NO Markdown, NO code fences, NO Mermaid syntax, NO commentary before or after.
- Draw the boxes, arrows, lifelines and labels yourself with <rect>, <line>, <path>, <polygon>, <text> etc. Use a readable layout, a viewBox, sensible width/height, and legible <text> labels grounded in the actual components above (do not invent components that aren't in the manifest/tree).
- Keep it self-contained: no external images, fonts, or scripts.`,
		spec.kind, spec.brief, reqTitle,
		truncateForPrompt(projectInfo, 1500),
		truncateForPrompt(fileTree, 1800),
	)

	prompt := base
	var lastErr error
	for attempt := 1; attempt <= svgMaxAttempts; attempt++ {
		resp, err := client.Complete(ctx, llm.CompletionRequest{
			Model:     model,
			Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
			MaxTokens: 8000,
		})
		if err != nil {
			return "", fmt.Errorf("llm error on attempt %d: %w", attempt, err)
		}
		svg := extractSVG(resp.Content)
		if verr := validateSVG(svg); verr == nil {
			return svg, nil
		} else {
			lastErr = verr
			// Feed the exact failure back so the next attempt is corrective.
			prompt = fmt.Sprintf(`%s

Your previous attempt was REJECTED: %v

Previous output began with:
%s

Return ONLY a corrected, valid, self-contained SVG document (no Mermaid, no code fences, no prose).`,
				base, verr, firstRunes(strings.TrimSpace(resp.Content), 200))
		}
	}
	return "", fmt.Errorf("could not produce valid SVG after %d attempts: %w", svgMaxAttempts, lastErr)
}

// generateProjectDiagrams writes docs/architecture.svg and docs/sequence.svg
// into repoDir, generating valid SVG for any that are missing or invalid (e.g.
// the scribe agent left a Mermaid file). Returns the relative paths written.
func generateProjectDiagrams(ctx context.Context, repoDir, reqTitle, fileTree, projectInfo string, client llm.Client, model string) []string {
	var written []string
	for _, spec := range factoryDiagrams {
		abs := filepath.Join(repoDir, spec.filename)

		// Skip if a valid SVG already exists (agent honoured the rule).
		if existing, err := os.ReadFile(abs); err == nil {
			if validateSVG(string(existing)) == nil {
				log.Printf("[docs] %s already valid SVG, keeping", spec.filename)
				continue
			}
			log.Printf("[docs] %s exists but is not valid SVG — regenerating", spec.filename)
		}

		svg, err := generateSVGDiagram(ctx, client, model, spec, reqTitle, projectInfo, fileTree)
		if err != nil {
			log.Printf("[docs] failed to generate %s: %v", spec.filename, err)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			log.Printf("[docs] mkdir for %s failed: %v", spec.filename, err)
			continue
		}
		if err := os.WriteFile(abs, []byte(svg+"\n"), 0o644); err != nil {
			log.Printf("[docs] write %s failed: %v", spec.filename, err)
			continue
		}
		log.Printf("[docs] wrote %s (%d bytes)", spec.filename, len(svg))
		written = append(written, spec.filename)
	}
	return written
}

// diagramsReadmeBlock is the scribe-marked README section linking the SVGs. It
// is inserted only when the README does not already reference the diagrams, so
// hand-written architecture prose is never clobbered.
func diagramsReadmeBlock() string {
	return "\n<!-- nxd:diagrams:start -->\n## Architecture\n\n" +
		"![Architecture](docs/architecture.svg)\n\n" +
		"### Primary flow\n\n" +
		"![Sequence](docs/sequence.svg)\n" +
		"<!-- nxd:diagrams:end -->\n"
}

// ensureReadmeReferencesDiagrams appends the diagram block to README content if
// it does not already link the SVGs. Pure string transform — caller writes it.
func ensureReadmeReferencesDiagrams(readme string, written []string) string {
	if len(written) == 0 {
		return readme
	}
	if strings.Contains(readme, "docs/architecture.svg") || strings.Contains(readme, "docs/sequence.svg") {
		return readme
	}
	if strings.Contains(readme, "nxd:diagrams:start") {
		return readme
	}
	return strings.TrimRight(readme, "\n") + "\n" + diagramsReadmeBlock()
}
