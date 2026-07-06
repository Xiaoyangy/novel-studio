---
genre: 42
chapter_words: "not-a-range"
forbidden_chars: "should-be-list"
forbidden_phrases:
  - 1
  - 2
fatigue_words: true
---

# 类型错误

每个字段都用错类型；checker 应跳过这些字段并写 conflicts。
正文仍应作为偏好注入。
