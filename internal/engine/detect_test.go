package engine

import "testing"

func TestDetectFrontend(t *testing.T) {
	tests := []struct {
		name        string
		title, desc string
		ownedFiles  []string
		want        bool
	}{
		{"ui-keyword-title", "Build the landing page", "", nil, true},
		{"component-keyword", "Create reusable Button component", "", nil, true},
		{"dashboard-keyword", "Admin dashboard with charts", "", nil, true},
		{"frontend-in-desc", "", "Implement the frontend for task management", nil, true},
		{"tailwind-keyword", "Style the app", "Use Tailwind for the layout", nil, true},
		{"react-keyword", "Task list view", "React component rendering tasks", nil, true},
		{"owned-tsx", "Wire task state", "", []string{"src/App.tsx"}, true},
		{"owned-css", "Polish spacing", "", []string{"styles/main.css"}, true},
		{"owned-vue", "Item editor", "", []string{"src/Editor.vue"}, true},
		{"owned-svelte", "Item editor", "", []string{"src/Editor.svelte"}, true},
		{"owned-html", "Static page", "", []string{"public/index.html"}, true},
		{"backend-only", "Create REST API endpoints", "Express routes for tasks", nil, false},
		{"db-story", "Add database migrations", "Postgres schema for users", nil, false},
		{"go-files-only", "Implement parser", "", []string{"internal/parser/parser.go"}, false},
		{"cli-story", "Add --json flag to CLI", "", []string{"cmd/root.go"}, false},
		{"ssr", "Server-side rendering of the settings page", "", nil, true},
		// Substring traps: keywords must match whole words only.
		{"pagination-is-not-page", "Add pagination to the tasks API", "cursor-based pagination in the repository layer", nil, false},
		{"performance-is-not-form", "Improve performance of the query planner", "", nil, false},
		{"review-is-not-view", "Code review automation for PRs", "LLM review of diffs", nil, false},
		{"format-is-not-form", "Format output as JSON", "", nil, false},
		{"build-is-not-ui", "Build the release pipeline", "artifact signing", nil, false},
		// Server-side HTML and "responsive" infrastructure are NOT UI work.
		{"html-email-is-backend", "Generate HTML email report", "render the weekly digest as text/html", nil, false},
		{"responsive-gateway-is-backend", "Design a responsive API gateway", "low-latency request routing", nil, false},
		{"html-with-owned-file", "Static marketing site", "", []string{"public/index.html"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectFrontend(tt.title, tt.desc, tt.ownedFiles)
			if got != tt.want {
				t.Errorf("detectFrontend(%q, %q, %v) = %v, want %v", tt.title, tt.desc, tt.ownedFiles, got, tt.want)
			}
		})
	}
}
