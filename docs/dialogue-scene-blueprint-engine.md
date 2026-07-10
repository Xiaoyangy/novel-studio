# Dialogue Scene Blueprint Engine

`dialogue_scene_blueprints` is the prewrite contract for key dialogue scenes. It turns dialogue craft into structured planning without forcing every scene into a dialogue-first opening.

## Purpose

The field exists because `voice_logic` answers "how this character speaks", while a dialogue scene also needs to answer:

- which dialogue mode fits the current scene pressure;
- whether speech, action, silence, object evidence, misunderstanding, or environmental voice should open the scene;
- where the reader is standing before any explanation starts;
- what the POV character misreads, delays, denies, or physically reacts to;
- how much memory or identity context is allowed before the scene becomes an info dump;
- how each exchange changes information, pressure, relationship, or power;
- which value the scene flips, who holds power when it ends, and who is watching.

## Mode Taxonomy

`dialogue_mode` is an open enum. The built-in modes fall into five families:

### Defensive / pressure family

| Mode | Core move |
|---|---|
| `pressure_negotiation` | both sides trade under constraint; concessions cost something |
| `interrogation` | one side extracts information the other protects |
| `plea_for_help` | surface request hides risk transfer |
| `logistics_under_stress` | task talk carrying fear or blame |
| `avoidance` | one side refuses engagement; cold handling |
| `silence_pressure` | silence itself is the weapon |
| `status_report` | report whose subtext contradicts its surface |
| `coercion_blackmail` | unlike negotiation, the coercer leaves no real choice; menace comes from calm and specificity, not shouting |

### Offensive / revelation family

| Mode | Core move |
|---|---|
| `reveal_showdown` | information is poured out, not extracted: evidence lands piece by piece with body-reaction beats between |
| `public_confrontation` | audience required; half of every line performs for third parties, and audience reaction bends the scene |
| `recruitment_temptation` | an offer that must be genuinely tempting; write the beat where the target is almost persuaded, or refusal is weightless |
| `mutual_probing` | symmetric power, both hide and both fish; whoever asks the real question first exposes themselves |

### Relationship-turn family

| Mode | Core move |
|---|---|
| `confession` | admission of feeling or guilt |
| `banter_masking_fear` | teasing/flirting that hides stakes |
| `misunderstanding_escalation` | each turn compounds a misread |
| `rupture` | irreversible break; the exit beat must be a concrete "no way back" object or act |
| `reconciliation_apology` | apology forbidden to say "I'm sorry, I was wrong" directly; carried by action, old shared references, handed objects |
| `breaking_bad_news` | write the teller's delay and the hearer's refusal to understand; the news itself gets the shortest line |
| `farewell` | parting, possibly permanent |

### Emotional-depth family

| Mode | Core move |
|---|---|
| `mentorship_teaching` | strict exposition budget; teach only what the moment needs, focus on the relationship and on learning wrong |
| `uninhibited_truth` | drunk/wounded/dying filter drop; truth comes out broken, out of order, with regret — not lucid |
| `mundane_talk_bearing_weight` | surface is dinner, weather, bills; the real matter is never spoken, it leaks through action beats and pauses |

### Structurally special family

| Mode | Core move | Extra required fields |
|---|---|---|
| `group_council` | 3+ parties with factions; at least one defection or abstention changes the wind | `participants`, `objective_tactics[].faction`, `coalition_shift` |
| `overheard` | dialogue not addressed to the POV; POV cannot respond, can only misread and carry away a wrong conclusion | `pov_role` = eavesdropper/bystander |
| `mediated_exchange` | phone, text, letter, through a door, via an intermediary; no body beats — use medium beats (read-no-reply, typed-then-deleted, letter creases, footsteps behind a door) | `medium` |

Projects may define additional modes (e.g. reunion/认亲, auction bidding, court debate) as long as the orthogonal axes below are still filled in.

## Orthogonal Axes

Modes alone cannot enumerate scenes; what actually determines the prose is the combination of independent variables. Every blueprint must therefore fill:

- `value_shift` (McKee's scene law): the value at stake (trust, safety, hope, intimacy, control, reputation), its opening charge with on-scene evidence, the turn trigger, and a closing charge **different from the opening**. A dialogue scene that flips no value must be deleted or merged.
- `power_trajectory`: who holds power at open and on what basis, the flip beat where power first changes hands, and who holds it at close. Power must change hands at least once; it may flip back.
- `information_asymmetry`: what the POV knows, what the POV lacks (and will misread), what the other party holds, plus the **reader position** — `reader_ahead` (dramatic irony), `reader_level`, or `reader_behind` (suspense) — and how the gap is exploited, exposed, or widened this scene.
- `audience_presence`: `none` or the concrete third parties present; when present, whom each side performs for and how audience reaction bends the dialogue. `public_confrontation` requires a non-none audience.
- `medium`: face_to_face, phone, text_message, letter, through_door, intermediary; non-face-to-face scenes replace body beats with medium beats.
- `address_shift` (optional but encouraged for Chinese webnovels): how forms of address drift under pressure — honorific dropped, given name used, politeness suddenly restored. Address drift is its own subtext line and must be designed, not random.
- `pov_role` / `participants` / `coalition_shift`: for overheard and group scenes.

## Required Shape

Each blueprint must also include the original fields:

- `dialogue_mode` + `mode_reason`, `scene_pressure`, `emotional_temperature`, `relationship_frame`.
- `opening_strategy`: dialogue first, action first, object first, silence first, misunderstanding first, memory then interruption, environmental voice, or another justified strategy.
- `first_spoken_moment`, `entry_line`, `entry_speaker`, `location_anchor`.
- `pov_state`, `inner_question`, `memory_bridge`, `identity_grounding`.
- `dialogue_objective`, `interlocutor_agenda`, `protagonist_response_strategy`.
- `objective_tactics`: what each character wants right now, what tactic they use, how the other party counters, where emotion leaks, and what changes. In group scenes each tactic also carries a `faction`.
- `turn_progression`: two or more pressure/information turns. Every turn must carry surface function, hidden subtext, and the next pressure. `action_beat` is optional and is present only when an action changes power, hides information, interrupts speech, or affects the physical outcome.
- `directness_policy`, `silence_policy`, `info_release_policy`, `exposition_budget`, `escalation_pattern`, `beat_density`, `subtext_source`, `subtext_and_power_shift`.
- `exit_beat`: a concrete field change, object state, body action, relationship coldness, or unfinished choice.

## Six Dialogue Laws

Distilled for Writer execution and Editor review (mirrored in `skills/*/references/dialogue-mastery.md`):

1. **Dialogue is tactics.** Every line is an action a character takes toward what they want, not a statement of information. A blocked tactic must change; the same tactic three turns in a row is filler.
2. **Nobody states their desire.** A line that says the want outright is allowed only at a full emotional break, at most once per scene.
3. **Every turn changes one of four things:** information, pressure, relationship temperature, or power position. A turn that changes none is deleted.
4. **Every scene flips a value.** If the closing charge equals the opening charge, the scene is deleted or merged (enforced by `plan_chapter` validation).
5. **Power changes hands at least once.** The opening holder must lose the rhythm at least once; they may take it back.
6. **Address is subtext.** Drift in what characters call each other is a designed subtext line, not noise.

Plus two realism rules: replies may not connect (answering a different question, answering with a question, picking up the wrong emphasis — a perfectly meshed Q&A reads fake); and information gaps are three-way (party A, party B, and the reader).

## Anti-Patterns

Do not use the blueprint as:

- a fixed "dialogue first" template for every scene;
- a way to copy a sample's real-world institution or occupation;
- a menu of options shown to readers;
- a replacement for character dossiers, world rules, or chapter contracts;
- a route around POV limits.
- a paragraph-by-paragraph script. The draft profile strips `turn_progression` and action choreography into a smaller `render_packet`; the Drafter receives scene purpose and voice constraints, not a sequence to transcribe.

Three consecutive dialogue paragraphs beginning with a character action and then a colon/quote are a renderer failure, even if every individual action came from a valid blueprint. Once space and speakers are grounded, use tagless lines, neutral tags, interruption, partial answers, group reaction, or silence. Each location change must also expose the POV character's reason for going there; editing words such as "then" or "went downstairs" do not supply causality.

If a scene works better without dialogue first, choose `action_first`, `object_first`, `silence_first`, `misunderstanding_first`, or another `opening_strategy` and explain why. Key dialogues must be planned before drafting, so the writer cannot drift into exposition-first prose or identical voices.

## Source Basis

The mode system and laws are distilled from:

- Robert McKee (Story / Dialogue): desire → tactics → beats; every scene must turn a value's charge; dialogue sparkles when the audience tracks the give-and-take of tactics.
- Writing Excuses on dialogue's job, scene conflict, context, subtext, and character voice.
- Reedsy on scene-based dialogue, action beats, voice differentiation, and cutting filler.
- K.M. Weiland / Jane Friedman on dialogue conflict as goal plus obstacle, including hidden inner stakes.
- Gotham Writers Workshop on subtext as meaning beneath speech.
- Writers Helping Writers on tension sources such as opposing goals, emotion, insecurity, bias, assumptions, annoyance, culture, and subtext.
- Screenwriting scene taxonomies (Final Draft's 75 scene types) for the reveal/betrayal/recruitment/farewell/reconciliation families.
- Chinese webnovel craft (知乎 / 阅文作家助手 community material) for public confrontation (打脸), showdown (摊牌), address-shift subtext, and audience-performance dynamics.

## Pipeline Contract

`plan_chapter` requires at least one blueprint when `causal_simulation` is present, and validates the orthogonal axes: `medium`, `audience_presence.present`, `information_asymmetry` (pov_lacks / other_holds / reader_position / asymmetry_play), full `value_shift` with closing ≠ opening, full `power_trajectory`, plus mode-conditional requirements for `group_council`, `overheard`, and `public_confrontation`. Zero-init writes a reusable baseline into `meta/prewrite_storycraft_plan.json` and transfers it into `drafts/01.zero_init.plan.json`.

Writer must execute the blueprint in prose unless a stronger chapter constraint overrides it. Editor checks whether the final text:

- opens key dialogue with a world-appropriate voice or carrier;
- grounds place before dumping background;
- gives the POV character a human delay or mistake;
- keeps the memory bridge short;
- gives the interlocutor their own agenda;
- flips the planned value and moves power at least once (value-static or power-static key scenes are S3 findings in story-review);
- avoids tactic repetition (same move three turns running);
- treats address drift as designed subtext;
- exits through a concrete scene consequence rather than a button, sudden noise, or abstract hook.
