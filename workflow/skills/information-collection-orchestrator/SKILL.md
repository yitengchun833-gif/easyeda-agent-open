---
name: information-collection-orchestrator
description: "Plan information retrieval when source/channel selection is unclear, evidence needs multiple sources, or the user requests structured collection, crawling or monitoring. 用户说查资料或采集目标即可；明确专业渠道或已知可靠来源可直接使用，不为每次联网强制串联所有搜索Skill。"
---

# Information Collection Orchestrator

## Portable workflow integration and privacy

This is the workflow's search matrix: research is the agent's evidence-gathering behavior. Prefer the host's built-in search/open-page tools for ordinary public research. AnySearch, Agent Reach, Firecrawl and the other routes below are optional adapters, available only after checking the new host's actual tools. Their credentials, login sessions and installations are not included. If absent, use built-in search and disclose coverage gaps; do not claim every channel ran.

Use lightweight mode for this workflow and write conclusions and sources into its existing findings.md. Structured collection is only for an explicitly requested dataset; do not add a second progress system.

Before any external query, strip local paths, usernames, private project names, customer identifiers, device serials, unpublished measurements and private document excerpts. Search public model numbers and generic technical questions only when they are non-confidential. If the question cannot be answered without disclosing private content, stop that query and explain the minimum permission needed. Never upload supplied files to search/crawling services or transmit secrets via URLs. Never package auth files, cookies, account tokens, proxy credentials or personal settings. Do not echo secret values during configuration checks.

Use this skill as the single routing entry point for information collection. Classify the source, goal, and expected deliverable before retrieval.

For an ordinary query, select one sufficient available discovery route, preferring a relevant domain specialist or primary source. AnySearch, Agent Reach and Firecrawl Search are alternatives, not a mandatory ensemble. Add independent sources or another route when evidence is missing, conflicting, high-stakes or the user requests deeper coverage. Source independence is not established merely by using different search engines. Run genuinely independent retrievals in parallel only when useful and permitted.

After discovery, canonicalize and deduplicate URLs, compare source quality, relevance, recency, direct evidence, and corroboration, and retain only sources suitable for the answer. Do not treat search rank or repeated appearance as proof of quality. Use one appropriate reader, extractor, or browser owner per selected URL; do not repeatedly fetch the same page through multiple crawlers merely to compare outputs.

## Choose the operating mode

### Lightweight lookup

Use for a small number of questions, pages, or facts whose deliverable belongs in the current chat.

1. Read `references/router.md` only when channel selection or fallback is unclear.
2. Use the selected sufficient route, or read a known source directly; merge and canonicalize candidates if additional retrieval is justified.
3. Screen candidates for authority, relevance, recency, evidence quality, and corroboration. Retrieve only the strongest sources needed for the answer.
4. Cite the retained sources and separate confirmed facts, inference, conflicting evidence, and unknowns.
5. Do not create a run folder, raw copy, normalized dataset, task JSON, or logs unless the user requests a saved artifact.

### Structured collection

Use for batch or multi-source collection, monitoring, reusable datasets, recurring workflows, or an explicitly requested evidence package.

1. Copy `config.example.json` into the task workspace and adjust limits only when needed.
2. Create a task document following `references/task.example.json`.
3. Read `references/router.md`; record the selected channels and coverage rationale, and choose one extraction route per selected URL.
4. Create a run folder containing `task.json`, `config.json`, `raw/`, `normalized/`, and `logs/`.
5. Record a one-line decision in `logs/route.jsonl`: intent, discovery Skills used, domain-specific additions, per-URL extraction route, unavailable search backends, and reason.

Never put cookies, passwords, access tokens, browser profiles, or private contact information in the run folder or output.

## Routing rules

- Consider **AnySearch** for general or vertical-domain discovery when no more suitable specialist is available.
- Use **Agent Reach** for platform-specific public content, career leads, social discussion or GitHub discovery when that route fits. Run its health check when a multi-backend platform is involved.
- Use **Firecrawl Search** when search with full-page extraction is useful; reuse hydrated content instead of scraping it again.
- For papers, code, jobs, products or other specialist domains, prefer the matching specialist as the primary route; add channels only for a concrete evidence gap.
- Use **Firecrawl** after discovery for a selected company site, job page, URL mapping, low-volume crawl, structured extraction, or managed monitoring.
- Use **Crawl4AI** for JavaScript-heavy pages, repeatable local schema extraction, local batch pipelines, or reusable CSS/JSON extraction rules.
- Use **Playwright** only for a necessary visible interaction or UI-flow investigation. It is not the default scraper.
- Use **Chrome control** only when the task genuinely needs the user's already-authenticated Chrome state. Do not inspect cookies, storage, passwords, or profiles.
- Use **MediaCrawler** only for supported Chinese content platforms when the task explicitly needs post, author, comment, or second-level-comment data. Treat it as a separate social-data source, not a general web crawler.

Read `references/router.md` for conflict controls and fallbacks. Read `references/recruiting-workflow.md` for the recruitment workflow.

## Structured result contract

In structured mode, normalize every retrieved item to `references/schema.json`. Preserve raw material separately and retain the original URL, retrieval time, discovery Skill, extraction tool, and evidence excerpt. Classify job facts and experience/commentary separately. Apply the configured minimum quality score, sort accepted records by quality, and keep rejected and duplicate items separate. Lightweight lookup does not require this storage contract, but follows the same quality criteria.

Run the deterministic post-processor after a collection:

```powershell
python scripts/process_records.py --task task.json --config config.json --input raw/records.json --output normalized/result.json
```

The processor validates required provenance, canonicalizes URLs, removes duplicates, applies the documented quality score, creates a failure/decision log, and does not make network calls.

## Failure handling

If the selected backend is unavailable, choose one compatible available alternative and disclose any material coverage limit; do not claim full coverage. For extraction failures, use the next compatible fallback from the task's route plan. Do not bypass a CAPTCHA, sign-in barrier, access control, robots policy, rate limit, or platform restriction. Mark the source as `blocked`, explain the reason, and retain only non-sensitive diagnostic information.

Stop a collection if its configured page, record, concurrency, or rate limits would be exceeded. Ask for permission before materially increasing those limits or adding a new site category.
