# Changelog

## 0.1.0 (2026-11-04)


### Features

* add the sample data core's make seed asks for (EN-205) ([187541d](https://github.com/UnlockedLabs/unlocked-open-content/commit/187541d270517787281d2eeb0a53b7ec423c6ac6))
* move Open Content from unlocked-core (EN-205) ([070dbc1](https://github.com/UnlockedLabs/unlocked-open-content/commit/070dbc1b26d1b757f2657b22bff9ea5132186ba6))
* move Open Content out of core (EN-205) ([c01e945](https://github.com/UnlockedLabs/unlocked-open-content/commit/c01e9458f232180cfd0b386e3a80c236bd67198e))
* move Open Content out of core (EN-205) ([c01e945](https://github.com/UnlockedLabs/unlocked-open-content/commit/c01e9458f232180cfd0b386e3a80c236bd67198e))
* serve core's request metrics on /api/metrics (EN-205) ([21294b8](https://github.com/UnlockedLabs/unlocked-open-content/commit/21294b86402387e6a3cd99a70877c9417ed81ac4))


### Bug Fixes

* allowlist order_by and order before they reach SQL and give outbound HTTP calls a context and timeout (EN-205) ([1a6c50c](https://github.com/UnlockedLabs/unlocked-open-content/commit/1a6c50cd8bded498b8ec4dddf799c5eb3063e700))
* average session length in SQL instead of loading every visit (EN-205) ([dc9a7dc](https://github.com/UnlockedLabs/unlocked-open-content/commit/dc9a7dc160c3cd17af91cc3fc3a81964ee3896d0))
* build and check with Go 1.26.10 (EN-205) ([4ac9782](https://github.com/UnlockedLabs/unlocked-open-content/commit/4ac9782cf488626122eaf38a66e66ea97e146f4a))
* cache each library's visibility per facility (EN-205) ([6383e28](https://github.com/UnlockedLabs/unlocked-open-content/commit/6383e289d937fb4f39754a4355d9bc969ba270fc))
* don't re-seed a site whose helpful links were all deleted (EN-205) ([3f7612c](https://github.com/UnlockedLabs/unlocked-open-content/commit/3f7612c1f4ee66fb38ae31a4a92b63035ec8316d))
* exit cleanly when stopped before starting (EN-205) ([ffb3edb](https://github.com/UnlockedLabs/unlocked-open-content/commit/ffb3edbca109a4dd3260a7906913f1d224181801))
* guard the helpful-links sort order against concurrent requests (EN-205) ([bdeb46b](https://github.com/UnlockedLabs/unlocked-open-content/commit/bdeb46bc51b605a5abafa6455e5ee23872589e17))
* parse YouTube video IDs with net/url so a bare youtu.be can't panic (EN-205) ([559e40d](https://github.com/UnlockedLabs/unlocked-open-content/commit/559e40d05c721a22c70d999a0b42ecca8f6b2bf6))
* read favorites and activity only as the caller (EN-205) ([328fa1a](https://github.com/UnlockedLabs/unlocked-open-content/commit/328fa1a75d8b99c623c3509db5985a469cf58a15))
* seed from the worker, keep core's start delay and require the site's token (EN-205) ([fe2780e](https://github.com/UnlockedLabs/unlocked-open-content/commit/fe2780ed2e2ff98c6cd8411608bcadee0ef0e1ed))
* share one transport for the library proxy so idle connections close (EN-205) ([69812c7](https://github.com/UnlockedLabs/unlocked-open-content/commit/69812c70e483ae6f892e61eebfbf398686c274db))
