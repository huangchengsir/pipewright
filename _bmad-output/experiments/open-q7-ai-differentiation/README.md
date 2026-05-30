# Open Q#7 实验装置 —— 怎么用

验证「现成 LLM + 知识库 + 成败 diff」是否**显著优于**把日志粘进 ChatGPT。设计与判据见 **`protocol.md`**(先读它)。

## 文件
- `protocol.md` — 实验设计 / 对照臂 / 指标 / **预注册显著阈值与 GO·PARTIAL·NO-GO 决策规则** / 诚实约束。
- `harness/run.mjs` — 4 臂跑批 + 盲评 judge + 复合分聚合 + markdown 报告。
- `harness/stub-llm.mjs` — 确定性 stub(离线 dry-run)。
- `dataset/seed-cases.json` — 6 拟真种子(含 diff-decisive / kb-decisive / log-evident 控制案)。
- `results/` — 报告与原始输出落盘。

## 1. 先跑 dry-run 验装置(无需网络/LLM)
```bash
cd _bmad-output/experiments/open-q7-ai-differentiation
node harness/run.mjs --stub
```
→ 产出 `results/stub-dryrun-report.md`。**stub 的任何数字都是占位,不是差异化证据**——它只证明流水线(prompt 装配 / 4 臂 / 盲评 / 聚合 / 报告)机械正确。

## 2. 真跑(得真结论)—— 需两样本机没有的输入
### (a) 真实数据集
复制 `dataset/seed-cases.json` 为 `dataset/cases.json`,按相同 schema 填入作者 **~20 条真实失败日志**。**刻意纳入足量 diff-decisive 案**(根因只在「上次绿这次红改了什么」里、不在日志里)——否则实验会假性得出"无差异"(全是日志自明的案,ChatGPT 裸粘也能答对)。每案必须有 `groundTruth`(真实根因 + 决定性行 + 正确修复)。

> 失败日志可从平台取:7-2 已把失败日志落 `pipeline_runs.failure_log`;`diffSummary` 暂手工填(7-3 落地后自动)。**日志入数据集前自行脱敏**(去真实 token/密码),本仓只放脱敏后文本。

### (b) 一个连得通的 LLM(本机 GFW 拦 anthropic/openai、无 Ollama)
最简:本地装 [Ollama](https://ollama.com) 拉一个中等编码模型:
```bash
ollama pull qwen2.5-coder:7b      # 底座(4 臂同用,控制变量)
ollama pull qwen2.5-coder:14b     # 裁判(更强,盲评)
node harness/run.mjs \
  --dataset dataset/cases.json \
  --provider ollama --base http://localhost:11434 --model qwen2.5-coder:7b \
  --judge-provider ollama --judge-base http://localhost:11434 --judge-model qwen2.5-coder:14b
```
或经 VPN 指向 OpenAI/Claude:
```bash
node harness/run.mjs --dataset dataset/cases.json \
  --provider openai --base https://api.openai.com --model gpt-4o-mini --key $OPENAI_KEY \
  --judge-provider openai --judge-base https://api.openai.com --judge-model gpt-4o --judge-key $OPENAI_KEY
```
→ 产出 `results/run-report.md`(各臂均值 + C−A/D−A/C−B/D−C 差值 + A-未答对子集胜率 + 决策)。

## 3. 读结论(对照 protocol §6)
- **GO**:平台最佳臂(C/D)对 A 提升 ≥25% **且** A-未答对子集胜率 ≥60% **且** C−B 显示 diff 有真实边际 → 按计划建 7-3/7-5。
- **PARTIAL**:D−A 过阈但 C−B 弱 → KB 有用、diff 没用 → 重排序。
- **NO-GO**:平台最佳臂 ≈ A → **AI 护城河不在诊断答案质量**,差异化转向集成/零摩擦/反馈飞轮。**这是好结论**——它在投入 7-3/7-5 前拦下伪需求。

## 4. 校准(可信度)
judge 是 LLM,会错。真跑后**作者人工复评 ≥5 案**,与 judge 比对(Cohen's κ);κ<0.6 则 judge 不可信,回退全人评。`results/run-raw.json` 存了每臂原始输出供人工复核。
