These eight fixtures reproduce multiline layout collapse at small OCR edge
intersections in the saved `2026-09-10_04-36-05` run, revision
`86bc7080a49d46810e85b74801ffce119ae7101d`.

Paragraph IDs, text, line boxes, translations, preferred size, observed step,
physical parent bounds, protected source boxes, and preceding active line boxes
are fixed inputs. Paddle word regions in these targets coincide with line boxes;
the fixture omits redundant word/polygon metadata. Full-page replay uses the
original OCR JSON, including that metadata. `oldBase` records the collapsed
baseline region; tests compare fits using the same font and neighbors.

The default unit test uses the existing package test font. To use the saved run's
font, set `SYMPLLATE_LAYOUT_FONT_DIR` to the portable application directory
(containing `bin/fonts/regular.ttf`). Its SHA256 for this run is
`197d9f3703b4c00af609178876a8d73e396f64fe438b2c871778566632374be3`.

Run `go test ./internal/imagebatch -run 'TestSafeTranslationBase|TestPrepareRetainsRotated' -count=1 -v`.
No OCR, translation, or inpainting models are invoked by these fixtures.
