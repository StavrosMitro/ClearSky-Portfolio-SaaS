# Grade characterization (roadmap 2.2)

These files pin down how the **legacy** Node grade services (`initial_grades`,
`final_grades`, `stats_service`, `View_personal_grades`) read an e-sec workbook
and build the statistics, so the replacement services (`grades_ingest_service`,
`grades_query_service`) can be checked against the same results. The legacy
services were retired in roadmap 3.10; their code remains in the git history
(commit `57cd8b5`).

| Path | Content |
|---|---|
| `fixtures/*.xlsx` | Workbooks in the legacy template: row 0 title, row 1 question weights (from column 8), row 2 headers, rows 3+ students |
| `golden/*.json` | For each fixture, the legacy parse result (`docs`) and the histograms `stats_service` served (`histograms`) |
| `capture.js` | The legacy parsing and `fetchHistogram` code, copied verbatim from commit `57cd8b5`, with MySQL storage modelled (`grade` DECIMAL(4,2), `Q1..Q10` INT) |
| `generate-fixtures.js` | Deterministic fixture generator |

Legacy behaviour worth knowing:

- Question scores are stored **multiplied by their weight**, and only columns
  8–17 (Q1–Q10) are read.
- The statistics round each value to the nearest integer; the total uses bins
  0–10 and each question 0 to its highest rounded value.
- Fixtures avoid `.5` ties on purpose: MySQL's rounding of ties when storing a
  float into an INT depends on the platform.

Regenerate (only if the fixtures must change):

```bash
cd tests/characterization
npm install
npm run fixtures && npm run capture
```

The Go tests of the new services read `fixtures/` and `golden/` directly.
