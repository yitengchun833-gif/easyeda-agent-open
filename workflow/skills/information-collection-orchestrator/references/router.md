# Search selection, extraction router, and conflict controls

## Select sufficient discovery channels

For an ordinary query, start with one suitable available route or a known primary source. Add a channel for missing evidence, conflicting sources, important claims or explicitly requested depth. Different engines returning the same publisher do not establish independent corroboration. Do not run all installed search Skills merely to complete an ensemble.

| Query domain | Primary route | Add when justified |
|---|---|---|
| General web, news, facts | One suitable general search route or known primary source | Independent evidence for unresolved claims |
| Academic papers or citations | One applicable academic/citation specialist | Another index for a coverage gap |
| Code, libraries, APIs, bugs | Official documentation or a matching developer-search specialist | GitHub evidence for implementation-specific gaps |
| Jobs and companies | Career specialist for vacancies; campus-job Skill for campus recruiting | Company primary sources or platform-specific evidence |
| Products, markets, finance | Suitable domain workflow and primary evidence | Independent evidence proportionate to decision risk |

Merge results by canonical URL. Treat multiple discovery hits as a coverage signal, not proof. Rank and retain sources using, in order: direct relevance to the claim, primary/official status, inspectable evidence, appropriate recency, methodological transparency, and corroboration by independent sources. Exclude SEO copies, unattributed reposts, unsupported snippets, stale pages when freshness matters, and sources that do not actually support the intended claim.

## Choose one extractor per selected URL

| Page or task | Primary extractor | Compatible fallback | Do not use first |
|---|---|---|---|
| Normal public page or a discovered company/job URL | Host built-in page reader | Available Firecrawl Scrape, AnySearch Extract or Agent Reach reader | Browser automation |
| Repeated JS/SPA extraction with a stable schema | Crawl4AI | Playwright for one required interaction | Firecrawl crawl plus Crawl4AI on the same URLs |
| Login-dependent UI action or UI defect investigation | Playwright | Chrome control only when existing Chrome state is essential | Generic crawler retries |
| User's existing authenticated Chrome tab/session | Chrome control | Stop and ask if authentication blocks access | Copying profiles, cookies, or storage |
| Chinese social post/comment/second-level-comment dataset | MediaCrawler | Agent Reach for discovery only | Firecrawl or Crawl4AI as a replacement data pipeline |
| Recurring public-page changes | Firecrawl Monitor | Scheduled low-volume recheck, if allowed | Repeated ad-hoc crawls |

## Shared-resource rules

| Resource | Rule |
|---|---|
| Browser process | Keep one interactive owner per run. Do not run Playwright, Crawl4AI, and MediaCrawler against the same Chrome debugging port concurrently. |
| Chrome login state | Reuse only through the tool designed for it. Never export cookies, profile data, or passwords into task files. |
| CDP ports | Assign a unique port per concurrent run; document it in the route log, never hard-code `9222` as a shared default. |
| Proxy | Make proxy selection task-scoped. Do not share rotating proxies or use them to avoid platform controls. |
| Output files | Each run receives its own directory. Append JSONL logs; never allow tools to overwrite another tool's raw response. |
| Rate limits | Start with the configuration delay and concurrency of one. Lower a rate when the site signals a limit; do not retry around it. |

## Fallback ladder

1. Identify the selected route and any material coverage limitation. In structured mode, record it in the existing route log; ordinary chat lookup does not create logs.
2. If the content is absent because of rendering, choose Crawl4AI for a repeatable page or Playwright for a necessary interaction.
3. If blocked by login, CAPTCHA, terms, robots, or a rate limit, stop—not bypass. Use an official public page, subscription, or user-provided export if available.
4. In structured mode preserve the failure in `logs/events.jsonl` with `status`, `tool`, `source_url`, `reason`, and `next_action`; in lightweight mode explain the material limit in the answer without creating a run folder.

## Conflict diagnosis

- **Search overlap needs a reason:** AnySearch, Agent Reach and Firecrawl Search are alternative routes. Add another only for an identified evidence need; deduplicate URLs before extraction.
- **Duplicate extraction is not:** Firecrawl's many outcome-specific skills are wrappers around its core capability. Select the narrow workflow skill only when its outcome exactly matches; fetch each retained URL through one extractor unless the first extractor fails.
- **Browser collision:** Crawl4AI uses Playwright internally. Do not start a separate Playwright session for the same extraction unless Crawl4AI cannot perform the required UI operation.
- **Discovery/collection separation:** Selected channels discover candidates; one chosen extractor collects each retained page. Reuse adequate content already retrieved.
- **Fact/opinion mixing:** Store company-job facts with `record_kind=job_fact`; store social posts and comments with `record_kind=experience_signal`. Never use the latter as proof a vacancy exists.
