package agent

// FrontendDesignBrief is nxd's frontend design skill: the standards block
// injected into the goal prompt of every UI-facing story (PromptContext.
// IsFrontend, detected in the engine from owned files + story text).
//
// Synthesized from Anthropic's frontend-design skill and current guidance on
// avoiding generic AI-generated design ("AI slop"): token-first planning, one
// signature element, named anti-pattern looks, real copy, and a non-negotiable
// accessibility floor. Content lives in one const so the planner, the goal
// prompt, and tests share a single source of truth. Keep it under the size
// budget pinned by TestFrontendDesignBrief_SizeBudget — it rides on every
// UI-story dispatch.
const FrontendDesignBrief = `
## FRONTEND DESIGN — MANDATORY STANDARDS

You are also the design lead for this UI. The client rejects anything that
looks templated. Make deliberate, opinionated choices specific to THIS
product, its audience, and this page's single job.

### Two-pass process (plan tokens BEFORE code)
1. Write a compact design-token plan first, as a comment block or DESIGN.md:
   - Palette: 4-6 named hex values — one dominant color creating atmosphere,
     one sharp accent. Not evenly-distributed timid pastels.
   - Type: 2+ roles — a characterful display face used with restraint, a
     complementary body face (never the same family you'd pick for any other
     project), optional utility face for data.
   - Layout: one-sentence concept. Structure must encode something true about
     the content (numbered markers only if the content really is a sequence).
   - Signature: the ONE element this page will be remembered by. Spend your
     boldness there; keep everything around it quiet and disciplined.
2. Critique the plan before coding: if any part is what you would produce for
   ANY similar brief, it is a default, not a choice — revise it. Then write
   the code deriving every color and type decision from the plan. Encode the
   tokens once (CSS custom properties or the Tailwind theme), never ad-hoc
   per component.

### Banned defaults (these read as AI-generated)
- Fonts: Inter, Roboto, Open Sans, Lato, Arial, bare system-ui as the design.
- Purple/blue-purple gradients on white; emerald or acid-green single accent
  on near-black; warm cream #F4F1EA + serif display + terracotta accent;
  broadsheet hairlines with zero border-radius EVERYWHERE. All four are
  legitimate only if the brief explicitly asks for them.
- The template page: gradient hero → vague centered headline → three feature
  cards with icons → testimonials → footer. Uniform 16px-radius cards.
- Scattered animations. One orchestrated moment (a page-load sequence or a
  scroll reveal) beats effects everywhere; extra motion reads as generated.

### Quality floor (non-negotiable, never announced in the UI)
- Responsive down to 360px wide; no horizontal scroll, no overlapping text.
- Visible keyboard focus on every interactive element (focus-visible ring).
- prefers-reduced-motion respected: gate every animation on it.
- WCAG AA contrast: 4.5:1 body text, 3:1 large text and UI components.
- Touch targets at least 44x44px. Semantic HTML (nav/main/button, alt text,
  labels tied to inputs) — a div with onClick is not a button.
- Empty, loading, and error states designed, not defaulted.
- Watch CSS specificity: section-level and element-level spacing rules that
  cancel each other are the classic generated-CSS failure.

### Copy is design material
Write real copy for THIS product — never lorem ipsum or vague marketing
lines. Buttons say exactly what happens ("Save changes", never "Submit");
the same action keeps the same name through the whole flow. Errors state
what went wrong and how to fix it, without apologizing. Name things by what
the user controls ("notifications"), not how the system is built ("webhook
config"). Active voice, sentence case, no filler.
`
