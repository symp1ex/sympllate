# Candidate collision fixtures

Eight existing candidates from saved layout replay of revision
`86bc7080a49d46810e85b74801ffce119ae7101d`, run
`dist/portable/_output/2026-09-10_04-36-05`.

Each fixture retains the translation, preferred typography, alignment, source
base, one generated candidate, and the source/active rectangles intersecting
that candidate. Neighbor state is fixed for this diagnostic test; these are not
expected sequential page results. No OCR/translation/inpainting is invoked.

`Maximum` and `Admissible` describe the portable regular.ttf (SHA256
`197d9f3703b4c00af609178876a8d73e396f64fe438b2c871778566632374be3`).
Set `SYMPLLATE_LAYOUT_FONT_DIR` to the portable directory to assert those exact
measurements. The default package font instead checks the exhaustive score
oracle without assuming identical metrics. T5/b58 admits 14.75 in this fixed
candidate, but the existing score prefers 13.75 after a line-break change.

T4/b13 retains its existing semantic unit. This fixture does not correct OCR
fusion of the tab labels.

`candidate_font_guard.json` retains the full fixed source/active constraints
from the first-fix replay for five guard cases. T5/b23 preserves the larger
force result despite a better-scoring smaller recovery; T5/b34 preserves the
larger emergency result. T1/b56 and T4/b13 preserve the cheaper force result
despite a larger recovery. T1/b16 accepts the larger, cheaper normal result.
The portable font checks exact saved outcomes; the package font checks the
same guard invariants against the previous phase policy.
