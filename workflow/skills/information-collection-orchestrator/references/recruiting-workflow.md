# Recruitment information workflow

1. Use AnySearch, Agent Reach, and Firecrawl Search to discover official company career pages and public job leads; add the campus-job Skill for campus recruiting. Merge canonical URLs and capture only links, titles, discovery tools, and snippets at this phase.
2. Use Firecrawl for each official career page. Use Crawl4AI only if that page needs client-side rendering or a repeatable local schema.
3. Normalize confirmed vacancies as `job_fact`. Include company, role, location, requirements, publication date when present, original URL, and evidence excerpt.
4. Use MediaCrawler only when the user asks for Chinese social-platform experience signals. Normalize posts/comments as `experience_signal`; remove personal contact details and do not treat the content as a vacancy confirmation.
5. Run `process_records.py`; use only accepted high-quality items, review rejected and duplicate items separately, then return users to the original official page to verify and apply.

Suggested factual confidence:

- Official company career page: 1.00
- Official employer social account: 0.85
- Established job board listing: 0.70
- Employee/referral discussion: 0.45
- Unverified repost: 0.25
