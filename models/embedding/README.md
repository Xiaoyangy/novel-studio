# 本地 embedding 模型（Task 071）

`Qwen3-Embedding-0.6B-Q8_0.gguf`（约 610MB，来源 [Qwen/Qwen3-Embedding-0.6B-GGUF](https://huggingface.co/Qwen/Qwen3-Embedding-0.6B-GGUF)）：
1024 维（MRL 32-1024）、8k 输入窗、C-MTEB 中文强基线。由 llama.cpp 的 `llama-server
--embedding --pooling last` 提供 OpenAI 兼容 /v1/embeddings，`rag.embedding.local_gguf`
配置后引擎自动拉起（端口默认 18434）。客户端自动补 `<|endoftext|>` 并做 L2 归一化。

缺失时重新下载：
```bash
curl -L -o models/embedding/Qwen3-Embedding-0.6B-Q8_0.gguf \
  "https://huggingface.co/Qwen/Qwen3-Embedding-0.6B-GGUF/resolve/main/Qwen3-Embedding-0.6B-Q8_0.gguf"
```
备选：`bge-m3`（ollama pull bge-m3，1024 维）走 rag.embedding.base_url 常规配置。
