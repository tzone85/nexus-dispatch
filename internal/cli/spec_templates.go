// internal/cli/spec_templates.go — body content for `nxd spec init`.
//
// Each template asks the 10 ZeeSpec questions for its dimension. Empty
// answers leave the placeholder visible; `nxd spec validate` flags them.
package cli

const specWhatTemplate = `<!--
Answer each question. Fill placeholders so ` + "`nxd spec validate`" + ` passes.
-->

## 1. What is this system in one sentence?
TODO

## 2. What problem does it solve that nothing else does?
TODO

## 3. What are the top 3 user-visible features?
- TODO
- TODO
- TODO

## 4. What out-of-scope features are tempting but rejected?
- TODO

## 5. What are the core domain entities (nouns)?
- TODO

## 6. What are the core operations (verbs)?
- TODO

## 7. What metrics define success?
- TODO

## 8. What are the explicit non-goals?
- TODO

## 9. What does v1.0 NOT do that v1.1 will?
TODO

## 10. What's the smallest demo that proves it works?
TODO
`

const specWhyTemplate = `<!--
Business rules and constraints — the rationale that prevents AI drift.
-->

## 1. Why does this system exist? (one paragraph)
TODO

## 2. Why now and not later?
TODO

## 3. Why is this approach better than the alternatives?
TODO

## 4. What business / product constraints are non-negotiable?
- TODO

## 5. What regulatory / compliance constraints apply?
- TODO

## 6. What's the budget envelope (time, cost, complexity)?
TODO

## 7. What measurable outcome must this hit?
TODO

## 8. Who is the system explicitly NOT for?
TODO

## 9. What past mistakes is this designed to avoid?
TODO

## 10. What's the single rule that, if broken, kills the project?
TODO
`

const specWhoTemplate = `<!--
Roles, permissions, and the explicit list of actors.
-->

## 1. Who are the human users?
- TODO

## 2. Who are the system actors (services, daemons, agents)?
- TODO

## 3. Who can do WHAT? (role → permission matrix)
TODO

## 4. Who is the system administrator?
TODO

## 5. Who reviews / approves changes?
TODO

## 6. Who is on-call for incidents?
TODO

## 7. Who owns the data, legally?
TODO

## 8. Who can NEVER access sensitive paths?
- TODO

## 9. Who depends on this system being up?
- TODO

## 10. Who is responsible for sunset / migration?
TODO
`

const specHowTemplate = `<!--
Architecture, technology, design choices.
-->

## 1. What language(s) and runtime(s)?
TODO

## 2. What primary frameworks / libraries?
- TODO

## 3. What's the deployment target (single binary / container / serverless)?
TODO

## 4. How is state persisted?
TODO

## 5. How are agents / services authenticated?
TODO

## 6. How is observability handled (logs / metrics / tracing)?
TODO

## 7. How are secrets managed?
TODO

## 8. How are failures recovered?
TODO

## 9. How is correctness validated (tests, types, linters)?
- TODO

## 10. What architecture patterns are explicitly required?
- TODO
`

const specWhenTemplate = `<!--
Timing — events, schedules, lifecycle.
-->

## 1. When does the system start (manually, on event, on schedule)?
TODO

## 2. When does each operation trigger?
TODO

## 3. What's the timeout for the longest legitimate request?
TODO

## 4. When does data expire?
TODO

## 5. When are background jobs scheduled?
TODO

## 6. What's the maintenance / downtime window?
TODO

## 7. When does an alert fire?
TODO

## 8. When does the system auto-scale?
TODO

## 9. When does a circuit break?
TODO

## 10. When does the system reject / refuse work?
TODO
`

const specWhereTemplate = `<!--
Boundaries — endpoints, hosts, network exposure.
-->

## 1. Where does the binary / container run?
TODO

## 2. Where is data stored at rest?
TODO

## 3. Where are external integrations made?
- TODO

## 4. What ports / protocols are exposed?
TODO

## 5. Where does logging go?
TODO

## 6. Where is the public API surface?
TODO

## 7. What's the trust boundary?
TODO

## 8. Where do secrets live (and where do they NEVER live)?
TODO

## 9. Where does the user interact (CLI / web / IDE)?
TODO

## 10. Where can this NOT run (constraints)?
TODO
`

const specUpstreamTemplate = `<!--
What feeds INTO this system from outside.
-->

## 1. What systems/services send us data?
- TODO

## 2. What's the contract (schema, format, freshness) for each input?
TODO

## 3. How are upstream failures detected?
TODO

## 4. What's the retry / backoff for upstream calls?
TODO

## 5. Who owns the upstream sources?
TODO

## 6. What happens when an upstream source is down?
TODO

## 7. What inputs are user-supplied vs system-generated?
TODO

## 8. What's the maximum input size / rate?
TODO

## 9. How is upstream input authenticated?
TODO

## 10. What input is sanitized / rejected?
TODO
`

const specDownstreamTemplate = `<!--
What this system PRODUCES for outside consumption.
-->

## 1. What systems/services consume our output?
- TODO

## 2. What's the contract for each output?
TODO

## 3. What's the SLA we publish?
TODO

## 4. What happens when downstream is unavailable?
TODO

## 5. Who owns each downstream consumer?
TODO

## 6. How are breaking changes communicated?
TODO

## 7. What's the data retention / deletion policy?
TODO

## 8. What audit trail do we emit?
TODO

## 9. What rate-limits do we expose?
TODO

## 10. How can a consumer signal that they're broken?
TODO
`
