package engine

import (
	"path/filepath"
	"regexp"
	"strings"
)

// frontendFileExts are file extensions that mark a story as UI-facing when they
// appear in its owned files.
var frontendFileExts = map[string]bool{
	".tsx": true, ".jsx": true, ".vue": true, ".svelte": true,
	".css": true, ".scss": true, ".sass": true, ".less": true,
	".html": true, ".astro": true,
}

// detectFrontend checks if the story builds or changes a user-facing web UI.
// This triggers the FrontendDesignBrief injection (agent.FrontendDesignBrief)
// so agents produce distinctive, accessible frontends instead of generic
// AI-default design. Detection combines owned-file extensions (strongest
// signal) with title/description keywords.
func detectFrontend(title, description string, ownedFiles []string) bool {
	for _, f := range ownedFiles {
		if frontendFileExts[strings.ToLower(filepath.Ext(f))] {
			return true
		}
	}
	return frontendKeywordRe.MatchString(strings.ToLower(title + " " + description))
}

// frontendKeywordRe matches UI vocabulary as whole words only — plain substring
// matching trips on "pagination" (page), "performance" (form), "review" (view).
// Deliberately absent: "html" (server-side HTML emails/reports are backend
// work; real UI stories carry .html in owned files or another keyword) and
// "responsive" ("responsive API gateway" means fast, not responsive design).
// Hoisted to package level so detection never recompiles it.
var frontendKeywordRe = regexp.MustCompile(`\b(` +
	`frontend|front-end|ui|ux|user interface|` +
	`landing page|page|screen|view|component|widget|` +
	`dashboard|layout|styling|stylesheet|css|` +
	`tailwind|react|vue|svelte|next\.js|nextjs|astro|` +
	`design system|web app|webapp|website|` +
	`form|modal|navbar|navigation bar|sidebar|button|` +
	`theme|dark mode|typography` +
	`)\b`)
