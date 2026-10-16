# The design, and what the direction sheet could and could not do here

## What was actually run

`design-taste-frontend` is written for web pages. Its own description says so:
"Not for dashboards or app screens". The shell is a native desktop window drawn
by Go, not HTML, so only part of the process applies. This is the honest record.

| Step | Ran? | Why |
| --- | --- | --- |
| `draw.mjs` — deal directions | **yes** | Needs no browser. Dealt three; `product-performs` was chosen. |
| `lint.mjs` — banned patterns | no | Scans HTML source. There is none. |
| `inspect.mjs` — measure in a browser | **no** | Takes a URL or an `.html` file and drives Chrome. A Go native window is neither. |
| Critique from screenshots | **yes** | Screenshots of the running app, looked at and fixed (see below). |
| `assets.mjs` — find pictures | no | It supplies photographs for a page. An app's imagery is its own interface. |

## What the direction sheet gave, and what was taken

Dealt direction: **product-performs** — "software is most convincing when it is
doing its job in front of you". Seed `2103996900`.

**Taken:**

- **The palette, "frost and slate".** Cool white ground, one paler blue-grey
  step for surfaces, type in dark slate rather than black, and a single teal
  accent. This replaced the earlier iOS-style palette, which had been picked by
  hand rather than drawn.
- **The type floor.** The house rules say nothing smaller than 13px and running
  text at 16px or more. The interface had 8 sizes at 11px and 20 at 12px — all
  of them violations — and six distinct sizes, over the five-step ceiling. Every
  size now maps onto five steps: 13 / 16 / 19 / 22 / 44.
- **The idea.** "The product performs": the Stats page is not a description of
  the product, it is the product's own measurement, live.
- **Accent discipline.** The sheet puts the accent on the action and one detail
  per screen. The selected sidebar row was taking it; it now steps in tone and
  carries a small accent bar, leaving the accent for the primary action and the
  live reading.

**Not taken, and why:**

- **The typefaces, League Spartan and Figtree.** mygo's native UI sets a family
  by name (`Theme.Font`) and has no font-loading API, so a bundled face cannot
  ship. The system UI face is used at the dealt sizes instead.
- **The hero, the 90svh full-bleed chart, the photograph sets, the load
  animation.** These are page compositions. A tray app's window has no hero, and
  its first screen is the measurement itself.
- **The four-picture floor.** The sheet's floor counts photographs and cropped
  interface fragments. An app window has no photographs to carry, and the
  fragments it does have are the interface. The floor was not met and could not
  honestly be.

## What the screenshots changed

Looking at the running window, not the code:

1. The latency chart's target line was drawn in the hairline colour on a
   surface one step away from it — a contrast ratio near 1.1:1, effectively
   invisible. It now uses the muted ink and is labelled.
2. The chart's vertical range was fixed at ±20% of the target, so a series that
   varied by 40 ms sat in a thin strip. It now spans what was measured, with a
   margin, so the shape fills the plot.
3. The sidebar's selected row was filled with the accent, spending the accent on
   navigation. It now steps in tone with a small accent bar.
4. Labels on the large figures were being clipped by the fixed row gap. The row
   now wraps.
5. An empty trend card was a large empty frame — a hole, which the house rules
   name as a fault. Before the first reading it is now two lines of text.

## The one thing worth stating plainly

This is a **desktop app designed with a web-page skill's rules**, and the
automated half of that skill cannot check it. Every judgement above came from
looking at screenshots of the running window. Where the skill's process could
not run, this file says so rather than implying it did.


---

# Second pass: making it feel native

The first pass still looked like a web dashboard wearing a desktop window. The
`native-feel-cross-platform-desktop` audit named why, and this pass fixes each
item. Its central tenet is T3: *adopt the platform; don't compete with it.*

## What was wrong, and what replaced it

| Looked like a web app | What it is now | Why |
| --- | --- | --- |
| A hand-drawn "Stats" title bar inside the window | Removed | The window already has a title bar the platform draws, drags and matches to the rest of the system. A second one competes with it. |
| Four cards stacked down the page | One surface, sections separated by space | A window is already a surface. Cards inside a window are a dashboard idiom. |
| Three big numbers in a row, each in its own box | One headline figure, then label-and-value rows | A system inspector lists label and value; the eye compares down a column, so the values are right-aligned to one edge. |
| 90 thin bars in the chart | A line, downsampled to about one point per 9 px, keeping each bucket's peak | A reading every 250 ms in a 540-px plot is a picket fence and says nothing. |
| The framework's blue sliders next to a teal interface | The framework's theme is set to the palette | Two accents in one window reads as two applications. |
| `Fill()` on the scroll view | `Grow(1)` | `Fill` asks for the parent's whole height, which pushed the content up out of the window. |
| Eleven- and twelve-pixel labels | Thirteen and sixteen | The house rules' floor. |

## The bug the screenshots caught

Setting the framework theme without carrying its `Dark` flag made the next frame
read the wrong appearance: the sidebar turned light while the content stayed
dark, and the navigation vanished into the background. Visible only in a
screenshot; the code looked correct.

## What the skill's audit still lists as unmet

Honest gaps, from `references/06-native-conventions.md`:

- **System accent colour.** mygo's theme is set from this app's palette, so the
  accent is the design's teal rather than the user's chosen OS accent. Following
  it would need a hook mygo does not expose on the native-UI path.
- **Window remembers its size and position** — done, through `StateKey`.
- **Single-instance behaviour on Windows** — not implemented. A second launch
  currently starts a second app.
- **Auto-update as a real process** — not implemented.
- **Native context menu, native notifications** — not implemented; nothing in
  the app opens a menu or notifies yet.
- **Full keyboard navigation** — the framework provides Tab and arrow movement;
  it has not been audited against this app's controls.
