# AI Writing Signals — English / Other Languages

> Mirror of `signals-zh.md` for non-Chinese texts. Use the same six-dimension framework, translated:
>
> 1. Syntax & rhythm (CV)
> 2. Diction & clichés
> 3. Emotion & psychology
> 4. Concrete detail & texture
> 5. Metaphor & imagery
> 6. Voice, coherence, risk

For specific English signals (e.g., "delve into", "tapestry", "vibrant", "in the realm of", "navigate the complexities of", em-dash overuse, "It's important to note that", balanced three-item lists, perfect parallel structure), see widely-circulated public lists such as:

- Wikipedia "Signs of AI writing" — current consensus list
- OpenAI / GPTZero / Originality.ai public signal documentation
- Any "ChatGPT tells" compilation; the canonical examples shift every model generation, so this file intentionally stays high-level

**Key principle**: the same 0–5 dimension scores apply. The English cliché set differs (see above), but the *category* of warning is the same: uniform rhythm, named emotions, abstract detail, stock imagery, "good-student essay" voice, theme closures that are too clean.

**Paragraph-level self-duplication** (the dimension most likely to trip a publishing platform's duplicate detector) is language-agnostic — `quality/audit/scripts/paragraph_dup.py` works on any UTF-8 text by treating runs of non-CJK characters as the equivalent of "汉字".

For Chinese, the workflow is:
```bash
python3 quality/audit/scripts/text_signals.py <file>        # 量化信号 + 段落级 + 句子级
python3 quality/audit/scripts/paragraph_dup.py <file>        # 仅段落级（独立可跑）
```
